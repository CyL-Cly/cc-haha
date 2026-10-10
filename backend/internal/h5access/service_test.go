package h5access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type testRandom struct {
	calls, generation int
	fail              bool
}

func (random *testRandom) Read(data []byte) (int, error) {
	if random.fail {
		return 0, errors.New("entropy failed")
	}
	random.calls++
	random.generation++
	for index := range data {
		data[index] = byte(index + random.generation*17)
	}
	return len(data), nil
}

func testService(t *testing.T) (*Service, *testRandom, string, map[string]string, *[]NetworkInterface) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	environment := map[string]string{"CLAUDE_CONFIG_DIR": config}
	store := storage.New(storage.Options{Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := settings.NewManaged(settings.Options{Store: store, HomeDir: home, Environment: func() map[string]string { return environment }, EnsureMigrated: upgrader.SettingsCallback})
	if err != nil {
		t.Fatal(err)
	}
	interfaces := []NetworkInterface{{Name: "en0", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "192.168.1.2"}}}}
	random := &testRandom{}
	service, err := New(Options{Managed: managed, Environment: func() map[string]string { return environment }, NetworkInterfaces: func() []NetworkInterface { return interfaces }, ServerPort: func() int { return 3000 }, Random: random})
	if err != nil {
		t.Fatal(err)
	}
	return service, random, config, environment, &interfaces
}

