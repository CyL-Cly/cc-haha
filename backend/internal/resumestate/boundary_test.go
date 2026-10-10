package resumestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func TestRequiredHostDependenciesAndUnsupportedInputsStopExplicitly(t *testing.T) {
	for _, mutate := range []func(*Options){func(o *Options) { o.Now = nil }, func(o *Options) { o.PolicyPaths = nil }, func(o *Options) { o.SettingsState = nil }, func(o *Options) { o.OriginalCWD = "/" + strings.Repeat("x", 201) }} {
		home := t.TempDir()
		options, events := testOptions(t, home, OSFiles{})
		mutate(&options)
		if service, err := New(options); service != nil || err == nil {
			t.Fatal("incomplete host silently proceeded")
		}
		if len(events.phases) != 0 || len(events.allocations) != 0 {
			t.Fatal("incomplete host ran effects")
		}
	}
	for _, raw := range []string{`3`, `null`, `{}`, `{"messages":null}`} {
		home := t.TempDir()
		options, events := testOptions(t, home, OSFiles{})
		service, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Load(Input{Source: []byte(raw)})
		var capability *CapabilityError
		if result != nil || !errors.As(err, &capability) || len(events.errors) != 1 {
			t.Fatal("unsupported original LogOption was silently restored", err)
		}
	}
}
func TestSourceReducerFailureOwnershipAndOriginalBytes(t *testing.T) {
	for _, raw := range []string{`{"messages":[null]}`, `{"messages":[{"type":"user","message":null}]}`, `{"messages":[{"type":"assistant","message":{"content":[null]}}]}`} {
		home := t.TempDir()
		options, events := testOptions(t, home, OSFiles{})
		service, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		original := []byte(raw)
		result, err := service.Load(Input{Source: original})
		var failure *sessionresume.Failure
		if result != nil || !errors.As(err, &failure) || failure.Name != "TypeError" || len(events.errors) == 0 || string(original) != raw {
			t.Fatal("Source-owned failure disappeared or original input changed", err)
		}
		if len(service.State().Skills) != 0 {
			t.Fatal("failed malformed input restored skill state")
		}
	}
}

type faultFiles struct {
	OSFiles
	openError, readError, writeError error
	readPath                         string
	mkdirCalls                       int
	readAtFailure                    int
}

func (f *faultFiles) ReadFile(path string) ([]byte, error) {
	if path == f.readPath && f.readError != nil {
		return nil, f.readError
	}
	return f.OSFiles.ReadFile(path)
}
func (f *faultFiles) WriteFile(path string, value []byte) error {
	if f.writeError != nil {
		return f.writeError
	}
	return f.OSFiles.WriteFile(path, value)
}
func (f *faultFiles) MkdirAll(path string) error { f.mkdirCalls++; return f.OSFiles.MkdirAll(path) }
func (f *faultFiles) Open(path string) (sessionstore.File, error) {
	if f.openError != nil {
		return nil, f.openError
	}
	value, err := f.OSFiles.Open(path)
	if err != nil {
		return nil, err
	}
	return &faultRead{File: value, fail: f.readAtFailure}, nil
}

type faultRead struct {
	sessionstore.File
	calls, fail int
}

func (f *faultRead) ReadAt(value []byte, offset int64) (int, error) {
	f.calls++
	if f.calls == f.fail {
		return 0, fs.ErrPermission
	}
	return f.File.ReadAt(value, offset)
}

