package climigrations

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Only outer observation maps are canonical. Ordered JSON history/config
// strings and actual file base64 stay byte-for-byte semantic observations.
func canonical(raw json.RawMessage) []byte {
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
			out = append(out, canonical(field.Value)...)
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
			out = append(out, canonical(value)...)
		}
		return append(out, ']')
	}
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}
func normalizeBytes(value any, home string) any {
	switch value := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range value {
			if key == "rawBase64" {
				decoded, _ := base64.StdEncoding.DecodeString(item.(string))
				out[key] = base64.StdEncoding.EncodeToString(bytes.ReplaceAll(decoded, []byte(home), []byte("<HOME>")))
			} else {
				out[key] = normalizeBytes(item, home)
			}
		}
		return out
	case []Event:
		out := []any{}
		for _, item := range value {
			out = append(out, normalizeBytes(map[string]any(item), home))
		}
		return out
	case []json.RawMessage:
		out := []json.RawMessage{}
		for _, raw := range value {
			out = append(out, json.RawMessage(bytes.ReplaceAll(raw, []byte(home), []byte("<HOME>"))))
		}
		return out
	case Event:
		return normalizeBytes(map[string]any(value), home)
	case string:
		return strings.ReplaceAll(value, home, "<HOME>")
	case json.RawMessage:
		return json.RawMessage(bytes.ReplaceAll(value, []byte(home), []byte("<HOME>")))
	default:
		return value
	}
}
func TestActualSourceGoldens(t *testing.T) {
	data, err := os.ReadFile("testdata/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input    fixtureInput
		Expected struct{ Result json.RawMessage }
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.Input.Name, func(t *testing.T) {
			row.Input.Home = t.TempDir()
			result, err := executeFixture(row.Input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(normalizeBytes(result, row.Input.Home))
			if !bytes.Equal(canonical(raw), canonical(row.Expected.Result)) {
				t.Fatalf("actual Source mismatch\nGo=%s\nSource=%s", raw, row.Expected.Result)
			}
		})
	}
}
func newNative(t *testing.T, profile, config string) (*Service, *cliglobalconfig.Service, *clisettings.Service, string) {
	t.Helper()
	home := t.TempDir()
	configHome := filepath.Join(home, ".claude")
	project := filepath.Join(home, "project")
	_ = os.MkdirAll(configHome, 0777)
	_ = os.MkdirAll(project, 0777)
	path := filepath.Join(configHome, ".claude.json")
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(config, "<HOME>", home)), 0600)
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &configHome, Now: func() time.Time { return time.UnixMilli(1000) }, PID: 100, Stderr: func(string) {}, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = global.Close() })
	if err = global.Enable(); err != nil {
		t.Fatal(err)
	}
	state, _ := clisettings.NewState(func() float64 { return 1000 })
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: configHome, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1000 }, PID: 100})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Global: global, Settings: settings, ConfigHome: configHome, Now: func() float64 { return 1000 }, Host: Host{Profile: profile, EmptyCredentialStore: true, OriginalCWD: project}})
	if err != nil {
		t.Fatal(err)
	}
	return service, global, settings, home
}
func TestRejectedHostCannotStamp(t *testing.T) {
	service, _, _, _ := newNative(t, ExternalEmptyOAuthProfile, `{}`)
	cases := []struct {
		name   string
		change func(*Options)
	}{{"missing", func(opt *Options) { opt.Global = nil }}, {"profile", func(opt *Options) { opt.Host.Profile = "internal" }}, {"credential", func(opt *Options) { opt.Host.EmptyCredentialStore = false }}, {"environment", func(opt *Options) { opt.Host.Environment = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"} }}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			options := service.options
			test.change(&options)
			if _, err := New(options); err == nil {
				t.Fatal("unresolved host accepted")
			}
		})
	}
}
func TestGitCanonicalizationStopsAtActualLookup(t *testing.T) {
	service, global, _, home := newNative(t, ExternalEmptyOAuthProfile, `{"autoUpdates":false}`)
	_ = os.Mkdir(filepath.Join(home, "project", ".git"), 0777)
	_, err := service.Run()
	var capability *CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal(err)
	}
	config, _ := global.Get()
	if config.Get("migrationVersion") != nil {
		t.Fatal("unported Git host stamped11")
	}
	if service.DisableAutoUpdater() == nil {
		t.Fatal("earlier actual auto-update effects disappeared")
	}
}
func TestUnportedActiveSettingsCannotAdvanceMarker(t *testing.T) {
	service, global, _, home := newNative(t, ExternalEmptyOAuthProfile, `{"autoUpdates":false}`)
	_ = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"sandbox":{}}`), 0600)
	_, err := service.Run()
	var capability *clisettings.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatal(err)
	}
	config, _ := global.Get()
	if config.Get("migrationVersion") != nil || config.Get("autoUpdates") == nil {
		t.Fatal("capability treated as returned write error")
	}
}
func TestMCPMalformedGlobalCapabilityStopsBeforeStamp(t *testing.T) {
	for _, raw := range []string{`123`, `[123]`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			service, global, _, _ := newNative(t, ExternalEmptyOAuthProfile, fmt.Sprintf(`{"projects":{"<HOME>/project":{"enabledMcpjsonServers":%s}}}`, raw))
			config, _ := global.Get()
			if config.Get("projects") == nil {
				t.Fatal("original project input lost before migration")
			}
			_, err := service.Run()
			var capability *CapabilityError
			if !errors.As(err, &capability) {
				t.Fatal(err)
			}
			config, _ = global.Get()
			if config.Get("migrationVersion") != nil {
				t.Fatal("malformed approval shape stamped")
			}
		})
	}
}

type gatedFS struct {
	OSFileSystem
	entered chan struct{}
	gate    <-chan struct{}
}

func (f gatedFS) MkdirAll(path string) error {
	close(f.entered)
	<-f.gate
	return f.OSFileSystem.MkdirAll(path)
}
func TestChangelogIsPendingAtReturnAndExclusive(t *testing.T) {
	service, global, _, home := newNative(t, ExternalEmptyOAuthProfile, `{"migrationVersion":11,"cachedChangelog":"original"}`)
	gate := make(chan struct{})
	entered := make(chan struct{})
	service.options.FS = gatedFS{entered: entered, gate: gate}
	task, err := service.Run()
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	config, _ := global.Get()
	if config.Get("cachedChangelog") == nil {
		t.Fatal("Run waited/cleared before mkdir promise completed")
	}
	path := filepath.Join(home, ".claude", "cache", "changelog.md")
	_ = os.MkdirAll(filepath.Dir(path), 0777)
	_ = os.WriteFile(path, []byte("existing"), 0600)
	close(gate)
	if err = task.Wait(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "existing" {
		t.Fatal("wx overwrote existing")
	}
	config, _ = global.Get()
	if config.Get("cachedChangelog") != nil {
		t.Fatal("exclusive failure prevented cleanup")
	}
	if (*ChangelogTask)(nil).Wait() != nil {
		t.Fatal("nil background task")
	}
}
func TestExclusiveFileSystemErrors(t *testing.T) {
	fs := OSFileSystem{}
	home := t.TempDir()
	if fs.MkdirAll(filepath.Join(home, "dir")) != nil {
		t.Fatal("mkdir")
	}
	path := filepath.Join(home, "dir", "notes")
	if err := fs.WriteExclusive(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteExclusive(path, []byte("bad")); !os.IsExist(err) {
		t.Fatal(err)
	}
	if err := fs.WriteExclusive(filepath.Join(home, "missing", "notes"), nil); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestScalarIdentityAndTruthiness(t *testing.T) {
	for _, raw := range []string{"11", "11.0", "1.1e1"} {
		if !numericEleven([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`"11"`, `null`, `[]`, `false`, ""} {
		if numericEleven([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`[]`, `{}`, `true`, `1e400`, `"x"`} {
		if !truthy([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"", `null`, `false`, `""`, `-0`, `1e-400`} {
		if truthy([]byte(raw)) {
			t.Fatal(raw)
		}
	}
}
