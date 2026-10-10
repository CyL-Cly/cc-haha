// Package diagnosticsapi owns the desktop diagnostic files. The CLI logger's
// segmented stream is consumed without taking ownership of its active files.
package diagnosticsapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const retentionDays = 7
const maxBytes int64 = 50 * 1024 * 1024
const maxDiagnosticsBytes int64 = 20 * 1024 * 1024
const maxAuxiliaryBytes int64 = 5 * 1024 * 1024
const maxCliBytes int64 = 5 * 1024 * 1024
const maxExportBytes int64 = 14 * 1024 * 1024
const textExportLength = 256 * 1024
const truncatedOlder = "[TRUNCATED OLDER CONTENT]\n"

type Options struct {
	HomeDir, ConfigDir, Version string
	StartedAt                   time.Time
	Now                         func() time.Time
	OpenDir                     func(context.Context, string) error
	LocalIndexStatus            func() (any, error)
	RebuildLocalIndex           func(context.Context) (any, error)
}
type Service struct {
	options   Options
	mu        sync.Mutex
	lastSweep time.Time
}
type Event struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Severity  string          `json:"severity"`
	Summary   string          `json:"summary"`
	SessionID string          `json:"sessionId,omitempty"`
	Details   json.RawMessage `json:"details,omitempty"`
	original  *protocol.JSONObject
}

func (event *Event) UnmarshalJSON(raw []byte) error {
	type fields Event
	var value fields
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var original protocol.JSONObject
	if err := json.Unmarshal(raw, &original); err != nil {
		return err
	}
	*event = Event(value)
	event.original = &original
	return nil
}
func (event Event) MarshalJSON() ([]byte, error) {
	if event.original != nil {
		return json.Marshal(event.original)
	}
	type fields Event
	return json.Marshal(fields(event))
}