func TestPlanMemoizationWriteFaultAndSlugOverwriteKeepRealState(t *testing.T) {
	home := t.TempDir()
	files := &faultFiles{writeError: fs.ErrPermission}
	options, events := testOptions(t, home, files)
	options.EnvironmentKind = "byoc"
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	input := original([]json.RawMessage{encode(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "sessionId", Value: quote(oldSID)}, protocol.JSONField{Name: "slug", Value: quote("first")}, protocol.JSONField{Name: "planContent", Value: quote("protected plan")}, protocol.JSONField{Name: "message", Value: []byte(`{"role":"user","content":"q"}`)}))})
	if result, err := service.Load(input); err != nil || result == nil {
		t.Fatal("Source catches plan write error and continues", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "plans", "first.md")); !errors.Is(err, fs.ErrNotExist) || len(events.errors) != 1 {
		t.Fatal("failed write was fabricated or failure lost")
	}
	files.writeError = nil
	input.Source = []byte(strings.ReplaceAll(string(input.Source), "first", "second"))
	if _, err := service.Load(input); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plans", "second.md"))
	if err != nil || string(data) != "protected plan" || files.mkdirCalls != 1 {
		t.Fatal("memoized directory or real write drifted", err)
	}
	if rows := service.State().PlanSlugs; len(rows) != 1 || text(rows[0].Get("value")) != "second" {
		t.Fatal("same original SID did not overwrite slug in insertion order")
	}
}
func TestOriginalKnownSettingsMergeAndResetValidateBeforeUse(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	options, _ := testOptions(t, home, OSFiles{})
	options.CWD = project
	options.OriginalCWD = project
	writePrivate(t, filepath.Join(home, ".claude", "settings.json"), `{"hooks":{"SessionStart":[{"matcher":"user","hooks":[]}]},"enabledPlugins":{"a":false},"cleanupPeriodDays":30}`)
	writePrivate(t, filepath.Join(project, ".claude", "settings.json"), `{"hooks":{"SessionStart":[{"matcher":"project","hooks":[]}]},"enabledPlugins":{"b":false},"plansDirectory":"plans"}`)
	writePrivate(t, filepath.Join(project, ".claude", "settings.local.json"), `{"enabledPlugins":{"a":true}}`)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	settings, _, err := service.settings()
	if err != nil {
		t.Fatal(err)
	}
	matchers, ok := array(object(settings.Get("hooks")).Get("SessionStart"))
	if !ok || len(matchers) != 2 || object(matchers[0]).String("matcher") != "user" || object(matchers[1]).String("matcher") != "project" || string(object(settings.Get("enabledPlugins")).Get("a")) != "true" || string(object(settings.Get("enabledPlugins")).Get("b")) != "false" {
		t.Fatal("validated original files merged in wrong precedence", string(encode(settings)))
	}
	writePrivate(t, filepath.Join(project, ".claude", "settings.local.json"), `{"hooks":3}`)
	options.SettingsState.ResetSettingsCache()
	if _, _, err := service.settings(); err == nil {
		t.Fatal("reset admitted newly invalid settings")
	}
	invalid := protocol.JSONObject{}
	options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &invalid})
	if _, _, err := service.settings(); err == nil {
		t.Fatal("nonobject cached settings escaped")
	}
}
func TestNativeConfigurationReadFaultsKeepCauseAndNeverRestore(t *testing.T) {
	for _, mode := range []string{"directory", "invalid-json", "permission"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".claude", "settings.json")
			files := &faultFiles{}
			if mode == "directory" {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if mode == "invalid-json" {
				writePrivate(t, path, `{invalid`)
			} else {
				files.readPath = path
				files.readError = fs.ErrPermission
			}
			options, events := testOptions(t, home, files)
			if service, err := New(options); service != nil || err == nil || mode == "permission" && !errors.Is(err, fs.ErrPermission) {
				t.Fatal("configuration failure was swallowed", err)
			}
			if len(events.phases) != 0 || len(events.allocations) != 0 {
				t.Fatal("configuration failure caused restoration")
			}
		})
	}
}
func TestContinueNativeFileFaultsDoNotMutateOriginalFiles(t *testing.T) {
	for _, mode := range []string{"open", "head-read", "tail-read", "stat"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			files := &faultFiles{}
			options, events := testOptions(t, home, files)
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(service.projectDir(), oldSID+".jsonl")
			raw := string(plain("u", "q")) + "\n"
			if mode == "tail-read" {
				raw = string(plain("u", strings.Repeat("x", 150000))) + "\n"
			}
			writePrivate(t, path, raw)
			if mode == "open" {
				files.openError = fs.ErrPermission
			}
			if mode == "head-read" {
				files.readAtFailure = 1
			}
			if mode == "tail-read" {
				files.readAtFailure = 2
			}
			if mode == "stat" {
				service.options.Stat = func(string) (Stat, error) { return Stat{}, fs.ErrPermission }
			}
			result, err := service.Load(Input{})
			if err != nil || mode != "stat" && result == nil || mode == "stat" && result != nil {
				t.Fatal("native lite-read/stat boundary drifted", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != raw {
				t.Fatal("original transcript mutated")
			}
			if mode == "stat" && !strings.Contains(string(encode(events.diagnostics)), "Failed to stat session file") {
				t.Fatal("stat fault lost diagnostic")
			}
		})
	}
}
func TestRawIdentityCapabilitiesAreNotStringCoercion(t *testing.T) {
	for _, row := range []struct {
		left, right string
		equal       bool
	}{{`1`, `1.0`, true}, {`1`, `"1"`, false}, {`true`, `1`, false}, {`null`, ``, false}, {``, ``, true}, {`"\ud800"`, `"\ud801"`, false}, {`"same"`, `true`, false}, {`{}`, `{}`, false}, {`[]`, `[]`, false}, {`false`, `false`, true}} {
		if scalarEqual([]byte(row.left), []byte(row.right)) != row.equal {
			t.Fatal("raw JS identity drifted", row)
		}
	}
	home := t.TempDir()
	options, _ := testOptions(t, home, OSFiles{})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	service.setSlug([]byte(`1`), quote("a"))
	service.setSlug([]byte(`1.0`), quote("b"))
	if len(service.State().PlanSlugs) != 1 || text(service.State().PlanSlugs[0].Get("value")) != "b" {
		t.Fatal("numeric same identity duplicated slug")
	}
	parsed, err := sessionresume.ParseMessages([]json.RawMessage{[]byte(`"nonobject"`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(&Result{Result: sessionresume.Result{Messages: parsed}}, requestcontext.Context{}, requestcontext.Dependencies{}); err == nil {
		t.Fatal("nonobject resumed envelope was accepted for first request")
	}
}
