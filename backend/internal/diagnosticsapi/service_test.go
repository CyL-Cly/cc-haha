package diagnosticsapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)

func testService(t *testing.T) *Service {
	t.Helper()
	home := t.TempDir()
	s, err := New(Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude"), Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func seedFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func tarFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	result := map[string]string{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Mode != 0600 {
			t.Fatalf("private archive entry mode: %o", header.Mode)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		result[header.Name] = string(b)
	}
	return result
}
func TestDiagnosticsConcurrentWritesRestartAndExportPrivacy(t *testing.T) {
	s := testService(t)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Record(Input{Type: "tool_failure", Severity: "error", Summary: "private chat token=never-share", SessionID: "private-session", Details: json.RawMessage(`{"errorCode":"CLI_EXITED","status":"failed","apiKey":"never-share","toolInput":{"prompt":"private prompt"},"error":{"name":"TypeError","message":"private failure","stack":"private trace"}}`)})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	status, err := s.GetStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.EventCount != 100 || status.RecentErrorCount != 100 {
		t.Fatalf("lost events: %+v", status)
	}
	again, err := New(s.options)
	if err != nil {
		t.Fatal(err)
	}
	events, err := again.ReadRecent(1000)
	if err != nil || len(events) != 100 {
		t.Fatalf("restart events %d %v", len(events), err)
	}
	for _, path := range []string{s.LogDir(), s.ExportDir()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private directory %s %+v %v", path, info, err)
		}
	}
	bundle, err := s.ExportBundle()
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, bundle.Path)
	if len(files) != 9 {
		t.Fatal(files)
	}
	for name, content := range files {
		if name == "app-info.json" {
			continue
		}
		for _, forbidden := range []string{"private chat", "never-share", "private prompt", "private failure", "private trace"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s leaks %q", name, forbidden)
			}
		}
	}
	report, err := s.BuildIssueReport()
	if err != nil || !strings.Contains(report, "CLI_EXITED") || strings.Contains(report, "private chat") {
		t.Fatalf("issue report %v %s", err, report)
	}
}
func TestDiagnosticsCorruptionEvidencePersistsAndUnknownFieldsSurvive(t *testing.T) {
	s := testService(t)
	raw := `{"id":"old","timestamp":"2026-10-05T00:00:00.000Z","type":"old","severity":"error","summary":"safe","future":{"preserve":true}}` + "\n{broken\n\n"
	seedFile(t, s.DiagnosticsPath(), []byte(raw))
	status, err := s.GetStatus()
	if err != nil || status.CorruptLineCount != 2 || status.PhysicalLineCount != 3 {
		t.Fatalf("status %+v %v", status, err)
	}
	if b, _ := os.ReadFile(s.DiagnosticsPath()); string(b) != raw {
		t.Fatal("status compacted current corruption evidence")
	}
	_, err = s.ExportBundle()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := New(s.options)
	status, err = again.GetStatus()
	if err != nil || status.CorruptLineCount != 2 || status.PhysicalLineCount != 1 {
		t.Fatalf("persisted %+v %v", status, err)
	}
	events, _ := again.ReadRecent(100)
	b, _ := json.Marshal(events)
	if !strings.Contains(string(b), `"future":{"preserve":true}`) {
		t.Fatalf("unknown fields discarded %s", b)
	}
	if err = again.Clear(); err != nil {
		t.Fatal(err)
	}
	status, err = again.GetStatus()
	if err != nil || status.CorruptLineCount != 0 || status.EventCount != 0 {
		t.Fatalf("clear %+v %v", status, err)
	}
}
func TestDiagnosticsRejectsSymlinksAndDoesNotTouchOutside(t *testing.T) {
	for _, leaf := range []string{"cc-haha", "cc-haha/diagnostics", "cc-haha/diagnostics/diagnostics.jsonl", "cc-haha/diagnostics/electron-host.log"} {
		t.Run(leaf, func(t *testing.T) {
			s := testService(t)
			outside := t.TempDir()
			target := outside
			if filepath.Ext(leaf) != "" {
				target = filepath.Join(outside, "protected.txt")
				seedFile(t, target, []byte("protected"))
			}
			path := filepath.Join(s.options.ConfigDir, filepath.FromSlash(leaf))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			_, err := s.Record(Input{Type: "probe", Summary: "test"})
			if err == nil {
				t.Fatal("followed diagnostics symlink")
			}
			if filepath.Ext(leaf) != "" {
				if b, _ := os.ReadFile(target); string(b) != "protected" {
					t.Fatal("protected target touched")
				}
			}
			if _, err = s.GetStatus(); err == nil {
				t.Fatal("status followed symlink")
			}
		})
	}
}
func TestDiagnosticsRetentionPreservesLiveCLISegmentsAndQuarantinesDead(t *testing.T) {
	s := testService(t)
	live := s.CliDiagnosticsPath() + fmt.Sprintf(".%d.current.jsonl", os.Getpid())
	dead := s.CliDiagnosticsPath() + ".99999999.current.jsonl"
	seedFile(t, live, []byte("active\n"))
	seedFile(t, dead, []byte("dead evidence\n"))
	stale := s.CliDiagnosticsPath() + ".completed.jsonl"
	seedFile(t, stale, []byte("old\n"))
	past := testNow.Add(-8 * 24 * time.Hour)
	_ = os.Chtimes(stale, past, past)
	status, err := s.GetStatus()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(live); err != nil {
		t.Fatal("live segment removed", err)
	}
	if _, err = os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("dead current segment was not quarantined")
	}
	matches, _ := filepath.Glob(s.CliDiagnosticsPath() + ".99999999.reclaimed.*.jsonl")
	if len(matches) != 1 {
		t.Fatal(matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != "dead evidence\n" {
		t.Fatal("quarantined evidence lost")
	}
	if status.TotalBytes <= 0 {
		t.Fatal("missing diagnostic data")
	}
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale completed segment retained")
	}
}
func TestDiagnosticsTailRedactsBoundarySecrets(t *testing.T) {
	s := testService(t)
	path := s.CliDiagnosticsPath()
	secret := "BOUNDARY_SPLIT_SECRET"
	suffix := secret + "\n\nLATEST_FAILURE_MARKER\n"
	padding := strings.Repeat("safe line\n", (textExportLength-len(suffix))/10)
	seedFile(t, path, []byte(strings.Repeat("oldprefix", 600)+"\ntoken="+secret+"\n"+padding+"\nLATEST_FAILURE_MARKER\n"))
	bundle, err := s.ExportBundle()
	if err != nil {
		t.Fatal(err)
	}
	content := tarFiles(t, bundle.Path)["cli-diagnostics.jsonl"]
	if strings.Contains(content, secret) || !strings.Contains(content, "LATEST_FAILURE_MARKER") {
		t.Fatal("unsafe tail export", content)
	}
}

