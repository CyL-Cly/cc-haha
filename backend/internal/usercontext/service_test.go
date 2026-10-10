package usercontext

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func nativeOptions(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	state, err := clisettings.NewState(func() float64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1 }, PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	if err != nil {
		t.Fatal(err)
	}
	return Options{Environment: env, Settings: settings, SettingsState: state, Profile: ExternalProfile, Now: func() time.Time { return time.Date(2026, 10, 1, 23, 59, 59, 0, time.UTC) }}
}
func TestRequiredOriginalCapabilities(t *testing.T) {
	for _, name := range []string{"env", "settings", "state", "clock", "profile"} {
		t.Run(name, func(t *testing.T) {
			options := nativeOptions(t)
			switch name {
			case "env":
				options.Environment = nil
			case "settings":
				options.Settings = nil
			case "state":
				options.SettingsState = nil
			case "clock":
				options.Now = nil
			case "profile":
				options.Profile = "unresolved"
			}
			if _, err := New(options); err == nil {
				t.Fatal("unresolved original context admitted")
			}
		})
	}
}
func TestCachedReferenceMutationAndIndependentSessionMemo(t *testing.T) {
	options := nativeOptions(t)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	first := service.GetUserContext()
	value, err := first.Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value.Set("future", quote("same object"))
	if service.GetUserContext() != first {
		t.Fatal("promise identity changed")
	}
	same, _ := service.GetUserContext().Await(context.Background())
	if same != value || same.Get("future") == nil {
		t.Fatal("cached object cloned")
	}
	session := service.GetSessionStartDate()
	_ = options.Environment.SetProperty(quote("CLAUDE_CODE_OVERRIDE_DATE"), quote("new-date"))
	service.ClearUserContext()
	next, _ := service.GetUserContext().Await(context.Background())
	if next == value || !strings.Contains(string(next.Get("currentDate")), "new-date") || string(service.GetSessionStartDate()) != string(session) {
		t.Fatal("independent memo lost")
	}
	service.ClearSessionStartDate()
	if string(service.GetSessionStartDate()) != `"new-date"` {
		t.Fatal("session date clear ignored")
	}
	for _, value := range []json.RawMessage{nil, []byte("null"), []byte(`"\ud800"`)} {
		if err := service.SetSystemPromptInjection(value); err != nil {
			t.Fatal(err)
		}
		if string(service.SystemPromptInjection()) != string(value) {
			t.Fatal("injection identity lost")
		}
	}
	if err := service.SetSystemPromptInjection([]byte("true")); err == nil {
		t.Fatal("non-string injection admitted")
	}
}
func TestMemoryCapabilityRejectsWithoutFakeEmptyContext(t *testing.T) {
	options := nativeOptions(t)
	_ = options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), quote("0"))
	service, _ := New(options)
	future := service.GetUserContext()
	value, err := future.Await(context.Background())
	var capability *CapabilityError
	if value != nil || !errors.As(err, &capability) || future.Pending() || service.GetUserContext() != future {
		t.Fatal("unsupported producer returned empty or recreated failure")
	}
	if _, err = future.Await(nil); err == nil {
		t.Fatal("nil wait context accepted")
	}
}
func TestRawEnvironmentDisableConditions(t *testing.T) {
	for _, value := range []json.RawMessage{[]byte("true"), quote(" TRUE \ufeff"), quote("yes"), quote("on")} {
		t.Run(string(value), func(t *testing.T) {
			options := nativeOptions(t)
			_ = options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), quote("0"))
			_ = options.Environment.SetProperty(quote("CLAUDE_CODE_DISABLE_CLAUDE_MDS"), value)
			service, _ := New(options)
			if _, err := service.GetUserContext().Await(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
	options := nativeOptions(t)
	_ = options.Environment.SetProperty(quote("CLAUDE_CODE_DISABLE_CLAUDE_MDS"), []byte("123"))
	service, _ := New(options)
	if _, err := service.GetUserContext().Await(context.Background()); err == nil {
		t.Fatal("invalid isEnvTruthy capability admitted")
	}
	options = nativeOptions(t)
	options.BareArgument = true
	_ = options.Environment.SetProperty(quote("CLAUDE_CODE_SIMPLE"), []byte("123"))
	service, _ = New(options)
	if _, err := service.GetUserContext().Await(context.Background()); err != nil {
		t.Fatal("--bare short circuit ignored")
	}
}
func TestUnimplementedSettingsFailurePrecedesMemoProducer(t *testing.T) {
	options := nativeOptions(t)
	path, _ := options.Settings.Path("userSettings", "")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(`{"fileSuggestion":{"type":"command","command":"no-op"}}`), 0600)
	service, _ := New(options)
	future := service.GetUserContext()
	_, err := future.Await(context.Background())
	var capability *clisettings.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal(err)
	}
	_, cached := service.CachedMode()
	if cached != nil {
		t.Fatal("resolver failure cached as user promise")
	}
}

