package clisettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func canonicalObservation(raw json.RawMessage) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '{' {
		var object protocol.JSONObject
		_ = object.UnmarshalJSON(raw)
		fields := object.OwnProperties()
		sort.Slice(fields, func(i, j int) bool { return string(fields[i].NameJSON) < string(fields[j].NameJSON) })
		out := []byte{'{'}
		for i, field := range fields {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, field.NameJSON...)
			out = append(out, ':')
			out = append(out, canonicalObservation(field.Value)...)
		}
		return append(out, '}')
	}
	if raw[0] == '[' {
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		out := []byte{'['}
		for i, value := range values {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, canonicalObservation(value)...)
		}
		return append(out, ']')
	}
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func TestWriterSourceGoldens(t *testing.T) {
	source, err := os.ReadFile("testdata/parser_writer_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    writerProfile
		Expected json.RawMessage
	}
	if err = json.Unmarshal(source, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Input.Name, func(t *testing.T) {
			test.Input.Home = t.TempDir()
			actual, err := observeWriter(test.Input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(actual)
			raw = bytes.ReplaceAll(raw, []byte(test.Input.Home), []byte("<HOME>"))
			if !bytes.Equal(canonicalObservation(raw), canonicalObservation(test.Expected)) {
				t.Fatalf("actual Source mismatch\nGo=%s\nSource=%s", raw, test.Expected)
			}
		})
	}
}
func testService(t *testing.T, fs FileSystem, observe func(Effect)) (*Service, *State, string) {
	t.Helper()
	home := t.TempDir()
	state, _ := NewState(func() float64 { return 1000 })
	service, err := NewService(Options{State: state, ConfigHome: home, OriginalCWD: home, Profile: ExternalReleaseProfile, Now: func() float64 { return 1000 }, PID: 123, FS: fs, Observe: observe})
	if err != nil {
		t.Fatal(err)
	}
	return service, state, filepath.Join(home, ".claude", "settings.json")
}
func object(t *testing.T, raw string) protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	if err := value.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestParserErrorsCloneAndIdentity(t *testing.T) {
	service, state, path := testService(t, nil, nil)
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":null}`), 0640); err != nil {
		t.Fatal(err)
	}
	a, err := service.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := service.ParseFile(path)
	if a.Settings != nil || a.ErrorReference() != b.ErrorReference() {
		t.Fatal("invalid settings/errors identity lost")
	}
	*a.ErrorReference() = append(*a.ErrorReference(), []byte(`{"extra":true}`))
	c, _ := service.ParseFile(path)
	if len(c.Errors) != 2 {
		t.Fatal("shared array append lost")
	}
	(*a.ErrorReference())[0] = []byte(`{"mutated":true}`)
	d, _ := service.ParseFile(path)
	if string(d.Errors[0]) != `{"mutated":true}` {
		t.Fatal("shared indexed mutation lost")
	}
	state.ResetSettingsCache()
	os.WriteFile(path, []byte(`{"model":"opus","future":{"deep":1}}`), 0640)
	e, _ := service.ParseFile(path)
	e.Settings.Set("future", []byte(`{"deep":2}`))
	f, _ := service.ParseFile(path)
	if e.Settings == f.Settings || string(f.Settings.Get("future")) != `{"deep":1}` {
		t.Fatal("validated clone poisoned path cache")
	}
}
func TestKnownCapabilitiesStopBeforeMutation(t *testing.T) {
	for _, key := range activeKeys {
		if autoSettingsKey(quoted(key)) || processedWorkflowKey(quoted(key)) || processedAgentProperty(quoted(key)) || key == "model" || key == "env" || key == "effortLevel" || key == "pluginConfigs" || key == "hooks" || key == "disableAllHooks" || key == "allowManagedHooksOnly" || processedMigrationKey(quoted(key)) {
			continue
		}
		t.Run(key, func(t *testing.T) {
			effects := []Effect{}
			service, state, path := testService(t, nil, func(e Effect) { effects = append(effects, e) })
			patch := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`{}`)})
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: patch})
			var capability *CapabilityError
			if !errors.As(err, &capability) || len(effects) != 0 || state.ConsumeInternalWrite(path, 5000) {
				t.Fatal("unsupported known capability mutated state", err)
			}
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("unsupported key wrote file")
			}
		})
	}
}
func TestConstructorAndSourceBoundaries(t *testing.T) {
	if _, err := NewService(Options{}); err == nil {
		t.Fatal("missing explicit host accepted")
	}
	state, _ := NewState(func() float64 { return 0 })
	home := t.TempDir()
	if _, err := NewService(Options{State: state, ConfigHome: home, OriginalCWD: home, Now: func() float64 { return 0 }, PID: 1, Profile: "internal"}); err == nil {
		t.Fatal("profile widened")
	}
	service, _, _ := testService(t, nil, nil)
	for _, source := range []string{"unknown"} {
		if _, err := service.Path(source, ""); err == nil {
			t.Fatal(source)
		}
	}
	if _, err := service.Path("localSettings", ""); err != nil {
		t.Fatal("actual no-Git local path", err)
	}
	if _, err := service.Path("projectSettings", "relative"); err == nil {
		t.Fatal("unresolved path accepted")
	}
	for _, source := range []string{"policySettings", "flagSettings"} {
		if result, err := service.UpdateEditableSource(source, "", Patch{}); err != nil || result.Error != nil {
			t.Fatal("Source readonly exit", err)
		}
	}
}
func TestSourceCacheAndExplicitOverride(t *testing.T) {
	service, state, path := testService(t, nil, nil)
	os.MkdirAll(filepath.Dir(path), 0777)
	os.WriteFile(path, []byte(`{"model":"opus"}`), 0640)
	a, err := service.GetEditableSource("projectSettings", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := service.GetEditableSource("projectSettings", "")
	if a != b {
		t.Fatal("source cache identity")
	}
	a.Set("future", []byte(`true`))
	c, _ := service.GetEditableSource("projectSettings", service.options.OriginalCWD)
	if c == a || c.Get("future") != nil {
		t.Fatal("override must bypass source cache")
	}
	state.SetCachedSettingsForSource("userSettings", nil)
	u, err := service.GetEditableSource("userSettings", "")
	if err != nil || u != nil {
		t.Fatal("cached null/miss")
	}
	if value, err := service.GetEditableSource("localSettings", ""); err != nil || value != nil {
		t.Fatal("missing no-Git local read", err)
	}
}

type brokenFS struct {
	OSFileSystem
	operation string
}

func (f brokenFS) OpenReadFile(p string) (ReadHandle, error) {
	if f.operation == "read" {
		return nil, errors.New("read fault")
	}
	return f.OSFileSystem.OpenReadFile(p)
}

func (f brokenFS) MkdirAll(p string) error {
	if f.operation == "mkdir" {
		return errors.New("mkdir fault")
	}
	return f.OSFileSystem.MkdirAll(p)
}
func (f brokenFS) ReadFile(p string) ([]byte, error) {
	if f.operation == "read" {
		return nil, errors.New("read fault")
	}
	return f.OSFileSystem.ReadFile(p)
}
func (f brokenFS) Stat(p string) (os.FileInfo, error) {
	if f.operation == "stat" {
		return nil, errors.New("stat fault")
	}
	return f.OSFileSystem.Stat(p)
}
func TestWriteFailuresAndMarks(t *testing.T) {
	for _, fault := range []string{"mkdir", "read", "stat"} {
		t.Run(fault, func(t *testing.T) {
			effects := []Effect{}
			service, state, path := testService(t, brokenFS{operation: fault}, func(e Effect) { effects = append(effects, e) })
			result, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)})
			if err != nil || result.Error == nil || !strings.Contains(result.Error.Error(), fault+" fault") {
				t.Fatal(result, err)
			}
			marked := state.ConsumeInternalWrite(path, 5000)
			if marked != (fault == "stat") {
				t.Fatal("mark order", effects)
			}
			for _, effect := range effects {
				if effect.Kind == "reset" {
					t.Fatal("failed write reset")
				}
			}
		})
	}
}
func TestRawTargetsAndMergeGuards(t *testing.T) {
	for _, raw := range []string{`[]`, `42`, `"x"`} {
		t.Run(raw, func(t *testing.T) {
			service, state, path := testService(t, nil, nil)
			os.MkdirAll(filepath.Dir(path), 0777)
			os.WriteFile(path, []byte(raw), 0640)
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)})
			if err == nil || state.ConsumeInternalWrite(path, 5000) {
				t.Fatal("unported target merged", err)
			}
		})
	}
	for _, raw := range []string{`{"future":{"__proto__":1}}`, `{"future":{"constructor":1}}`, `{"future":[{"prototype":1}]}`} {
		if _, err := ValidateJSONContent(raw, "x"); err == nil {
			t.Fatal("prototype graph accepted")
		}
	}
	target := object(t, `{"x":[]}`)
	source := object(t, `{"x":{"a":1}}`)
	if err := mergeObjects(&target, source); err == nil {
		t.Fatal("array target object merge widened")
	}
	target = object(t, `{"x":1}`)
	if err := mergeObjects(&target, source); err != nil {
		t.Fatal(err)
	}
	target.Set("gone", []byte(`true`))
	source = protocol.NewJSONObject(protocol.JSONField{Name: "gone"})
	if err := mergeObjects(&target, source); err != nil {
		t.Fatal(err)
	}
	if len(target.Get("gone")) != 0 {
		t.Fatal("undefined deletion")
	}
	if err := checkMergeValues(protocol.NewJSONObject(protocol.JSONField{Name: "bad", Value: []byte(`{`)})); err == nil {
		t.Fatal("invalid raw accepted")
	}
}
func TestUndefinedPatchGuards(t *testing.T) {
	for _, parts := range []string{`[]`, `[1]`, `["future","x"]`} {
		var paths []json.RawMessage
		_ = json.Unmarshal([]byte(parts), &paths)
		if err := validateUndefinedPath(object(t, `{"future":1}`), paths); err == nil {
			t.Fatal(parts)
		}
	}
	target := object(t, `{"future":{"x":{"y":1},"keep":true}}`)
	var parts []json.RawMessage
	_ = json.Unmarshal([]byte(`["future","x","y"]`), &parts)
	if err := setUndefinedPath(&target, parts); err != nil {
		t.Fatal(err)
	}
	if string(target.Get("future")) != `{"x":{},"keep":true}` {
		t.Fatal(string(target.Get("future")))
	}
	if err := setUndefinedPath(&target, nil); err == nil {
		t.Fatal("empty")
	}
	if err := setUndefinedPath(&target, []json.RawMessage{[]byte(`1`)}); err == nil {
		t.Fatal("number")
	}
	if err := setUndefinedPath(&target, []json.RawMessage{[]byte(`"no"`), []byte(`"x"`)}); err == nil {
		t.Fatal("parent")
	}
}