func sourceDiagnostics(t *testing.T, input any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun is unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-diagnostics.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_DESKTOP_DIAGNOSTICS_FIXTURE=1", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	raw, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source diagnostics: %v %s", err, stderr.String())
	}
	var result []json.RawMessage
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("Source output %s %v", out, err)
	}
	return result
}
func normalized(v any) any {
	raw, _ := json.Marshal(v)
	text := string(raw)
	text = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`).ReplaceAllString(text, "<event-id>")
	var out any
	_ = json.Unmarshal([]byte(text), &out)
	return out
}
func TestDiagnosticsSanitizationAndSharingAgainstActualSource(t *testing.T) {
	s := testService(t)
	event := Event{ID: "id-1", Timestamp: iso(testNow), Type: "sdk_result_error", Severity: "error", SessionID: "s-1", Summary: "PRIVATE_CHAT", Details: json.RawMessage(`{"errorCode":"CLI_EXITED","status":"failed","output":"PRIVATE_OUTPUT","nested":{"durationMs":7,"prompt":"PRIVATE_PROMPT"},"error":{"name":"TypeError","message":"PRIVATE_ERROR","stack":"PRIVATE_TRACE"},"route":"/api/sessions/00000000-0000-0000-0000-000000000000/messages","unsafe":"PRIVATE_UNSAFE"}`)}
	stringsToCheck := []string{"Bearer fake-secret token=client-secret https://user:pass@example.com/path?token=x", "ANTHROPIC_AUTH_TOKEN=fake-secret; api-key=other", "/tmp/private/file C:\\Users\\private\\file AKIAABCDEFGHIJKLMNOP"}
	stringsToCheck = append(stringsToCheck, "Bearer\u00a0fake-secret auth_token\ufeff=\ufeffprivate", "/tmp/private\u00a0tail C:\\Users\\private\u00a0tail")
	cases := []any{}
	got := []any{}
	for _, text := range stringsToCheck {
		cases = append(cases, map[string]any{"action": "sanitize", "value": text})
		got = append(got, s.SanitizeString(text, 4096))
	}
	cases = append(cases, map[string]any{"action": "share", "event": event})
	got = append(got, ShareEvent(event))
	value := json.RawMessage(`{"apiKey":"private","token":"private","nested":{"summary":"token=private"},"items":[1,2,3]}`)
	cases = append(cases, map[string]any{"action": "value", "value": value})
	got = append(got, s.sanitizeValue(value, 0))
	source := sourceDiagnostics(t, map[string]any{"now": iso(testNow), "cases": cases})
	for i, row := range source {
		if !reflect.DeepEqual(normalized(got[i]), normalized(row)) {
			t.Fatalf("case %d\nGo %s\nSource %s", i, prettyJSON(got[i]), row)
		}
	}
}

func TestDiagnosticsArchiveContentsAgainstActualSource(t *testing.T) {
	s := testService(t)
	raw := `{"id":"id-1","timestamp":"2026-10-06T01:02:03.000Z","type":"sdk_result_error","severity":"error","summary":"PRIVATE_CHAT","sessionId":"s-1","details":{"errorCode":"CLI_EXITED","status":"failed","sdkMessages":[{"result":"PRIVATE_RESULT"}],"error":{"name":"TypeError","message":"PRIVATE_ERROR"}}}` + "\n{broken\n"
	files := map[string]string{
		"cc-haha/diagnostics/diagnostics.jsonl":     raw,
		"cc-haha/diagnostics/cli-diagnostics.jsonl": "token=fake-secret cli event\n",
		"cc-haha/diagnostics/electron-host.log":     "Bearer fake-secret lifecycle\n",
		"cc-haha/providers.json":                    `{"activeId":"provider-1","providers":[{"id":"provider-1","name":"_foo_ provider","presetId":"custom","apiFormat":"anthropic","baseUrl":"https://user:pass@api.example.com/private?token=x","models":{"main":"flash","haiku":"fast","sonnet":"big","opus":"large"},"apiKey":"NEVER_SHARE"}]}`,
	}
	for path, value := range files {
		seedFile(t, filepath.Join(s.options.ConfigDir, filepath.FromSlash(path)), []byte(value))
	}
	bundle, err := s.ExportBundle()
	if err != nil {
		t.Fatal(err)
	}
	got := tarFiles(t, bundle.Path)
	source := sourceDiagnostics(t, map[string]any{"now": iso(testNow), "files": files, "cases": []any{map[string]any{"action": "export"}}})
	var want map[string]string
	if err := json.Unmarshal(source[0], &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("archive entries Go %v Source %v", got, want)
	}
	for name, content := range got {
		if name == "app-info.json" {
			continue
		} // Native runtime metadata is deliberately Go, never invented Bun/Node versions.
		if strings.HasSuffix(name, ".json") || name == "diagnostics.jsonl" {
			if !reflect.DeepEqual(normalized(json.RawMessage(content)), normalized(json.RawMessage(want[name]))) {
				t.Fatalf("archive %s\nGo %s\nSource %s", name, content, want[name])
			}
		} else if content != want[name] {
			t.Fatalf("archive %s\nGo %s\nSource %s", name, content, want[name])
		}
	}
}
