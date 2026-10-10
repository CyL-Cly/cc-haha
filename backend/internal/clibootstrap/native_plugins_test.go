package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
)

type nativePluginFixture struct {
	home, config, cwd, managed, market, plugin string
	n                                          *NativeBoundary
}

func newNativePluginFixture(t *testing.T) nativePluginFixture {
	t.Helper()
	home := t.TempDir()
	f := nativePluginFixture{home: home, config: filepath.Join(home, ".claude"), cwd: filepath.Join(home, "project"), managed: filepath.Join(home, "managed"), market: filepath.Join(home, "market")}
	f.plugin = filepath.Join(f.market, "plugins", "sample")
	f.write(t, filepath.Join(f.cwd, ".claude", "settings.json"), `{}`)
	f.write(t, filepath.Join(f.managed, "managed-settings.json"), `{}`)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"future":{"keep":true}}`)
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","settings":{"agent":"Plan"}}`)
	f.write(t, filepath.Join(f.plugin, "skills", "sample", "SKILL.md"), "---\ndescription: Actual plugin Skill\n---\nRead the project.")
	f.write(t, filepath.Join(f.market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": f.market}, "installLocation": f.market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), string(known))
	n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: map[string]string{"DISABLE_TELEMETRY": "1", "HOME": home, "CLAUDE_CONFIG_DIR": f.config, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, CWD: f.cwd, Platform: runtime.GOOS, ManagedRoot: f.managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.Temp = filepath.Join(home, "tmp")
	if err := os.MkdirAll(n.Temp, 0700); err != nil {
		t.Fatal(err)
	}
	// Native fixtures must never invoke the machine Keychain. Dedicated
	// Keychain protocol tests install an explicit subprocess primitive.
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, fmt.Errorf("isolated fixture Keychain unavailable")
	}
	f.n = n
	return f
}

func (f nativePluginFixture) write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestServerSettingsLoadsActualPluginBaseAndDropsDisabledSettings(t *testing.T) {
	f := newNativePluginFixture(t)
	p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	settings, err := p.InitialSettings()
	if err != nil {
		t.Fatalf("installed plugins block the actual server settings consumer: %v", err)
	}
	if settings.String("agent") != "Plan" || settings.Get("future") == nil {
		t.Fatalf("actual plugin settings were not merged: %+v", settings)
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	p.InvalidateSettingsCache()
	settings, err = p.InitialSettings()
	if err != nil || settings.Get("agent") != nil {
		t.Fatalf("disabled plugin retained its settings: %+v %v", settings, err)
	}
	data, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if err != nil || string(data) != `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}` {
		t.Fatal("read-only settings consumer rewrote the user's configuration", err)
	}
}

func TestServerPluginSettingsProductionBindingsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	for _, tc := range []struct{ name, source, settings string }{
		{"plugin-base", "", ""},
		{"user-overrides-plugin", "user", `{"enabledPlugins":{"sample@mkt":true},"agent":"Explore","future":{"keep":true}}`},
		{"project-overrides-user", "project", `{"agent":"ProjectAgent","future":{"project":true}}`},
		{"local-overrides-project", "local", `{"agent":"LocalAgent"}`},
		{"flag-overrides-local", "flag", `{"agent":"FlagAgent"}`},
		{"managed-overrides-files", "policy", `{"agent":"PolicyAgent"}`},
		{"managed-disables-plugin", "policy", `{"enabledPlugins":{"sample@mkt":false}}`},
		{"plugin-file-overrides-manifest", "plugin", `{"agent":"Explore","future":"discard"}`},
		{"plugin-styles-default-custom-and-duplicate", "styles", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			sources := DefaultState()
			paths := map[string]string{"user": filepath.Join(f.config, "settings.json"), "project": filepath.Join(f.cwd, ".claude", "settings.json"), "local": filepath.Join(f.cwd, ".claude", "settings.local.json"), "flag": filepath.Join(f.home, "flag.json"), "policy": filepath.Join(f.managed, "managed-settings.json"), "plugin": filepath.Join(f.plugin, "settings.json")}
			if tc.source != "" && tc.source != "styles" {
				f.write(t, paths[tc.source], tc.settings)
			}
			runtimeInput := map[string]any{"cwd": f.n.CWD}
			if tc.source == "styles" {
				f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","settings":{"agent":"Plan"},"outputStyles":["./extra.md","./output-styles/style.md","./extra-dir"]}`)
				f.write(t, filepath.Join(f.plugin, "output-styles", "style.md"), "---\nname: Custom\ndescription: false\nforce-for-plugin: 'true'\n---\n\n  Plugin prompt.\n")
				f.write(t, filepath.Join(f.plugin, "extra.md"), "---\nname: 12\ndescription: [invalid, description]\n---\n# Extra heading\nExtra prompt.")
				f.write(t, filepath.Join(f.plugin, "extra-dir", "UPPER.MD"), "\n# Upper case\nPrompt.")
				runtimeInput["includeStyles"] = true
			}
			if tc.source == "flag" {
				path := paths["flag"]
				sources.FlagSettingsPath = &path
				runtimeInput["flagPath"] = path
			}
			operations := []map[string]any{
				{"kind": "load"},
				{"kind": "write", "path": ".claude/settings.json", "content": `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`},
				{"kind": "invalidate"}, {"kind": "load"},
			}
			initialUser, err := os.ReadFile(paths["user"])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(f.home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			_, file, _, _ := runtime.Caller(0)
			fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
			input, _ := json.Marshal(map[string]any{"runtime": runtimeInput, "operations": operations})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = f.home
			cmd.Stdin = bytes.NewReader(input)
			cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + filepath.Join(f.home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source settings: %v %s", err, output)
			}
			var expected struct{ Observations []json.RawMessage }
			if err := json.Unmarshal(output, &expected); err != nil {
				t.Fatal(err, string(output))
			}
			f.write(t, paths["user"], string(initialUser))
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, sources)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			var styles interface {
				List(context.Context, string) ([]systemprompt.StyleConfig, error)
			}
			if tc.source == "styles" {
				styles, err = p.StyleLoader()
				if err != nil {
					t.Fatal(err)
				}
			}
			index := 0
			for _, operation := range operations {
				switch operation["kind"] {
				case "write":
					f.write(t, filepath.Join(f.home, operation["path"].(string)), operation["content"].(string))
				case "invalidate":
					p.InvalidateSettingsCache()
				case "load":
					plugins, err := p.LoadPlugins(ctx, "", false)
					if err != nil {
						t.Fatal(err)
					}
					settings, err := p.InitialSettings()
					if err != nil {
						t.Fatal(err)
					}
					actual := map[string]any{"plugins": plugins, "settings": settings, "base": p.settingsState.GetPluginSettingsBase()}
					if styles != nil {
						all, err := styles.List(ctx, f.n.CWD)
						if err != nil {
							t.Fatal(err)
						}
						projection := []map[string]any{}
						for _, style := range all {
							if style.Source == "plugin" {
								projection = append(projection, map[string]any{"name": style.Name, "description": style.Description, "prompt": style.Prompt, "source": style.Source, "forceForPlugin": style.ForceForPlugin})
							}
						}
						actual["styles"] = projection
					}
					raw, _ := json.Marshal(actual)
					var got, want any
					_ = json.Unmarshal(raw, &got)
					if index >= len(expected.Observations) {
						t.Fatal("missing Source observation")
					}
					_ = json.Unmarshal(expected.Observations[index], &want)
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					if !bytes.Equal(gotJSON, wantJSON) {
						t.Fatalf("operation %d: Go %s Source %s", index, gotJSON, wantJSON)
					}
					index++
				}
			}
			if index != len(expected.Observations) {
				t.Fatal(fmt.Sprintf("Source observations %d consumed %d", len(expected.Observations), index))
			}
		})
	}
}