func rawObject(value string) storage.Object {
	var result storage.Object
	_ = json.Unmarshal([]byte(value), &result)
	return result
}
func writeManaged(t *testing.T, config, value string) {
	t.Helper()
	path := filepath.Join(config, "cc-haha", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTokenLifecycleUsesManagedSettingsAndPreservesOuterFields(t *testing.T) {
	service, random, config, _, _ := testService(t)
	ctx := context.Background()
	writeManaged(t, config, `{"future":{"nested":true},"env":{"unrelated":"keep"},"h5Access":{"future":"legacy"}}`)
	empty, err := service.GetSettings(ctx)
	if err != nil || empty.Enabled || empty.Token != nil || empty.PublicBaseURL != nil || len(empty.AllowedOrigins) != 0 {
		t.Fatalf("empty=%+v error=%v", empty, err)
	}
	first, err := service.Enable(ctx)
	if err != nil || !first.Settings.Enabled || !validToken(first.Token) || first.Settings.Token == nil || *first.Settings.Token != first.Token || random.calls != 1 {
		t.Fatalf("first=%+v error=%v random=%+v", first, err, random)
	}
	second, err := service.Enable(ctx)
	if err != nil || first.Token != second.Token || random.calls != 1 {
		t.Fatal("enable rotated token", second, err, random)
	}
	updated, err := service.UpdateSettings(ctx, rawObject(`{"allowedOrigins":["https://EXAMPLE.test:443/path","https://example.test","http://127.1:3000"],"publicBaseUrl":"http://192.168.1.2:4000///?ignored=1#ignored","fixedPort":1024,"disconnectGraceSeconds":5}`))
	if err != nil || !reflect.DeepEqual(updated.AllowedOrigins, []string{"https://example.test", "http://127.0.0.1:3000"}) || updated.PublicBaseURL == nil || *updated.PublicBaseURL != "http://192.168.1.2:4000" || *updated.FixedPort != 1024 {
		t.Fatalf("updated=%+v error=%v", updated, err)
	}
	if valid, err := service.ValidateToken(ctx, first.Token); err != nil || !valid {
		t.Fatal(valid, err)
	}
	if valid, err := service.ValidateToken(ctx, "wrong"); err != nil || valid {
		t.Fatal(valid, err)
	}
	if allowed, err := service.IsOriginAllowed(ctx, "https://example.test:443/path"); err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	for _, origin := range []string{"https://other.test", "https://*.test", "file://app", "https://user@example.test", "invalid", ""} {
		if allowed, err := service.IsOriginAllowed(ctx, origin); err != nil || allowed {
			t.Fatalf("origin=%s allowed=%v error=%v", origin, allowed, err)
		}
	}
	disabled, err := service.Disable(ctx)
	if err != nil || disabled.Enabled || *disabled.Token != first.Token {
		t.Fatal(disabled, err)
	}
	if valid, err := service.ValidateToken(ctx, first.Token); err != nil || valid {
		t.Fatal(valid, err)
	}
	if allowed, err := service.IsOriginAllowed(ctx, "https://example.test"); err != nil || allowed {
		t.Fatal(allowed, err)
	}
	third, err := service.Enable(ctx)
	if err != nil || third.Token != first.Token || random.calls != 1 {
		t.Fatal(third, err)
	}
	rotated, err := service.RegenerateToken(ctx)
	if err != nil || rotated.Token == first.Token || random.calls != 2 {
		t.Fatal(rotated, err, random)
	}
	if valid, err := service.ValidateToken(ctx, first.Token); err != nil || valid {
		t.Fatal(valid, err)
	}
	if valid, err := service.ValidateToken(ctx, rotated.Token); err != nil || !valid {
		t.Fatal(valid, err)
	}
	if grace, err := service.GetDisconnectGraceMS(ctx); err != nil || grace != 5000 {
		t.Fatal(grace, err)
	}
	data, err := os.ReadFile(filepath.Join(config, "cc-haha", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	stored := rawObject(string(data))
	var future, environment bytes.Buffer
	_ = json.Compact(&future, stored["future"])
	_ = json.Compact(&environment, stored["env"])
	if future.String() != `{"nested":true}` || environment.String() != `{"unrelated":"keep"}` || bytes.Contains(stored["h5Access"], []byte("future")) {
		t.Fatalf("unknown field policy changed: %s", data)
	}
	cleared, err := service.UpdateSettings(ctx, rawObject(`{"allowedOrigins":[],"publicBaseUrl":null,"fixedPort":null,"disconnectGraceSeconds":null}`))
	if err != nil || cleared.PublicBaseURL != nil || cleared.FixedPort != nil || cleared.DisconnectGraceSeconds != nil {
		t.Fatal(cleared, err)
	}
	if grace, err := service.GetDisconnectGraceMS(ctx); err != nil || grace != DefaultDisconnectGraceMS {
		t.Fatal(grace, err)
	}
}

func TestSettingsValidationLeavesPersistentStateUntouched(t *testing.T) {
	service, _, config, _, _ := testService(t)
	ctx := context.Background()
	if _, err := service.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "cc-haha", "settings.json")
	before, _ := os.ReadFile(path)
	for _, fixture := range []struct{ patch, message string }{
		{`{"publicBaseUrl":42}`, "publicBaseUrl must be a string or null"},
		{`{"publicBaseUrl":"invalid"}`, "Invalid publicBaseUrl: invalid"},
		{`{"publicBaseUrl":"file:///app"}`, "Invalid publicBaseUrl protocol: file:///app"},
		{`{"publicBaseUrl":"http://user@server"}`, "Invalid publicBaseUrl credentials: http://user@server"},
		{`{"publicBaseUrl":"http://192.168.9.9:3000"}`, "H5 host 192.168.9.9 is not bound to any local network interface on this machine. Available LAN IPv4: 192.168.1.2"},
		{`{"fixedPort":6000}`, "fixedPort must be a browser-safe integer between 1024 and 65535"},
		{`{"fixedPort":1e400}`, "fixedPort must be a browser-safe integer between 1024 and 65535"},
		{`{"fixedPort":"3000"}`, "fixedPort must be a browser-safe integer between 1024 and 65535"},
		{`{"disconnectGraceSeconds":4}`, "disconnectGraceSeconds must be an integer between 5 and 86400"},
		{`{"disconnectGraceSeconds":86401}`, "disconnectGraceSeconds must be an integer between 5 and 86400"},
		{`{"allowedOrigins":null}`, "allowedOrigins must be an array of strings"},
		{`{"allowedOrigins":[42]}`, "allowedOrigins must be an array of strings"},
		{`{"allowedOrigins":["http://*.test"]}`, "allowedOrigins must not contain wildcard origins"},
		{`{"allowedOrigins":["invalid"]}`, "Invalid origin: invalid"},
		{`{"allowedOrigins":["file://host"]}`, "Invalid origin protocol: file://host"},
		{`{"allowedOrigins":["http://user@host"]}`, "Invalid origin credentials: http://user@host"},
	} {
		_, err := service.UpdateSettings(ctx, rawObject(fixture.patch))
		var apiError *settings.APIError
		if !errors.As(err, &apiError) || apiError.StatusCode != 400 || apiError.Code != "BAD_REQUEST" || err.Error() != fixture.message {
			t.Fatalf("patch=%s error=%v", fixture.patch, err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("rejected patch wrote settings: %s", fixture.patch)
		}
	}
	if value, err := service.UpdateSettings(ctx, rawObject(`{"publicBaseUrl":"","fixedPort":65535,"disconnectGraceSeconds":86400}`)); err != nil || value.PublicBaseURL != nil || *value.FixedPort != 65535 || *value.DisconnectGraceSeconds != 86400 {
		t.Fatal(value, err)
	}
}

func TestStoredSettingsNormalizationAndLegacyHash(t *testing.T) {
	for _, raw := range []string{"", `null`, `[]`, `true`, `42`, `{"enabled":true,"token":42,"tokenHash":"INVALID"}`} {
		normalized := normalizeStored(json.RawMessage(raw))
		if normalized.Enabled || normalized.Token != nil || normalized.TokenHash != nil || len(normalized.AllowedOrigins) != 0 {
			t.Fatalf("raw=%s value=%+v", raw, normalized)
		}
	}
	for _, token := range []string{"short", strings.Repeat("a", 513), "has space invalid token", strings.Repeat("中", 16), "valid\ninvalidtoken"} {
		if validToken(token) {
			t.Errorf("accepted token=%q", token)
		}
	}
	plain := "custom_visible_token"
	raw, _ := jsonMarshal(map[string]any{"enabled": true, "token": plain, "tokenHash": strings.Repeat("f", 64), "tokenPreview": "wrong", "allowedOrigins": []any{"http://EXAMPLE.test:80/path", 42, "http://example.test", "bad", "file:///app", "http://u@host", "http://*.test"}, "publicBaseUrl": "file:///invalid", "fixedPort": 6000, "disconnectGraceSeconds": 1.1})
	value := normalizeStored(raw)
	preview, _ := stringValue(value.TokenPreview)
	if !value.Enabled || *value.TokenHash != hashToken(plain) || *preview != tokenPreview(plain) || value.PublicBaseURL != nil || value.FixedPort != nil || value.DisconnectGraceSeconds != nil || !reflect.DeepEqual(value.AllowedOrigins, []string{"http://example.test"}) {
		t.Fatalf("value=%+v", value)
	}
	service, _, config, _, _ := testService(t)
	writeManaged(t, config, `{"h5Access":{"enabled":true,"tokenHash":"`+hashToken(plain)+`","tokenPreview":"legacy","publicBaseUrl":"https://proxy.test/path/"}}`)
	stored, err := service.GetSettings(context.Background())
	preview, _ = stringValue(stored.TokenPreview)
	if err != nil || !stored.Enabled || stored.Token != nil || *preview != "legacy" {
		t.Fatal(stored, err)
	}
	if valid, err := service.ValidateToken(context.Background(), plain); err != nil || !valid {
		t.Fatal(valid, err)
	}
	legacy := normalizeStored(json.RawMessage(`{"enabled":true,"tokenHash":"` + hashToken(plain) + `","tokenPreview":false}`))
	if legacy.TokenPreview != nil {
		t.Fatal(legacy)
	}
}

func TestLegacyPreviewRetainsLoneUTF16AcrossDisable(t *testing.T) {
	service, _, config, _, _ := testService(t)
	writeManaged(t, config, `{"h5Access":{"enabled":true,"tokenHash":"`+hashToken("legacy-visible-token")+`","tokenPreview":"A\ud800B\udfffC"}}`)
	value, err := service.Disable(context.Background())
	if err != nil || string(value.TokenPreview) != `"A\ud800B\udfffC"` {
		t.Fatalf("preview=%s error=%v", value.TokenPreview, err)
	}
	persisted, err := os.ReadFile(filepath.Join(config, "cc-haha", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(persisted, []byte(`"A\ud800B\udfffC"`)) {
		t.Fatalf("preview replaced in history: %s", persisted)
	}
}

func TestDiagnosticsAndEnvironmentDoNotPersistAutoDiscovery(t *testing.T) {
	service, _, config, environment, interfaces := testService(t)
	ctx := context.Background()
	diagnostics, err := service.GetDiagnostics(ctx)
	if err != nil || diagnostics.StoredHostStaleness != "unset" || diagnostics.ActivePort != 3000 || *diagnostics.SuggestedHost != "192.168.1.2" {
		t.Fatal(diagnostics, err)
	}
	if _, err := service.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(ctx, rawObject(`{"publicBaseUrl":"http://192.168.1.2:4000"}`)); err != nil {
		t.Fatal(err)
	}
	environment["CLAUDE_H5_AUTO_PUBLIC_URL"] = "1"
	diagnostics, err = service.GetDiagnostics(ctx)
	if err != nil || diagnostics.StoredHostStaleness != "ok" || *diagnostics.EffectivePublicBaseURL != "http://192.168.1.2:3000" {
		t.Fatal(diagnostics, err)
	}
	*interfaces = []NetworkInterface{{Name: "Ethernet", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "10.0.0.2"}}}}
	diagnostics, err = service.GetDiagnostics(ctx)
	if err != nil || diagnostics.StoredHostStaleness != "unreachable" || *diagnostics.EffectivePublicBaseURL != "http://10.0.0.2:3000" {
		t.Fatal(diagnostics, err)
	}
	if _, err := service.UpdateSettings(ctx, rawObject(`{"publicBaseUrl":"https://proxy.test/app///"}`)); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = service.GetDiagnostics(ctx)
	if err != nil || diagnostics.StoredHostStaleness != "proxy" || *diagnostics.EffectivePublicBaseURL != "https://proxy.test/app" {
		t.Fatal(diagnostics, err)
	}
	environment["CLAUDE_H5_PUBLIC_BASE_URL"] = "https://configured.test/path///?ignored=yes"
	public, err := service.GetSettings(ctx)
	if err != nil || *public.PublicBaseURL != "https://configured.test/path" {
		t.Fatal(public, err)
	}
	environment["CLAUDE_H5_PUBLIC_BASE_URL"] = "invalid"
	public, err = service.GetSettings(ctx)
	if err != nil || *public.PublicBaseURL != "https://proxy.test/app" {
		t.Fatal(public, err)
	}
	*interfaces = nil
	public, err = service.GetSettings(ctx)
	if err != nil || *public.PublicBaseURL != "https://proxy.test/app" {
		t.Fatal(public, err)
	}
	data, _ := os.ReadFile(filepath.Join(config, "cc-haha", "settings.json"))
	if bytes.Contains(data, []byte("configured.test")) || bytes.Contains(data, []byte("10.0.0.2")) {
		t.Fatal("ephemeral discovery was persisted", string(data))
	}
}

type failureFS struct {
	storage.FileSystem
	failRead, failRename bool
}

func (filesystem failureFS) ReadFile(path string) ([]byte, error) {
	if filesystem.failRead {
		return nil, fs.ErrPermission
	}
	return filesystem.FileSystem.ReadFile(path)
}
func (filesystem failureFS) Rename(from, to string) error {
	if filesystem.failRename {
		return errors.New("rename failed")
	}
	return filesystem.FileSystem.Rename(from, to)
}

func TestBootstrapAndIOFailuresDoNotBypassManagedSettings(t *testing.T) {
	service, random, _, _, _ := testService(t)
	for _, missing := range []string{"managed", "environment", "network", "port"} {
		options := service.options
		switch missing {
		case "managed":
			options.Managed = nil
		case "environment":
			options.Environment = nil
		case "network":
			options.NetworkInterfaces = nil
		case "port":
			options.ServerPort = nil
		}
		if _, err := New(options); err == nil {
			t.Errorf("accepted missing %s", missing)
		}
	}
	options := service.options
	options.Random = nil
	if value, err := New(options); err != nil || value.options.Random == nil {
		t.Fatal(value, err)
	}
	random.fail = true
	if _, err := service.Enable(context.Background()); err == nil || err.Error() != "entropy failed" {
		t.Fatal(err)
	}
	for _, read := range []bool{true, false} {
		home := t.TempDir()
		base := storage.New(storage.Options{Warn: func(string) {}}).FileSystem()
		store := storage.New(storage.Options{FS: failureFS{FileSystem: base, failRead: read, failRename: !read}, Warn: func(string) {}})
		upgrader, _ := migrations.New(migrations.Options{Store: store, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		managed, _ := settings.NewManaged(settings.Options{Store: store, HomeDir: home, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: upgrader.SettingsCallback})
		value, _ := New(Options{Managed: managed, Environment: func() map[string]string { return map[string]string{} }, NetworkInterfaces: func() []NetworkInterface { return nil }, ServerPort: func() int { return 3000 }, Random: &testRandom{}})
		ctx := context.Background()
		if read {
			if valid, err := value.ValidateToken(ctx, ""); err != nil || valid {
				t.Fatal("empty token touched storage", valid, err)
			}
			if allowed, err := value.IsOriginAllowed(ctx, ""); err != nil || allowed {
				t.Fatal("empty origin touched storage", allowed, err)
			}
			for _, call := range []func() error{
				func() error { _, err := value.GetSettings(ctx); return err }, func() error { _, err := value.GetDiagnostics(ctx); return err }, func() error { _, err := value.GetDisconnectGraceMS(ctx); return err }, func() error { _, err := value.ValidateToken(ctx, "token"); return err }, func() error { _, err := value.IsOriginAllowed(ctx, "http://test"); return err },
			} {
				if err := call(); err == nil || !errors.Is(err, fs.ErrPermission) {
					t.Fatal("read failure ignored", err)
				}
			}
		}
		if _, err := value.Enable(ctx); err == nil {
			t.Fatal("managed write/read failure ignored")
		}
	}
}

func TestJSONNumberPortAndGraceValidation(t *testing.T) {
	for _, raw := range []string{"", `null`, `true`, `false`, `[]`, `{}`, `"3000"`, `1e400`, `-1`, `1.1`, `0`} {
		if normalizeFixedPort(json.RawMessage(raw)) != nil || normalizeGrace(json.RawMessage(raw)) != nil {
			t.Errorf("accepted numeric field %s", raw)
		}
	}
	for _, port := range []float64{0, 1719, 2049, 5060, 6000, 6667, 10080} {
		if IsBrowserSafePort(port) {
			t.Fatalf("browser-blocked port=%v", port)
		}
	}
	for _, port := range []float64{1024, 3000, 4430, 65535} {
		if !IsBrowserSafePort(port) {
			t.Fatalf("browser-safe port=%v", port)
		}
	}
	if IsBrowserSafePort(math.NaN()) || IsBrowserSafePort(65536) || IsBrowserSafePort(1.5) {
		t.Fatal("invalid browser port accepted")
	}
	if _, err := jsonMarshal(make(chan int)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

var _ io.Reader = (*testRandom)(nil)
