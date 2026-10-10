package sessionprojection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func testService(t *testing.T, raw, platform string) (*Service, string, *Options) {
	t.Helper()
	home := t.TempDir()
	file := filepath.Join(home, "session.jsonl")
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	options := Options{Reader: sessionmetadata.NewReader(nil), Budget: sessionstore.NewBudget(), ConfigDir: func() string { return home }, Cwd: func() string { return home }, Platform: platform, ParseDate: ParseDateUTC, Stat: func(path string) (Stat, error) {
		handle, err := (sessionstore.OSFileSystem{}).Open(path)
		if err != nil {
			return Stat{}, err
		}
		info, err := handle.Stat()
		defer handle.Close()
		if err != nil {
			return Stat{}, err
		}
		// The projection's injected birthtime dependency is tested separately
		// from native stat identity. The TS/Go oracle supplies actual birthtime.
		return Stat{SourceVersion: fmt.Sprintf("%s:%s:%d:%s", info.Dev, info.Ino, info.Size, info.MtimeNS), BirthtimeISO: "2000-01-01T00:00:00.000Z", MtimeISO: "2001-01-01T00:00:00.000Z"}, nil
	}}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return service, file, &options
}
func decoded(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	compatible, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(compatible, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestRequiredDependenciesAndNativeStatFailures(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	_, file, options := testService(t, "", "linux")
	options.Platform = "other"
	if _, err := New(*options); err == nil {
		t.Fatal("unsupported platform accepted")
	}
	options.Platform = "linux"
	fault := errors.New("injected native stat failure")
	options.Stat = func(string) (Stat, error) { return Stat{}, fault }
	service, err := New(*options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(file, "-project"); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	for _, paths := range [][2]string{{file, string([]byte{0xff})}, {string([]byte{0xff}), "-project"}} {
		if _, err := service.Get(paths[0], paths[1]); !errors.Is(err, ErrUnsupportedPath) {
			t.Fatal("unrepresentable path reached native stat", err)
		}
	}
}
func TestCompleteAndEOFProjectionPriorityAndRuntime(t *testing.T) {
	raw := `{"type":"session-meta","workDir":"","runtimeProviderId":"old","runtimeModelId":"old","effortLevel":"high","permissionMode":"plan"}` + "\n" +
		`{"type":"user","timestamp":"2025-01-02T00:00:00Z","cwd":"C:","message":{"role":"user","content":"first"}}` + "\n" +
		`{"type":"user","isMeta":true,"timestamp":"2028-01-01T00:00:00Z","message":{"role":"user","content":"hidden"}}` + "\n" +
		`{"type":"system","message":{"role":"system"},"entrypoint":"claude-desktop-team-worker"}` + "\n" +
		`{"type":"session-meta","runtimeProviderId":null,"runtimeModelId":"new","permissionMode":"invalid"}` + "\n" +
		`{"type":"custom-title","customTitle":"complete"}` + "\n" + `{"type":"custom-title","customTitle":"EOF"}`
	service, file, _ := testService(t, raw, "win32")
	first, err := service.Get(file, "-project")
	if err != nil {
		t.Fatal(err)
	}
	summary, launch := decoded(t, first.Summary), decoded(t, first.LaunchInfo)
	if summary["title"] != "complete" || summary["messageCount"] != float64(2) || summary["workDir"] != `C:\` || summary["modifiedAt"] != "2025-01-02T00:00:00Z" || summary["isTeamWorker"] != true {
		t.Fatal(summary)
	}
	if launch["customTitle"] != "EOF" || launch["transcriptMessageCount"] != float64(2) || launch["workDir"] != filepath.Dir(file) || launch["permissionMode"] != "plan" || launch["runtimeProviderId"] != nil || launch["runtimeModelId"] != "new" {
		t.Fatal(launch)
	}
	if _, exists := launch["effortLevel"]; exists {
		t.Fatal(launch)
	}
	cached, err := service.Get(file, "different-project")
	if err != nil || cached.Signature == "" || first.Signature != "" || !bytes.Equal(cached.LaunchInfo, first.LaunchInfo) {
		t.Fatal(cached, err)
	}
}
func TestTitlesPreserveUTF16XMLAndSourceCoercion(t *testing.T) {
	rows := []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"image"},{"type":"text","text":"<note>hidden</note> hi &amp; raw"}]}}`,
		`{"type":"system","subtype":"local_command","content":"<command-name>/goal</command-name><command-args>clear now</command-args>"}`,
		`{"type":"system","subtype":"local_command","content":"<COMMAND-NAME>/goal</COMMAND-NAME><command-args>first &lt;goal&gt;</command-args>"}`,
		`{"type":"ai-title","aiTitle":[1,null,"future"]}`,
		`{"type":"custom-title","customTitle":"title"}`, `{"type":"custom-title","customTitle":""}`,
	}
	service, file, _ := testService(t, strings.Join(rows, "\n")+"\n", "linux")
	result, err := service.Get(file, "-project")
	if err != nil || decoded(t, result.Summary)["title"] != "/goal first <goal>" || string(result.CustomTitle) != `"title"` {
		t.Fatal(result, err)
	}
	units := append(utf16.Encode([]rune(strings.Repeat("x", 79))), 0xd83d, 0xde00, 'z')
	shortened := shorten(units)
	got, err := protocol.DecodeJavaScriptJSONString(shortened)
	if err != nil || len(got) != 83 || got[79] != 0xd83d || got[80] != '.' {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"<x a='1'>hidden</x>keep", "<X>keep</X>", "<x>no close", "<command-name a='1'>/ask</command-name><command-args>x &amp; y</command-args>", "<command-name-long>drop</command-name-long>keep", "<x>first</x>\n<x>second</x>z"} {
		if len(cleanTitle(utf16.Encode([]rune(raw)))) == 0 {
			t.Fatal(raw)
		}
	}
	if _, err := userTitle(json.RawMessage(`[null]`)); err == nil {
		t.Fatal("null content block silently accepted")
	}
	if value, err := userTitle(json.RawMessage(`[1,{"type":"text","text":false}]`)); err != nil || !isNull(value) {
		t.Fatal(value, err)
	}
}
func TestUnknownValuesNullClearingAndSerializedBudget(t *testing.T) {
	service, file, _ := testService(t, `{"repository":{"2":2,"1":1,"n":1e400,"future":"\ud800"}}`+"\n"+`{"type":"worktree-state","worktreeSession":{"worktreePath":"","worktreeName":"","future":true}}`+"\n"+`{"type":"worktree-state","worktreeSession":null}`+"\n", "linux")
	result, err := service.Get(file, "")
	if err != nil {
		t.Fatal(err)
	}
	launch := decoded(t, result.LaunchInfo)
	if launch["worktreeSession"] != nil || decoded(t, result.Summary)["workDir"] != "" {
		t.Fatal(launch)
	}
	for _, body := range []string{
		`{"type":"session-meta","workDir":"` + strings.Repeat("x", 128*1024) + `"}` + "\n",
		`{"type":"user","message":{"role":"user","content":[null]}}` + "\n",
		`{"type":"assistant","timestamp":"12/31/2025","message":{"role":"assistant"}}` + "\n",
	} {
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Get(file, ""); err == nil {
			t.Fatal("expected failure")
		}
		if err := os.WriteFile(file, []byte(`{"type":"custom-title","customTitle":"retry"}`+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := service.Get(file, "")
		if err != nil || decoded(t, result.Summary)["title"] != "retry" {
			t.Fatal(result, err)
		}
	}
}
func TestPersistedLaunchMemoryOverridesExceptActualCount(t *testing.T) {
	service, file, _ := testService(t, `{"type":"user","message":{"role":"user","content":"body"}}`+"\n", "linux")
	value, err := service.PersistedLaunchInfo(file, "-project", json.RawMessage(`{"workDir":"private","customTitle":"memory","transcriptMessageCount":999,"\ud800":"key","future":{"n":1e400}}`))
	if err != nil {
		t.Fatal(err)
	}
	launch := decoded(t, value)
	if launch["workDir"] != "private" || launch["customTitle"] != "memory" || launch["transcriptMessageCount"] != float64(1) || !bytes.Contains(value, []byte(`\ud800`)) {
		t.Fatal(string(value))
	}
	if _, err := service.PersistedLaunchInfo(file, "-project", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PersistedLaunchInfo(file, "-project", json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed memory accepted")
	}
	if _, err := service.PersistedLaunchInfo("missing", "-project", nil); err == nil {
		t.Fatal("missing stat accepted")
	}
}
func TestCacheScopeLRUEvictionAndRealAppendRewrite(t *testing.T) {
	service, file, options := testService(t, "", "linux")
	scope := "first"
	options.ConfigDir = func() string { return scope }
	service, _ = New(*options)
	if _, err := service.Get(file, "-project"); err != nil {
		t.Fatal(err)
	}
	scope = "other"
	if result, err := service.Get(file, "-project"); err != nil || result.Signature != "" {
		t.Fatal(result, err)
	}
	scope = "first"
	if result, err := service.Get(file, "-project"); err != nil || result.Signature == "" {
		t.Fatal(result, err)
	}
	for index := 0; index < 33; index++ {
		path := filepath.Join(filepath.Dir(file), fmt.Sprintf("%d.jsonl", index))
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Get(path, "-project"); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := service.Get(file, "-project"); err != nil || result.Signature != "" {
		t.Fatal(result, err)
	}
	for _, value := range []string{`{"type":"custom-title","customTitle":"append"}` + "\n", `{"type":"custom-title","customTitle":"rewrite"}` + "\n"} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := service.Get(file, "-project")
		if err != nil || !truthy(result.CustomTitle) {
			t.Fatal(result, err)
		}
	}
}

type heldFS struct {
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
	opens   atomic.Int32
}
type heldFile struct {
	sessionstore.File
	owner *heldFS
}

func (fs *heldFS) Open(path string) (sessionstore.File, error) {
	fs.opens.Add(1)
	file, err := (sessionstore.OSFileSystem{}).Open(path)
	if err != nil {
		return nil, err
	}
	return &heldFile{file, fs}, nil
}
func (file *heldFile) ReadAt(buffer []byte, offset int64) (int, error) {
	file.owner.once.Do(func() { close(file.owner.entered) })
	<-file.owner.gate
	return file.File.ReadAt(buffer, offset)
}
func TestConcurrentInflightCallsShareReader(t *testing.T) {
	_, file, options := testService(t, "{}\n", "linux")
	files := &heldFS{gate: make(chan struct{}), entered: make(chan struct{})}
	options.Reader = sessionmetadata.NewReader(files)
	service, _ := New(*options)
	type output struct {
		value Result
		err   error
	}
	results := make(chan output, 2)
	go func() { value, err := service.Get(file, "first"); results <- output{value, err} }()
	select {
	case <-files.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scan did not start")
	}
	go func() { value, err := service.Get(file, "second"); results <- output{value, err} }()
	select {
	case result := <-results:
		t.Fatal("subscriber completed before scan", result)
	case <-time.After(20 * time.Millisecond):
	}
	if files.opens.Load() != 1 {
		t.Fatal("inflight read duplicated", files.opens.Load())
	}
	close(files.gate)
	for index := 0; index < 2; index++ {
		select {
		case result := <-results:
			if result.err != nil || result.value.Signature != "" {
				t.Fatal(result)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("subscriber leaked")
		}
	}
}
func TestDateParserDeclaredProfilesAndExplicitUnsupportedForms(t *testing.T) {
	for _, value := range []string{"2025", "2025-02", "2025-02-03", "2025-02-29T01:00:00.123999Z", "2025-01-01T24:00:00Z", "2025-01-01T00:00-02:30", "2025-01-01T00:00:00+0230", "+010000-01-01T00:00:00Z", "Wed, 31 Dec 2025 23:00:00 GMT", "Thu, 01 Jan 2026 00:30:00 +0100", "01 Jan 26 00:30 GMT", "01 Jan 26 00:30 UTC", "01 Jan 26 00:30 +0100", "Thu, 1 Jan 2026 00:30:00 UTC"} {
		if _, valid, err := ParseDateUTC(utf16.Encode([]rune(value))); !valid || err != nil {
			t.Fatal(value, valid, err)
		}
	}
	for _, value := range []string{"", "-000000-01-01", "2025-13-01T00:00:00Z", "2025-01-00", "2025-01-32", "2025-01-01T25:00:00Z", "2025-01-01T24:00:01Z", "2025-01-01T00:60:00Z", "2025-01-01T00:00:60Z", "2025-01-01T00:00:00+24:00", "2025-01-01T00:00:00-02:60", "2025T00:00:00Z", "+275760-09-14T00:00:00Z", "-271821-04-19T00:00:00Z"} {
		if _, valid, err := ParseDateUTC(utf16.Encode([]rune(value))); valid || err != nil {
			t.Fatal(value, valid, err)
		}
	}
	for _, value := range []string{"12/31/2025", "2025-01-01T00:00:00", "not-a-date"} {
		if _, _, err := ParseDateUTC(utf16.Encode([]rune(value))); !errors.Is(err, ErrUnsupportedDate) {
			t.Fatal(value, err)
		}
	}
}
