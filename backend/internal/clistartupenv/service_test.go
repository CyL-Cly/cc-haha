package clistartupenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceContracts(t *testing.T) {
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input    fixtureInput
		Expected json.RawMessage
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 49 {
		t.Fatal("missing actual Source original-input corpus")
	}
	t.Setenv("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH", "1")
	for _, row := range rows {
		t.Run(row.Input.Name, func(t *testing.T) {
			input := row.Input
			input.Home, err = filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			actual, err := executeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(actual)
			raw = []byte(strings.ReplaceAll(string(raw), input.Home, "<HOME>"))
			var got, want any
			_ = json.Unmarshal(raw, &got)
			_ = json.Unmarshal(row.Expected, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("actual Source contract mismatch\ngot %s\nwant %s", raw, row.Expected)
			}
		})
	}
}

func TestSourceCorpusOwnsCredentialInputsRegardlessOfAmbientEmptyValue(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "ambient-fixture-key")
	t.Setenv("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH", "1")
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input    fixtureInput
		Expected json.RawMessage
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Input.Name != "ssh-boot-protects-auth-only-exact-case" {
			continue
		}
		row.Input.Home, err = filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		actual, err := executeFixture(row.Input)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(actual)
		var got, want any
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(raw), row.Input.Home, "<HOME>")), &got)
		_ = json.Unmarshal(row.Expected, &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ambient credential presence contaminated Source fixture: %s", raw)
		}
		if value, present := os.LookupEnv("ANTHROPIC_AUTH_TOKEN"); !present || value != "" || os.Getenv("ANTHROPIC_API_KEY") != "ambient-fixture-key" {
			t.Fatal("fixture changed caller environment")
		}
		return
	}
	t.Fatal("missing credential Source fixture")
}
func newTestService(t *testing.T, globalRaw string, initial protocol.JSONObject, enabled bool) (*Service, *cliglobalconfig.Service) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	_ = os.MkdirAll(config, 0777)
	_ = os.MkdirAll(filepath.Join(project, ".claude"), 0777)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(globalRaw), 0600)
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(946684800000) }, PID: 12345, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }, Stderr: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = global.Close() })
	if enabled {
		if err = global.Enable(); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := clisettings.NewState(func() float64 { return 946684800000 })
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 946684800000 }, PID: 12345, FS: fixtureSettingsFS{home: home, events: &[]Event{}, paths: map[string]bool{}}})
	if err != nil {
		t.Fatal(err)
	}
	initial.Set("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH", quote("1"))
	environment, err := NewEnvironment(initial)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, Host: Host{Profile: EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if err != nil {
		t.Fatal(err)
	}
	return s, global
}
func TestExplicitHostAndValueGuards(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing services accepted")
	}
	if _, err := NewEnvironment(protocol.JSONObject{}); err == nil {
		t.Fatal("missing original object accepted")
	}
	for _, raw := range []string{`{"A":[]}`, `{"A":{}}`} {
		if _, err := NewEnvironment(parseObject(raw)); err == nil {
			t.Fatal("reference value accepted")
		}
	}
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	for _, change := range []func(*Options){func(o *Options) { o.Host.Profile = "" }, func(o *Options) { o.Host.RemoteCacheAbsent = false }, func(o *Options) { o.Host.MDMCacheAbsent = false }, func(o *Options) { o.Host.HKCUCacheAbsent = false }, func(o *Options) { o.Host.PolicyFilesAbsent = false }, func(o *Options) { o.Host.PluginBaseAbsent = false }, func(o *Options) { o.Host.AllowedSources = []string{"not-a-source"} }, func(o *Options) { path := "relative.json"; o.Host.FlagPath = &path }, func(o *Options) { o.Host.FlagInline = []byte("null") }} {
		options := s.options
		change(&options)
		if _, err := New(options); err == nil {
			t.Fatal("unresolved host accepted")
		}
	}
	if err := s.options.Environment.SetProperty([]byte("null"), quote("x")); err == nil {
		t.Fatal("non-string key accepted")
	}
	if err := s.options.Environment.SetProperty(quote("A"), []byte("{bad")); err == nil {
		t.Fatal("invalid raw value accepted")
	}
}
func TestEnvironmentOwnUndefinedAndIdentity(t *testing.T) {
	o := protocol.NewJSONObject()
	_ = o.SetPropertyJSON([]byte(`"\ud800"`), quote("high"))
	_ = o.SetPropertyJSON(quote("�"), quote("replacement"))
	o.Set("MISSING_VALUE", nil)
	e, err := NewEnvironment(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Snapshot().OwnProperties()) != 3 {
		t.Fatal("UTF16 keys collided or undefined disappeared")
	}
	clone := e.Snapshot()
	clone.Set("MISSING_VALUE", quote("changed"))
	if e.Get("MISSING_VALUE") != nil {
		t.Fatal("snapshot mutation escaped")
	}
	partial := protocol.NewJSONObject(protocol.JSONField{Name: "BEFORE", Value: quote("kept")}, protocol.JSONField{Name: "REFERENCE", Value: []byte(`{"x":1}`)}, protocol.JSONField{Name: "AFTER", Value: quote("never")})
	if err = e.Assign(partial); err == nil {
		t.Fatal("reference domain accepted")
	}
	if !equalString(e.Get("BEFORE"), "kept") || e.Get("AFTER") != nil {
		t.Fatal("guard lost ordered partial assignment")
	}
	if !reflect.DeepEqual(describe(nil), map[string]any{"kind": "undefined"}) {
		t.Fatal("undefined projection changed")
	}
	if equalString(quote("a"), "long") || equalString([]byte("true"), "true") || equalString(quote("a"), "b") {
		t.Fatal("nonidentity string accepted")
	}
	if !truthy([]byte("[]")) {
		t.Fatal("JS object truthiness changed")
	}
}
func TestDynamicGuardsPreserveEarlierEffects(t *testing.T) {
	for _, row := range []struct{ name, global, capability string }{
		{"legacy array", `{"env":["not-ported"]}`, "non-record legacy env"},
		{"legacy string", `{"env":"chars"}`, "non-record legacy env"},
		{"reference value", `{"env":{"BEFORE":"kept","BAD":{}}}`, "mutable/reference"},
		{"numeric host predicate", `{"env":{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":1,"BEFORE":"kept"}}`, "isEnvTruthy"},
		{"cowork path", `{"env":{"CLAUDE_CODE_USE_COWORK_PLUGINS":"1","BEFORE":"kept"}}`, "cowork user settings"},
	} {
		t.Run(row.name, func(t *testing.T) {
			s, _ := newTestService(t, row.global, protocol.NewJSONObject(), true)
			err := s.ApplySafeConfigEnvironmentVariables()
			if err == nil || !strings.Contains(err.Error(), row.capability) {
				t.Fatalf("guard: %v", err)
			}
			if strings.Contains(row.global, "BEFORE") && !equalString(s.options.Environment.Get("BEFORE"), "kept") {
				t.Fatal("earlier scalar assignment was lost")
			}
		})
	}
	s, _ := newTestService(t, `{"env":{"CC_HAHA_AGENT_TEAMS_ENABLED":{},"BEFORE":"kept"}}`, protocol.NewJSONObject(), true)
	if err := s.ApplySafeConfigEnvironmentVariables(); err != nil {
		t.Fatal("filtered reference value blocked admitted rest", err)
	}
	if s.options.Environment.Get("CC_HAHA_AGENT_TEAMS_ENABLED") != nil {
		t.Fatal("host-only team key applied")
	}
}
func TestSafeAccessAndCAErrorOwnership(t *testing.T) {
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), false)
	events := []Event{}
	s.options.Observe = func(e Event) { events = append(events, e) }
	if err := s.ApplySafeConfigEnvironmentVariables(); err == nil || err.Error() != "Config accessed before allowed." {
		t.Fatal(err)
	}
	if err := s.ApplyExtraCACertsFromConfig(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0]["kind"] != "debug" || events[0]["level"] != "error" || !strings.Contains(events[0]["message"].(string), "Config accessed before allowed.") {
		t.Fatal(events)
	}
	s2, _ := newTestService(t, `{"env":{"NODE_EXTRA_CA_CERTS":{}}}`, protocol.NewJSONObject(), true)
	if err := s2.ApplyExtraCACertsFromConfig(); err == nil {
		t.Fatal("reference CA accepted")
	}
}
func TestUnsupportedSourcesDoNotBecomeEmpty(t *testing.T) {
	for _, where := range []string{"user", "flag", "inline", "project"} {
		t.Run(where, func(t *testing.T) {
			s, _ := newTestService(t, `{"env":{"BEFORE":"kept"}}`, protocol.NewJSONObject(), true)
			raw := []byte(`{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"CC_HAHA_FORBIDDEN_STARTUP_HOOK"}]}]}}`)
			switch where {
			case "user":
				_ = os.WriteFile(filepath.Join(s.options.ConfigHome, "settings.json"), raw, 0600)
			case "flag":
				path := filepath.Join(s.options.ConfigHome, "flag.json")
				_ = os.WriteFile(path, raw, 0600)
				s.options.Host.FlagPath = &path
			case "inline":
				s.options.Host.FlagInline = raw
			case "project":
				path, _ := s.options.Settings.Path("projectSettings", "")
				_ = os.WriteFile(path, raw, 0600)
			}
			err := s.ApplySafeConfigEnvironmentVariables()
			var capability *clisettings.CapabilityError
			if !errors.As(err, &capability) || !strings.Contains(capability.Error(), "active hook matcher/definition schema") {
				t.Fatal("unported active hook schema must remain a typed capability", err)
			}
			if !equalString(s.options.Environment.Get("BEFORE"), "kept") {
				t.Fatal("global assignment lost")
			}
		})
	}
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	_ = s.options.Environment.SetProperty(quote("CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"), quote("0"))
	if err := s.ApplySafeConfigEnvironmentVariables(); err == nil || !strings.Contains(err.Error(), "remote eligibility") {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.options.ConfigHome, "cc-haha"), 0777)
	_ = os.WriteFile(filepath.Join(s.options.ConfigHome, "cc-haha", "providers.json"), []byte("{}"), 0600)
	if err := s.ApplySafeConfigEnvironmentVariables(); err == nil || !strings.Contains(err.Error(), "providers index") {
		t.Fatal(err)
	}
}
func TestMergeProfileGuardsAndPresence(t *testing.T) {
	target := parseObject(`{"future":[1]}`)
	if err := mergeSettings(&target, parseObject(`{"future":{}}`)); err == nil {
		t.Fatal("array target/object source accepted")
	}
	parent := protocol.NewJSONObject()
	source := protocol.NewJSONObject()
	source.Set("catch", nil)
	if err := mergeSettings(&parent, source); err != nil {
		t.Fatal(err)
	}
	if len(parent.OwnProperties()) != 1 || parent.OwnProperties()[0].Value != nil {
		t.Fatal("own undefined slot lost")
	}
	if !scalarEqual(nil, nil) || scalarEqual(nil, []byte("null")) || scalarEqual([]byte("null"), []byte("false")) || scalarEqual([]byte("{}"), []byte("{}")) || !scalarEqual([]byte("true"), []byte("true")) {
		t.Fatal("scalar/ref equality changed")
	}
	if !bytes.Equal(concat(quote("a"), []byte(`"\ud800"`)), []byte(`"a\ud800"`)) {
		t.Fatal("quoted UTF16 concatenation lost")
	}
	for _, raw := range []string{"true", `" \ufeffYES "`, "0", "false", "null", `"off"`} {
		_, err := envTruthy([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
	}
	var cap *CapabilityError
	if _, err := envTruthy([]byte("1")); !errors.As(err, &cap) {
		t.Fatal("typed predicate guard missing", err)
	}
}

func TestCAAndCcHahaCapabilityBoundaries(t *testing.T) {
	for _, row := range []struct{ name, global, env, cc, user string }{
		{"CA active hook schema", `{}`, `{}`, ``, `{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"CC_HAHA_FORBIDDEN_STARTUP_HOOK"}]}]}}`},
		{"CA cowork", `{}`, `{"CLAUDE_CODE_USE_COWORK_PLUGINS":true}`, ``, ``},
		{"CA global container", `{"env":[]}`, `{}`, ``, ``},
		{"CC reference assignment", `{}`, `{}`, `{"env":{"BEFORE":"kept","BAD":{}}}`, ``},
		{"CC container", `{}`, `{}`, `{"env":[]}`, ``},
		{"CC typed DeepSeek base", `{}`, `{}`, `{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_BASE_URL":true}}`, ``},
		{"CC throwing regex coercion", `{}`, `{}`, `{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":{"toString":1}}}`, ``},
		{"CC late cowork", `{}`, `{}`, `{"env":{"CLAUDE_CODE_USE_COWORK_PLUGINS":"1"}}`, ``},
	} {
		t.Run(row.name, func(t *testing.T) {
			s, _ := newTestService(t, row.global, parseObject(row.env), true)
			if row.cc != "" {
				_ = os.MkdirAll(filepath.Join(s.options.ConfigHome, "cc-haha"), 0777)
				_ = os.WriteFile(filepath.Join(s.options.ConfigHome, "cc-haha", "settings.json"), []byte(row.cc), 0600)
			}
			if row.user != "" {
				_ = os.WriteFile(filepath.Join(s.options.ConfigHome, "settings.json"), []byte(row.user), 0600)
			}
			var err error
			if strings.HasPrefix(row.name, "CA") {
				err = s.ApplyExtraCACertsFromConfig()
			} else {
				err = s.ApplySafeConfigEnvironmentVariables()
			}
			if err == nil {
				t.Fatal("unsupported capability passed")
			}
			if row.name == "CA active hook schema" {
				var capability *clisettings.CapabilityError
				if !errors.As(err, &capability) || !strings.Contains(capability.Error(), "active hook matcher/definition schema") {
					t.Fatal("CA must propagate the unported active-hook capability", err)
				}
				if s.options.Environment.Get("NODE_EXTRA_CA_CERTS") != nil {
					t.Fatal("CA capability failure applied a fabricated fallback")
				}
			}
		})
	}
	t.Run("CC host bypass proxy check still checks provider read", func(t *testing.T) {
		s, _ := newTestService(t, "{}", parseObject(`{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":"1"}`), true)
		_ = os.MkdirAll(filepath.Join(s.options.ConfigHome, "cc-haha"), 0777)
		_ = os.WriteFile(filepath.Join(s.options.ConfigHome, "cc-haha", "providers.json"), []byte("{}"), 0600)
		if err := s.ApplySafeConfigEnvironmentVariables(); err == nil {
			t.Fatal("host-managed provider file silently ignored")
		}
	})
	t.Run("predicate typed input", func(t *testing.T) {
		s, _ := newTestService(t, "{}", parseObject(`{"CLAUDE_CODE_USE_COWORK_PLUGINS":1}`), true)
		if err := s.ApplyExtraCACertsFromConfig(); err == nil {
			t.Fatal("typed predicate ignored")
		}
		_ = s.options.Environment.SetProperty(quote("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"), []byte("1"))
		if _, err := s.ccHahaEnv(); err == nil {
			t.Fatal("typed host provider ignored")
		}
	})
	t.Run("cached user array container", func(t *testing.T) {
		s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
		value := parseObject(`{"env":[]}`)
		s.options.SettingsState.SetCachedSettingsForSource("userSettings", &value)
		if err := s.ApplyExtraCACertsFromConfig(); err == nil {
			t.Fatal("unsupported cached container ignored")
		}
	})
	t.Run("plugin snapshot contradiction", func(t *testing.T) {
		s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
		o := protocol.NewJSONObject()
		s.options.SettingsState.SetPluginSettingsBase(&o)
		if _, err := New(s.options); err == nil {
			t.Fatal("plugin cache ignored")
		}
	})
}
func TestResolverMergeAndPathGuards(t *testing.T) {
	for _, row := range []struct{ name, user, project, flag, inline string }{
		{"nested merger", `{"future":{"list":[1]}}`, `{"future":{"list":{}}}`, ``, ``},
		{"flag merger", ``, ``, `{"future":[1]}`, `{"future":{}}`},
		{"inline final merger", `{"future":[1]}`, ``, ``, `{"future":{}}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
			for _, source := range []struct{ name, raw string }{{"userSettings", row.user}, {"projectSettings", row.project}} {
				if source.raw != "" {
					path, _ := s.options.Settings.Path(source.name, "")
					_ = os.WriteFile(path, []byte(source.raw), 0600)
				}
			}
			if row.flag != "" {
				path := filepath.Join(s.options.ConfigHome, "flag.json")
				_ = os.WriteFile(path, []byte(row.flag), 0600)
				s.options.Host.FlagPath = &path
			}
			if row.inline != "" {
				s.options.Host.FlagInline = []byte(row.inline)
			}
			if err := s.ApplySafeConfigEnvironmentVariables(); err == nil {
				t.Fatal("unsupported merge/path admitted")
			}
		})
	}
	t.Run("unknown inline known schema after cached flag", func(t *testing.T) {
		s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
		s.options.SettingsState.SetCachedSettingsForSource("flagSettings", nil)
		s.options.Host.FlagInline = []byte(`{"hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"CC_HAHA_FORBIDDEN_STARTUP_HOOK"}]}]}}`)
		err := s.ApplySafeConfigEnvironmentVariables()
		var capability *clisettings.CapabilityError
		if !errors.As(err, &capability) || !strings.Contains(capability.Error(), "active hook matcher/definition schema") {
			t.Fatal("cached flag null skipped actual active inline validation", err)
		}
		if value, exists := s.options.SettingsState.GetCachedSettingsForSource("flagSettings"); !exists || value != nil {
			t.Fatal("failure changed the original null flag cache")
		}
		if s.options.SettingsState.GetSessionSettingsCache() != nil {
			t.Fatal("unsupported inline schema became a fabricated empty session cache")
		}
	})
	t.Run("readonly filters preserve numeric predicates", func(t *testing.T) {
		s, _ := newTestService(t, "{}", parseObject(`{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":0}`), true)
		if err := s.ApplySafeConfigEnvironmentVariables(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFrozenHostControlsCannotRedirectReads(t *testing.T) {
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "USER_TYPE", "CLAUDE_CODE_ENABLE_XAA", "CLAUDE_CODE_SIMPLE", "__proto__", "constructor", "prototype"} {
		t.Run(key, func(t *testing.T) {
			raw := `{"env":{"BEFORE":"kept",` + string(quote(key)) + `:"unresolved-host","AFTER":"never"}}`
			s, _ := newTestService(t, raw, protocol.NewJSONObject(), true)
			if err := s.ApplySafeConfigEnvironmentVariables(); err == nil {
				t.Fatal("host redirect accepted")
			}
			if !equalString(s.options.Environment.Get("BEFORE"), "kept") || s.options.Environment.Get("AFTER") != nil {
				t.Fatal("capability guard lost assignment order")
			}
		})
	}
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	_ = s.options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote("/unresolved"))
	if err := s.ApplySafeConfigEnvironmentVariables(); err == nil {
		t.Fatal("caller redirected fixed config service")
	}
	if err := s.ApplyExtraCACertsFromConfig(); err == nil {
		t.Fatal("CA read used mismatched fixed config service")
	}
	options := s.options
	_ = options.Environment.SetProperty(quote("USER_TYPE"), quote("ant"))
	if _, err := New(options); err == nil {
		t.Fatal("internal schema host accepted")
	}
}

// Actual Source applySafeConfigEnvironmentVariables accepts the original
// malformed ordinary .git marker with absent local settings. Git-ignore work
// belongs to UpdateEditableSource, not read-path parsing/merged safe env.
func TestResolverSafeReadWithOriginalMalformedGitMarker(t *testing.T) {
	s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	projectPath, err := s.options.Settings.Path("projectSettings", "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(projectPath))
	git := filepath.Join(root, ".git")
	if err = os.WriteFile(git, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplySafeConfigEnvironmentVariables(); err != nil {
		t.Fatal("actual Source safe read rejected", err)
	}
	localPath, err := s.options.Settings.Path("localSettings", "")
	if err != nil || localPath != filepath.Join(root, ".claude", "settings.local.json") {
		t.Fatal(localPath, err)
	}
	value, err := s.options.Settings.GetEditableSource("localSettings", "")
	if err != nil || value != nil {
		t.Fatal("Source absent local read was not null", value, err)
	}
	if _, err = os.Stat(localPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("safe read wrote local settings", err)
	}
	if raw, err := os.ReadFile(git); err != nil || string(raw) != "fixture" {
		t.Fatal("safe read changed original marker", string(raw), err)
	}
	// Keep the unported async local-write/gitignore capability separate.
	_, err = s.options.Settings.UpdateEditableSource("localSettings", "", clisettings.Patch{Object: parseObject(`{"model":"original-update"}`)})
	var cap *clisettings.CapabilityError
	if !errors.As(err, &cap) || cap.Capability != "local settings write Git-ignore continuation" {
		t.Fatal("unported write bypassed", err)
	}
	if s.options.SettingsState.ConsumeInternalWrite(localPath, 5000) {
		t.Fatal("write guard installed marker")
	}
	if _, err = os.Stat(localPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("guard wrote local settings", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("guard wrote gitignore", err)
	}
}