func TestConsumerCapabilityGuards(t *testing.T) {
	service, state, path := testService(t, nil, nil)
	os.MkdirAll(filepath.Dir(path), 0777)
	os.WriteFile(path, []byte(`{"permissions":{"allow":[]}}`), 0640)
	for _, call := range []func() error{
		func() error { _, err := service.ParseFile(path); return err },
		func() error { _, err := service.GetEditableSource("projectSettings", ""); return err },
		func() error {
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)})
			return err
		},
		func() error { _, err := service.UpdateEditableSource("localSettings", "", Patch{}); return err },
		func() error { _, err := service.UpdateEditableSource("projectSettings", "", Patch{}); return err },
		func() error {
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"future":{"constructor":1}}`)})
			return err
		},
		func() error {
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: []byte(`{`)})})
			return err
		},
		func() error {
			_, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"future":1}`), UndefinedPaths: [][]json.RawMessage{{[]byte(`"future"`), []byte(`"x"`)}}})
			return err
		},
	} {
		err := call()
		var capability *CapabilityError
		if !errors.As(err, &capability) || !strings.HasPrefix(err.Error(), "unsupported CLI settings capability:") {
			t.Fatal("capability projection", err)
		}
		if state.ConsumeInternalWrite(path, 5000) {
			t.Fatal("capability marked")
		}
	}
	record := &ParsedSettings{Errors: []json.RawMessage{[]byte(`{"first":true}`)}}
	if record.ErrorReference() != record.ErrorReference() {
		t.Fatal("legacy record stable ref")
	}
}