type Input struct {
	Type, Severity, Summary, SessionID string
	Details                            json.RawMessage
}
type Status struct {
	LogDir               string  `json:"logDir"`
	DiagnosticsPath      string  `json:"diagnosticsPath"`
	CliDiagnosticsPath   string  `json:"cliDiagnosticsPath"`
	RuntimeErrorsPath    string  `json:"runtimeErrorsPath"`
	ElectronHostPath     string  `json:"electronHostPath"`
	ExportDir            string  `json:"exportDir"`
	RetentionDays        int     `json:"retentionDays"`
	MaxBytes             int64   `json:"maxBytes"`
	TotalBytes           int64   `json:"totalBytes"`
	StorageLimitExceeded bool    `json:"storageLimitExceeded"`
	EventCount           int     `json:"eventCount"`
	PhysicalLineCount    int     `json:"physicalLineCount"`
	CorruptLineCount     int     `json:"corruptLineCount"`
	RecentErrorCount     int     `json:"recentErrorCount"`
	LastEventAt          *string `json:"lastEventAt"`
}
type Export struct {
	Path     string `json:"path"`
	FileName string `json:"fileName"`
	Bytes    int    `json:"bytes"`
}

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.HomeDir) {
		return nil, errors.New("Diagnostics require explicit absolute home/config paths")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.StartedAt.IsZero() {
		options.StartedAt = options.Now()
	}
	if options.Version == "" {
		options.Version = "999.0.0-local"
	}
	if options.OpenDir == nil {
		options.OpenDir = openDirectory
	}
	return &Service{options: options}, nil
}
func (s *Service) LogDir() string {
	return filepath.Join(s.options.ConfigDir, "cc-haha", "diagnostics")
}
func (s *Service) DiagnosticsPath() string { return filepath.Join(s.LogDir(), "diagnostics.jsonl") }
func (s *Service) CliDiagnosticsPath() string {
	return filepath.Join(s.LogDir(), "cli-diagnostics.jsonl")
}
func (s *Service) RuntimeErrorsPath() string { return filepath.Join(s.LogDir(), "runtime-errors.log") }
func (s *Service) ElectronHostPath() string  { return filepath.Join(s.LogDir(), "electron-host.log") }
func (s *Service) ExportDir() string         { return filepath.Join(s.LogDir(), "exports") }
func iso(t time.Time) string                 { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func identifier() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
func IsSeverity(v string) bool { return v == "debug" || v == "info" || v == "warn" || v == "error" }
func (s *Service) Record(input Input) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := identifier()
	if err != nil {
		return Event{}, err
	}
	severity := input.Severity
	if !IsSeverity(severity) {
		severity = "info"
	}
	event := Event{ID: id, Timestamp: iso(s.options.Now()), Type: input.Type, Severity: severity, Summary: s.SanitizeString(input.Summary, 4096), SessionID: s.SanitizeString(input.SessionID, 256)}
	if input.Details != nil {
		event.Details = s.sanitizeValue(input.Details, 0)
	}
	if err = s.ensure(); err == nil {
		var data []byte
		data, err = json.Marshal(event)
		if err == nil {
			err = s.writeFile(s.DiagnosticsPath(), append(data, '\n'), true)
		}
	}
	if err == nil && (severity == "warn" || severity == "error") {
		err = s.writeFile(s.RuntimeErrorsPath(), []byte(runtimeEntry(event)), true)
	}
	if err != nil {
		return Event{}, errors.New(strings.ReplaceAll(s.SanitizeString(err.Error(), 4096), s.options.ConfigDir, "$CLAUDE_CONFIG_DIR"))
	}
	_ = s.retention(false, true)
	return event, nil
}
func runtimeEntry(e Event) string {
	head := "[" + e.Timestamp + "] " + strings.ToUpper(e.Severity) + " " + e.Type
	if e.SessionID != "" {
		head += " session=" + e.SessionID
	}
	lines := []string{head, "summary: " + e.Summary}
	if e.Details != nil {
		var pretty bytes.Buffer
		_ = json.Indent(&pretty, e.Details, "", "  ")
		lines = append(lines, "details:", pretty.String())
	}
	return strings.Join(lines, "\n") + "\n\n"
}
func (s *Service) GetStatus() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return Status{}, err
	}
	if err := s.retention(true, false); err != nil {
		return Status{}, err
	}
	scan, err := s.scan()
	if err != nil {
		return Status{}, err
	}
	files, err := s.listFiles(s.LogDir())
	if err != nil {
		return Status{}, err
	}
	v := Status{LogDir: s.LogDir(), DiagnosticsPath: s.DiagnosticsPath(), CliDiagnosticsPath: s.CliDiagnosticsPath(), RuntimeErrorsPath: s.RuntimeErrorsPath(), ElectronHostPath: s.ElectronHostPath(), ExportDir: s.ExportDir(), RetentionDays: retentionDays, MaxBytes: maxBytes, EventCount: len(scan.Events), PhysicalLineCount: scan.Physical, CorruptLineCount: scan.Corrupt}
	var cliBytes, exports int64
	for _, f := range files {
		v.TotalBytes += f.Size
		if isCli(filepath.Base(f.Path)) && filepath.Dir(f.Path) == s.LogDir() {
			cliBytes += f.Size
		}
		if inside(s.ExportDir(), f.Path) {
			exports += f.Size
		}
		if f.Path == s.DiagnosticsPath() && f.Size > maxDiagnosticsBytes || f.Path == s.RuntimeErrorsPath() && f.Size > maxAuxiliaryBytes || f.Path == s.ElectronHostPath() && f.Size > maxAuxiliaryBytes {
			v.StorageLimitExceeded = true
		}
	}
	v.StorageLimitExceeded = v.StorageLimitExceeded || v.TotalBytes > maxBytes || cliBytes > maxCliBytes || exports > maxExportBytes
	cutoff := s.options.Now().Add(-24 * time.Hour)
	for _, e := range scan.Events {
		t, _ := parseDate(e.Timestamp)
		if (e.Severity == "warn" || e.Severity == "error") && !t.Before(cutoff) {
			v.RecentErrorCount++
		}
	}
	if len(scan.Events) > 0 {
		last := scan.Events[len(scan.Events)-1].Timestamp
		v.LastEventAt = &last
	}
	return v, nil
}
func (s *Service) ReadRecent(limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	scan, err := s.scan()
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, min(limit, len(scan.Events)))
	for i := len(scan.Events) - 1; i >= 0 && len(events) < limit; i-- {
		events = append(events, scan.Events[i])
	}
	return events, nil
}
func (s *Service) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.LogDir()); err != nil {
		return err
	}
	return s.ensure()
}
func (s *Service) OpenLogDir(ctx context.Context) error {
	s.mu.Lock()
	err := s.ensure()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.options.OpenDir(ctx, s.LogDir())
}
func openDirectory(_ context.Context, path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
func (s *Service) LocalIndexStatus() (any, error) {
	if s.options.LocalIndexStatus == nil {
		return nil, &CapabilityError{"local index"}
	}
	return s.options.LocalIndexStatus()
}
func (s *Service) RebuildLocalIndex(ctx context.Context) (any, error) {
	if s.options.RebuildLocalIndex == nil {
		return nil, &CapabilityError{"local index rebuild"}
	}
	return s.options.RebuildLocalIndex(ctx)
}

type CapabilityError struct{ Capability string }

func (e *CapabilityError) Error() string {
	return "Go server capability is not implemented: " + e.Capability
}
func (s *Service) appInfo() map[string]any {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return map[string]any{"appVersion": s.options.Version, "platform": platform, "arch": arch, "go": runtime.Version(), "node": nil, "bun": nil, "uptimeSeconds": int(s.options.Now().Sub(s.options.StartedAt).Seconds() + .5), "generatedAt": iso(s.options.Now())}
}
func (s *Service) BuildIssueReport() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return "", err
	}
	if err := s.retention(true, true); err != nil {
		return "", err
	}
	scan, err := s.scan()
	if err != nil {
		return "", err
	}
	events := newestShared(scan.Events, 100)
	providers := s.providers()
	return buildIssueReport(iso(s.options.Now()), s.appInfo(), providers, events, scan.Corrupt), nil
}
func (s *Service) ExportBundle() (Export, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return Export{}, err
	}
	if err := s.retention(true, true); err != nil {
		return Export{}, err
	}
	scan, err := s.scan()
	if err != nil {
		return Export{}, err
	}
	events := newestShared(scan.Events, 5000)
	cli, err := s.readCli()
	if err != nil {
		return Export{}, err
	}
	electron, err := s.textTail(s.ElectronHostPath(), textExportLength)
	if err != nil {
		return Export{}, err
	}
	var structured strings.Builder
	for _, e := range events {
		b, _ := json.Marshal(e)
		structured.Write(b)
		structured.WriteByte('\n')
	}
	files := []struct{ Name, Content string }{{"README.txt", readme}, {"app-info.json", prettyJSON(s.appInfo())}, {"diagnostics.jsonl", structured.String()}, {"recent-errors.md", s.errorSummary(events)}, {"runtime-errors.log", sharedRuntime(events)}, {"cli-diagnostics.jsonl", cli}, {"electron-host.log", electron}, {"providers-summary.json", prettyJSON(s.providers())}, {"sessions-summary.json", prettyJSON(s.sessions(events))}}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gzipWriter)
	for _, file := range files {
		b := []byte(file.Content)
		if err = tw.WriteHeader(&tar.Header{Name: file.Name, Mode: 0600, Size: int64(len(b)), ModTime: s.options.Now(), Format: tar.FormatUSTAR}); err != nil {
			return Export{}, err
		}
		if _, err = tw.Write(b); err != nil {
			return Export{}, err
		}
	}
	if err = tw.Close(); err != nil {
		return Export{}, err
	}
	if err = gzipWriter.Close(); err != nil {
		return Export{}, err
	}
	name := "cc-haha-diagnostics-" + strings.NewReplacer(":", "-", ".", "-").Replace(iso(s.options.Now())) + ".tar.gz"
	path := filepath.Join(s.ExportDir(), name)
	if err = s.writeFile(path, archive.Bytes(), false); err != nil {
		return Export{}, err
	}
	if err = s.exportRetention(s.options.Now().Add(-retentionDays * 24 * time.Hour)); err != nil {
		return Export{}, err
	}
	return Export{path, name, archive.Len()}, nil
}
func prettyJSON(v any) string            { b, _ := json.MarshalIndent(v, "", "  "); return string(b) + "\n" }
func readAll(f *os.File) ([]byte, error) { return io.ReadAll(f) }