func TestStatusLineSettingsDoNotBlockContextOrExecuteCommands(t *testing.T) {
	options := nativeOptions(t)
	path, _ := options.Settings.Path("userSettings", "")
	marker := filepath.Join(t.TempDir(), "must-not-run")
	command, _ := json.Marshal("/usr/bin/touch " + marker)
	content := `{"statusLine":{"type":"command","command":` + string(command) + `,"padding":2}}`
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetUserContext().Await(context.Background()); err != nil {
		t.Fatalf("statusLine setting blocked user context: %v", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("reading statusLine executed its command: %v", err)
	}
	settings, err := options.Settings.GetEditableSource("userSettings", "")
	if err != nil || settings == nil || string(settings.Get("statusLine")) != `{"type":"command","command":"/usr/bin/touch `+marker+`","padding":2}` {
		t.Fatalf("statusLine was not retained in user settings: %v %v", settings, err)
	}
}

func TestEmptyPolicyCacheRetainsReferenceButActivePolicyRejects(t *testing.T) {
	options := nativeOptions(t)
	empty := protocol.NewJSONObject()
	options.SettingsState.SetCachedSettingsForSource("policySettings", &empty)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetUserContext().Await(context.Background()); err != nil {
		t.Fatal("actual empty parsed policy rejected", err)
	}
	value, _ := options.SettingsState.GetCachedSettingsForSource("policySettings")
	if value != &empty {
		t.Fatal("policy producer cache reference replaced")
	}
	empty.Set("unknownPolicy", quote("not absent"))
	if _, err = service.GetUserContext().Await(context.Background()); err == nil {
		t.Fatal("nonempty policy crossed absent-policy capability")
	}
}
func TestFutureCancellationDoesNotCancelSharedNativeRead(t *testing.T) {
	options := nativeOptions(t)
	path, _ := options.Settings.Path("userSettings", "")
	config := filepath.Dir(path)
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(path, []byte(`{"pluginConfigs":{"agents-md@builtin":{"options":{"instructionFiles":"claude-md"}}}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, "CLAUDE.md"), []byte("Actual private native instructions.\n"), 0600)
	var mu sync.Mutex
	clock := options.Now()
	options.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	barrier := &memoryBarrier{clock: options.Now, settled: make(chan struct{}), release: make(chan struct{})}
	options.AdditionalDirectories = []string{t.TempDir()}
	options.Memory = &ExplicitUserMemory{ConfigHome: config, Profile: ExplicitUserMemoryProfile, Files: barrier}
	service, _ := New(options)
	session := service.GetSessionStartDate()
	future := service.GetUserContext()
	<-barrier.settled
	if !future.Pending() || service.GetUserContext() != future {
		t.Fatal("pending promise not shared")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := future.Await(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	service.ClearUserContext()
	_, cached := service.CachedMode()
	if cached != nil {
		t.Fatal("clear did not remove lookup slot")
	}
	mu.Lock()
	clock = clock.Add(2 * time.Second)
	mu.Unlock()
	close(barrier.release)
	value, err := future.Await(context.Background())
	if err != nil || !strings.Contains(string(value.Get("currentDate")), "2026-10-02") {
		t.Fatal(value, err)
	}
	if string(service.GetSessionStartDate()) != string(session) || string(service.CachedClaudeMd()) == "null" {
		t.Fatal("late producer/session memo incorrectly invalidated")
	}
	options = nativeOptions(t)
	options.AdditionalDirectories = []string{t.TempDir()}
	options.Memory = &ExplicitUserMemory{}
	service, _ = New(options)
	if _, err := service.GetUserContext().Await(context.Background()); err == nil {
		t.Fatal("memory producer capability failure swallowed")
	}
}

// memoryFault injects only an original native read/stat capability.
type memoryFault struct {
	content          []byte
	readErr, statErr error
}

func (f memoryFault) ReadFile(string) ([]byte, error)  { return f.content, f.readErr }
func (f memoryFault) Stat(string) (fs.FileInfo, error) { return nil, f.statErr }
func TestExplicitMemoryCapabilityBoundaries(t *testing.T) {
	for _, row := range []struct {
		name, profile, mode, content string
		statErr, readErr             error
		files                        bool
		additional                   bool
		wantError                    bool
	}{{"profile", "unknown", "claude-md", "ok", fs.ErrNotExist, nil, true, true, true}, {"files", ExplicitUserMemoryProfile, "claude-md", "ok", fs.ErrNotExist, nil, false, true, true}, {"mode", ExplicitUserMemoryProfile, "managed-only", "ok", fs.ErrNotExist, nil, true, true, true}, {"directories", ExplicitUserMemoryProfile, "claude-md", "ok", fs.ErrNotExist, nil, true, false, true}, {"rules", ExplicitUserMemoryProfile, "claude-md", "ok", nil, nil, true, true, true}, {"rules-read-error", ExplicitUserMemoryProfile, "claude-md", "ok", fs.ErrPermission, nil, true, true, true}, {"missing", ExplicitUserMemoryProfile, "claude-md", "", fs.ErrNotExist, fs.ErrNotExist, true, true, false}, {"read-error", ExplicitUserMemoryProfile, "claude-md", "", fs.ErrNotExist, fs.ErrPermission, true, true, true}, {"bad-utf8", ExplicitUserMemoryProfile, "claude-md", "\xff", fs.ErrNotExist, nil, true, true, true}, {"include", ExplicitUserMemoryProfile, "claude-md", "@secret", fs.ErrNotExist, nil, true, true, true}, {"comment", ExplicitUserMemoryProfile, "claude-md", "<!--secret-->", fs.ErrNotExist, nil, true, true, true}, {"frontmatter", ExplicitUserMemoryProfile, "claude-md", "---\na: b", fs.ErrNotExist, nil, true, true, true}, {"NUL", ExplicitUserMemoryProfile, "claude-md", "\x00", fs.ErrNotExist, nil, true, true, true}, {"empty", ExplicitUserMemoryProfile, "claude-md", " \ufeff\n", fs.ErrNotExist, nil, true, true, false}, {"plain", ExplicitUserMemoryProfile, "claude-md", " real bytes\r\n", fs.ErrNotExist, nil, true, true, false}} {
		t.Run(row.name, func(t *testing.T) {
			memory := ExplicitUserMemory{ConfigHome: t.TempDir(), Profile: row.profile}
			if row.files {
				memory.Files = memoryFault{content: []byte(row.content), statErr: row.statErr, readErr: row.readErr}
			}
			var additional []string
			if row.additional {
				additional = []string{t.TempDir()}
			}
			_, err := memory.load(row.mode, additional)
			if (err != nil) != row.wantError {
				t.Fatal(err)
			}
		})
	}
}
func TestRenderPrefixPreservesRawKeysAndExactConversionFailure(t *testing.T) {
	empty := protocol.NewJSONObject()
	if raw, err := RenderPrefix(empty); raw != nil || err != nil {
		t.Fatal(raw, err)
	}
	_ = empty.SetPropertyJSON([]byte(`"\ud800"`), []byte(`"\udfff"`))
	prefix, err := RenderPrefix(empty)
	if err != nil || !strings.Contains(string(prefix), `\ud800`) {
		t.Fatal(string(prefix), err)
	}
	empty.Set("bad", []byte(`{"toString":null}`))
	if _, err := RenderPrefix(empty); err == nil {
		t.Fatal("ordinary coercion failure swallowed")
	}
}
