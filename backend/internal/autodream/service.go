// Package autodream preserves the memory consolidation scheduler and on-disk
// lease. The runtime owns the fork, tool authority and task registry.
package autodream

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	MemoryDirectory     func() (string, error)
	TranscriptDirectory func() (string, error)
	NowMillis           func() float64
	PID                 int
	IsProcessRunning    func(int) bool
	OnError             func(error)
}
type Gates struct {
	Kairos, Remote, AutoMemoryEnabled bool
	Enabled                           *bool
	FeatureConfig                     protocol.JSONObject
	CurrentSessionID                  string
}
type Attempt struct {
	PriorMtime                float64
	SessionIDs                []string
	MemoryRoot, TranscriptDir string
}
type Config struct{ MinHours, MinSessions float64 }
type Service struct {
	options           Options
	lastSessionScanAt float64
}

func New(options Options) (*Service, error) {
	if options.MemoryDirectory == nil || options.TranscriptDirectory == nil || options.IsProcessRunning == nil || options.PID <= 1 {
		return nil, errors.New("auto-dream requires its actual memory/transcript paths and process identity")
	}
	if options.NowMillis == nil {
		options.NowMillis = func() float64 { return float64(time.Now().UnixNano()) / 1e6 }
	}
	return &Service{options: options}, nil
}
func Configuration(raw protocol.JSONObject) Config {
	value := Config{24, 5}
	for key, target := range map[string]*float64{"minHours": &value.MinHours, "minSessions": &value.MinSessions} {
		bytes := raw.Get(key)
		if len(bytes) == 0 || bytes[0] == '"' {
			continue
		}
		number, err := strconv.ParseFloat(string(bytes), 64)
		if err == nil && number > 0 && !math.IsNaN(number) && !math.IsInf(number, 0) {
			*target = number
		}
	}
	return value
}
func Enabled(setting *bool, feature protocol.JSONObject) bool {
	if setting != nil {
		return *setting
	}
	return string(feature.Get("enabled")) == "true"
}
func (s *Service) failure(err error) {
	if err != nil && s.options.OnError != nil {
		s.options.OnError(err)
	}
}
func (s *Service) memoryRoot() (string, error) {
	root, err := s.options.MemoryDirectory()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("auto-dream memory directory must be absolute")
	}
	return root, nil
}
func (s *Service) TryStart(gates Gates) *Attempt {
	if gates.Kairos || gates.Remote || !gates.AutoMemoryEnabled || !Enabled(gates.Enabled, gates.FeatureConfig) {
		return nil
	}
	cfg := Configuration(gates.FeatureConfig)
	last := s.ReadLastConsolidatedAt()
	if (s.options.NowMillis()-last)/3600000 < cfg.MinHours {
		return nil
	}
	if s.options.NowMillis()-s.lastSessionScanAt < 600000 {
		return nil
	}
	s.lastSessionScanAt = s.options.NowMillis()
	ids, err := s.ListSessionsTouchedSince(last)
	if err != nil {
		s.failure(err)
		return nil
	}
	selected := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != gates.CurrentSessionID {
			selected = append(selected, id)
		}
	}
	if float64(len(selected)) < cfg.MinSessions {
		return nil
	}
	prior, err := s.TryAcquire()
	if err != nil {
		s.failure(err)
		return nil
	}
	if prior == nil {
		return nil
	}
	memory, err := s.memoryRoot()
	if err != nil {
		s.failure(err)
		return nil
	}
	transcripts, err := s.options.TranscriptDirectory()
	if err != nil {
		s.failure(err)
		return nil
	}
	return &Attempt{*prior, selected, memory, transcripts}
}

var candidateUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Service) ListSessionsTouchedSince(since float64) ([]string, error) {
	dir, err := s.options.TranscriptDirectory()
	if err != nil {
		return nil, err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return []string{}, nil
	}
	defer directory.Close()
	// Source readdir preserves the host's directory order. os.ReadDir sorts
	// names and would change the session hints supplied to the dream prompt.
	names, err := directory.ReadDir(-1)
	if err != nil {
		return []string{}, nil
	}
	result := []string{}
	for _, name := range names {
		if !strings.HasSuffix(name.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name.Name(), ".jsonl")
		if !candidateUUID.MatchString(id) {
			continue
		}
		// Source stat follows symlinks and does not require a regular file here.
		info, err := os.Stat(filepath.Join(dir, name.Name()))
		if err == nil && float64(info.ModTime().UnixMilli()) > since {
			result = append(result, id)
		}
	}
	return result, nil
}
func AttemptExtra(attempt Attempt) string {
	hints := make([]string, len(attempt.SessionIDs))
	for i, id := range attempt.SessionIDs {
		hints[i] = "- " + id
	}
	return "\n\n**Tool constraints for this run:** Bash is restricted to read-only commands (`ls`, `find`, `grep`, `cat`, `stat`, `wc`, `head`, `tail`, and similar). Anything that writes, redirects to a file, or modifies state will be denied. Plan your exploration with this in mind — no need to probe.\n\nSessions since last consolidation (" + strconv.Itoa(len(attempt.SessionIDs)) + "):\n" + strings.Join(hints, "\n")
}