type specialFS struct{ OSFileSystem }
type specialInfo struct{}

func (specialInfo) Name() string                    { return "pipe" }
func (specialInfo) Size() int64                     { return 0 }
func (specialInfo) Mode() os.FileMode               { return os.ModeNamedPipe }
func (specialInfo) ModTime() time.Time              { return time.Time{} }
func (specialInfo) IsDir() bool                     { return false }
func (specialInfo) Sys() any                        { return nil }
func (specialFS) Lstat(string) (os.FileInfo, error) { return specialInfo{}, nil }
func (specialFS) ReadFile(string) ([]byte, error)   { panic("special file must not be read") }
func TestNetworkAndSpecialSettingsGuards(t *testing.T) {
	service, state, path := testService(t, specialFS{}, nil)
	for _, target := range []string{"//never-contact-host/settings", `\\never-contact-host\settings`, path} {
		if _, err := service.ParseFile(target); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
	// A previous null parse must not conceal an unsupported new file type in
	// raw fallback. No native FIFO read occurs and no mark may be consumed.
	state.SetCachedParsedFile(path, &ParsedSettings{Errors: []json.RawMessage{}})
	if _, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)}); err == nil || state.ConsumeInternalWrite(path, 5000) {
		t.Fatal("special fallback guard", err)
	}
	if err := (OSFileSystem{}).WriteFlush(filepath.Join(t.TempDir(), "absent-parent", "file"), []byte("x")); !os.IsNotExist(err) {
		t.Fatal("native open error lost", err)
	}
}
func TestNestedUnsupportedMergeCannotMark(t *testing.T) {
	service, state, path := testService(t, nil, nil)
	settings := object(t, `{"future":{"nested":[]}}`)
	state.SetCachedParsedFile(path, &ParsedSettings{Settings: &settings, Errors: []json.RawMessage{}})
	if _, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"future":{"nested":{"a":1}}}`)}); err == nil || state.ConsumeInternalWrite(path, 5000) {
		t.Fatal("nested unsupported merge marked", err)
	}
	settings = object(t, `{"future":{"prototype":1}}`)
	state.SetCachedParsedFile(path, &ParsedSettings{Settings: &settings, Errors: []json.RawMessage{}})
	if _, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)}); err == nil || state.ConsumeInternalWrite(path, 5000) {
		t.Fatal("cache did not retain capability boundary", err)
	}
	target := object(t, `{"future":{}}`)
	if err := setUndefinedPath(&target, []json.RawMessage{[]byte(`"future"`), []byte(`1`)}); err == nil {
		t.Fatal("nested key not validated")
	}
}

type changedReadFS struct {
	OSFileSystem
	calls       *int
	replacement []byte
}

func (f changedReadFS) ReadFile(path string) ([]byte, error) {
	raw, err := f.OSFileSystem.ReadFile(path)
	*f.calls++
	if *f.calls == 1 {
		_ = os.WriteFile(path, f.replacement, 0640)
	}
	return raw, err
}
func TestUnportedFreshRawFallbackStops(t *testing.T) {
	count := 0
	service, state, path := testService(t, changedReadFS{calls: &count, replacement: []byte(`{"permissions":{"allow":[]}}`)}, nil)
	os.MkdirAll(filepath.Dir(path), 0777)
	os.WriteFile(path, []byte(`{"env":123}`), 0640)
	if _, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: object(t, `{"model":"opus"}`)}); err == nil || state.ConsumeInternalWrite(path, 5000) {
		t.Fatal("fresh fallback unsupported known field accepted", err)
	}
	if count != 2 {
		t.Fatal("fallback must actually reread", count)
	}
}
func TestSharedStateCannotWidenParserProfile(t *testing.T) {
	service, state, path := testService(t, nil, nil)
	for _, raw := range []string{`{"permissions":{"allow":[]}}`, `{"future":{"prototype":true}}`} {
		record := object(t, raw)
		state.SetCachedParsedFile(path, &ParsedSettings{Settings: &record, Errors: []json.RawMessage{}})
		if _, err := service.ParseFile(path); err == nil {
			t.Fatal("external path cache widened profile")
		}
		state.SetCachedSettingsForSource("projectSettings", &record)
		if _, err := service.GetEditableSource("projectSettings", ""); err == nil {
			t.Fatal("external source cache widened profile")
		}
	}
}
func TestUndefinedQuotedPathIdentity(t *testing.T) {
	service, _, path := testService(t, nil, nil)
	os.MkdirAll(filepath.Dir(path), 0777)
	os.WriteFile(path, []byte(`{"future":{"REMOVE":true,"keep":1}}`), 0640)
	patch := object(t, `{"future":{"REMOVE":null}}`)
	result, err := service.UpdateEditableSource("projectSettings", "", Patch{Object: patch, UndefinedPaths: [][]json.RawMessage{{[]byte(`"\u0066uture"`), []byte(`"\u0052EMOVE"`)}}})
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "REMOVE") || !strings.Contains(string(raw), "keep") {
		t.Fatal(string(raw))
	}
	if propertyValue(patch, []byte("1")) != nil {
		t.Fatal("nonstring lookup")
	}
}
