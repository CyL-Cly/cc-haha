package ccswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func source(t *testing.T, home string, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ccswitch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	return out
}
func compare(t *testing.T, want json.RawMessage, got any) {
	t.Helper()
	if !jsonEqual(want, raw(got)) {
		t.Fatalf("Source%s\nGo%s", want, raw(got))
	}
}
func jsonEqual(a, b json.RawMessage) bool {
	if isString(a) || isString(b) {
		return isString(a) && isString(b) && string(protocol.QuoteJavaScriptUTF16(units(a))) == string(protocol.QuoteJavaScriptUTF16(units(b)))
	}
	x, y := obj(a), obj(b)
	if x.IsObject() || y.IsObject() {
		if !x.IsObject() || !y.IsObject() || len(x.OwnProperties()) != len(y.OwnProperties()) {
			return false
		}
		for _, p := range x.OwnProperties() {
			found := false
			for _, q := range y.OwnProperties() {
				if jsonEqual(p.NameJSON, q.NameJSON) {
					found = true
					if !jsonEqual(p.Value, q.Value) {
						return false
					}
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if bytes.HasPrefix(bytes.TrimSpace(a), []byte("[")) || bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
		x, y := array(a), array(b)
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return string(bytes.TrimSpace(a)) == string(bytes.TrimSpace(b))
}
func presets(t *testing.T) json.RawMessage {
	t.Helper()
	p, e := os.ReadFile("../server/providerconfig/presets.json")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCcSwitchMappingAgainstActualOriginalProducer(t *testing.T) {
	home := t.TempDir()
	cases := []map[string]any{}
	rows := []Row{}
	credentials := []map[string]any{
		{"ANTHROPIC_AUTH_TOKEN": "fake-long-credential"}, {"ANTHROPIC_API_KEY": "fake-long-credential"},
		{"ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "fake-long-credential"}, {"ANTHROPIC_API_KEY": "dummy", "ANTHROPIC_AUTH_TOKEN": "dummy"},
		{"ANTHROPIC_API_KEY": "same-token", "ANTHROPIC_AUTH_TOKEN": "same-token"}, {"ANTHROPIC_API_KEY": "api-token", "ANTHROPIC_AUTH_TOKEN": "auth-token"},
		{"ANTHROPIC_API_KEY": "real-fake-key", "ANTHROPIC_AUTH_TOKEN": "proxy-managed"}, {"ANTHROPIC_AUTH_TOKEN": "••••••••"},
		{"ANTHROPIC_AUTH_TOKEN": "(Your API Key)"}, {"ANTHROPIC_AUTH_TOKEN": true}, {}, {"ANTHROPIC_AUTH_TOKEN": "short"},
	}
	for _, keys := range credentials {
		for _, format := range []string{"anthropic", "openai_chat", "openai_responses", "gemini_native", ""} {
			for _, auth := range []string{"", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
				env := map[string]any{"ANTHROPIC_BASE_URL": " https://api.deepseek.com/anthropic/ ", "ANTHROPIC_MODEL": " model[1m] ", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "small:1m", "ANTHROPIC_DEFAULT_FABLE_MODEL": "fable[1m]", "ENABLE_TOOL_SEARCH": "auto:5", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "yes", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "0x10000", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"model":1000000,"other":"65536","invalid":3}`}
				for k, v := range keys {
					env[k] = v
				}
				meta := map[string]any{"apiFormat": format, "apiKeyField": auth}
				row := Row{ID: str("fixture-id"), Name: str("  Private provider  "), AppType: "claude", SettingsConfig: raw(map[string]any{"env": env}), Meta: raw(meta), Notes: str("note fake-long-credential and again fake-long-credential"), IsCurrent: true}
				rows = append(rows, row)
				cases = append(cases, map[string]any{"action": "mapping", "row": map[string]any{"id": row.ID, "name": row.Name, "appType": row.AppType, "settingsConfig": row.SettingsConfig, "meta": row.Meta, "notes": row.Notes, "isCurrent": true}})
			}
		}
	}
	for _, model := range []json.RawMessage{[]byte(`"m\ud800[1m]"`), str("m:1m[1m]"), str("m[1m]:1m"), str("m[2m]"), str("\ufeff model \ufeff"), str(""), []byte("true"), []byte("null")} {
		row := Row{ID: str("id"), Name: str("name"), AppType: "claude-desktop", SettingsConfig: raw(map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": model, "ANTHROPIC_AUTH_TOKEN": "fake-long-credential", "ANTHROPIC_BASE_URL": "https://private.invalid"}}), Meta: []byte(`{"isFullUrl":true}`)}
		rows = append(rows, row)
		cases = append(cases, map[string]any{"action": "mapping", "row": map[string]any{"id": row.ID, "name": row.Name, "appType": row.AppType, "settingsConfig": row.SettingsConfig, "meta": row.Meta, "isCurrent": false}})
	}
	for _, value := range []json.RawMessage{[]byte("true"), []byte("false"), []byte("1"), []byte("null"), str("false"), str("AUTO:10"), str(" YES "), []byte(`[]`)} {
		row := Row{ID: str("flags"), Name: str("flags"), AppType: "claude", SettingsConfig: raw(map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": "model", "ANTHROPIC_AUTH_TOKEN": "fake-long-credential", "ANTHROPIC_BASE_URL": "https://private.invalid", "ENABLE_TOOL_SEARCH": value, "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": value, "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"__proto__":1000000," constructor ":64000,"\ud800":1000000}`}})}
		rows = append(rows, row)
		cases = append(cases, map[string]any{"action": "mapping", "row": map[string]any{"id": row.ID, "name": row.Name, "appType": row.AppType, "settingsConfig": row.SettingsConfig, "isCurrent": false}})
	}
	for _, token := range []json.RawMessage{[]byte(`"sam\u0065-token"`), []byte(`"dum\u006dy"`)} {
		row := Row{ID: str("escape"), Name: str("escape"), AppType: "claude", SettingsConfig: raw(map[string]any{"env": map[string]any{"ANTHROPIC_MODEL": "model", "ANTHROPIC_AUTH_TOKEN": token, "ANTHROPIC_API_KEY": str(text(token)), "ANTHROPIC_BASE_URL": "https://private.invalid"}})}
		rows = append(rows, row)
		cases = append(cases, map[string]any{"action": "mapping", "row": map[string]any{"id": row.ID, "name": row.Name, "appType": row.AppType, "settingsConfig": row.SettingsConfig, "isCurrent": false}})
	}
	out := array(source(t, home, map[string]any{"home": home, "cases": cases}))
	if len(out) != len(rows) {
		t.Fatal("actual Source row count")
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) { compare(t, out[i], ToEntry(row, nil, nil, presets(t))) })
	}
}
