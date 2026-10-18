package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestNativeLSPInstalledConfigurationMatchesWholeOriginalPluginProducer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	for _, name := range []string{"default", "environment", "saved-options", "raw-options", "unconfigured-placeholder", "missing-option-plugin-isolation", "manifest-overrides-file", "socket-declaration", "disabled-plugin"} {
		t.Run(name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.n.Environment["USER_TYPE"] = "external"
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-only-lsp-configuration"
			f.n.Environment["CC_HAHA_LSP_COMMAND"] = "language-server"
			f.n.Environment["CC_HAHA_LSP_EMPTY"] = ""
			f.n.Environment["CC_HAHA_LSP_VALUE"] = "actual $& $` $' dollar\n中文"
			if name == "environment" || name == "missing-option-plugin-isolation" {
				f.n.Environment["DEBUG"] = "1"
				f.n.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"] = filepath.Join(f.home, "native-lsp-debug.log")
			}
			manifest := protocol.NewJSONObject(fieldMain("name", "sample"), fieldMain("version", "1.0.0"))
			settings := protocol.NewJSONObject(fieldMain("enabledPlugins", map[string]bool{"sample@mkt": name != "disabled-plugin"}), fieldMain("future", map[string]bool{"keep": true}))
			server := protocol.NewJSONObject(fieldMain("command", "private-language-server"), fieldMain("extensionToLanguage", map[string]string{".txt": "plaintext"}))
			if name == "environment" {
				server.Set("command", quoteMain("${CLAUDE_PLUGIN_ROOT}/bin/${CC_HAHA_LSP_COMMAND}"))
				server.Set("args", rawMain([]string{"${CC_HAHA_LSP_VALUE}", "${CC_HAHA_LSP_EMPTY:-fallback}", "${CC_HAHA_LSP_UNDEFINED:-fallback}", "${CC_HAHA_LSP_MISSING}", "${CLAUDE_PLUGIN_DATA}/actual"}))
				server.Set("workspaceFolder", quoteMain("${CLAUDE_PLUGIN_ROOT}/workspace/${CC_HAHA_LSP_UNDEFINED:-workspace}/${CC_HAHA_LSP_WORKSPACE_MISSING}"))
				server.Set("env", rawMain(map[string]string{"VALUE": "${CC_HAHA_LSP_VALUE}", "MISSING": "${CC_HAHA_LSP_MISSING}", "CLAUDE_PLUGIN_ROOT": "original ${UNEXPANDED}", "CLAUDE_PLUGIN_DATA": "reserved ${UNEXPANDED}"}))
			}
			withOptions := name == "saved-options" || name == "raw-options" || name == "missing-option-plugin-isolation"
			if withOptions {
				manifest.Set("userConfig", json.RawMessage(`{"label":{"type":"string","title":"Language server label","description":"Private fixture label"}}`))
				options := json.RawMessage(`{"label":"actual $& $' $` + "`" + ` 中文"}`)
				if name == "raw-options" {
					options = json.RawMessage(`{"label":"high\ud800 low\udfff"}`)
				}
				if name == "missing-option-plugin-isolation" {
					options = json.RawMessage(`{}`)
				}
				settings.Set("pluginConfigs", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "sample@mkt", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "options", Value: options}))})))
				server.Set("args", json.RawMessage(`["${user_config.label}","${CLAUDE_PLUGIN_DATA}"]`))
				server.Set("env", json.RawMessage(`{"LABEL":"${user_config.label}"}`))
				server.Set("workspaceFolder", quoteMain("${CLAUDE_PLUGIN_ROOT}/${user_config.label}"))
			}
			if name == "unconfigured-placeholder" {
				server.Set("args", json.RawMessage(`["${user_config.label}"]`))
			}
			if name == "socket-declaration" {
				server.Set("transport", quoteMain("socket"))
				server.Set("initializationOptions", json.RawMessage(`{"physical":true,"nested":[null,1,"中文"]}`))
				server.Set("settings", json.RawMessage(`{"server":{"enabled":false}}`))
				server.Set("startupTimeout", json.RawMessage(`1234`))
				server.Set("maxRestarts", json.RawMessage(`0`))
			}
			if name == "manifest-overrides-file" {
				override := server.Clone()
				override.Set("command", quoteMain("actual-manifest-winner"))
				manifest.Set("lspServers", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "fixture", Value: rawMain(override)})))
			}
			f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
			f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), string(rawMain(manifest)))
			f.write(t, filepath.Join(f.plugin, ".lsp.json"), string(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "fixture", Value: rawMain(server)}))))
			installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			if err = installed.Add(context.Background(), "sample@mkt", "user", f.plugin, manifest, nil); err != nil {
				t.Fatal(err)
			}
			if name == "missing-option-plugin-isolation" {
				other := filepath.Join(f.market, "plugins", "other")
				otherManifest := mainObj(json.RawMessage(`{"name":"other","version":"1.0.0"}`))
				f.write(t, filepath.Join(other, ".claude-plugin", "plugin.json"), string(rawMain(otherManifest)))
				f.write(t, filepath.Join(other, ".lsp.json"), `{"healthy":{"command":"actual-healthy-server","extensionToLanguage":{".other":"other"}}}`)
				f.write(t, filepath.Join(f.market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"},{"name":"other","source":"./plugins/other"}]}`)
				settings.Set("enabledPlugins", rawMain(map[string]bool{"sample@mkt": true, "other@mkt": true}))
				f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(settings)))
				if err = installed.Add(context.Background(), "other@mkt", "user", other, otherManifest, nil); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-lsp-configuration.fixture.ts")
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = f.home
			command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_LSP_CONFIGURATION_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-only-lsp-configuration", "PATH=/usr/bin:/bin"}
			if name == "environment" || name == "missing-option-plugin-isolation" {
				command.Env = append(command.Env, "DEBUG=1", "CLAUDE_CODE_DEBUG_LOGS_DIR="+filepath.Join(f.home, "source-lsp-debug.log"))
			}
			keys := []string{"CC_HAHA_LSP_COMMAND", "CC_HAHA_LSP_EMPTY", "CC_HAHA_LSP_VALUE"}
			sort.Strings(keys)
			for _, key := range keys {
				command.Env = append(command.Env, key+"="+f.n.Environment[key])
			}
			command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.n.CWD}))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("actual Source LSP configuration", err, string(output))
			}
			var original struct {
				Servers      protocol.JSONObject
				Errors       []string
				Capabilities struct{ Blocked, SecureReads []string }
			}
			if err = json.Unmarshal(output, &original); err != nil || len(original.Capabilities.Blocked) != 0 || len(original.Capabilities.SecureReads) != map[bool]int{true: 1, false: 0}[withOptions] {
				t.Fatal("actual Source loader/isolated credential primitive", err, string(output))
			}
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{"Read"}, MaxTurns: 1, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rows, err := m.nativeLSPConfigurations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			actual := protocol.NewJSONObject()
			for _, row := range rows {
				actual.Set(row.Name, rawMain(row.Config))
			}
			got, err := protocol.ParseAndStringifyJavaScriptJSON(rawMain(actual))
			if err != nil {
				t.Fatal(err)
			}
			want, err := protocol.ParseAndStringifyJavaScriptJSON(rawMain(original.Servers))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("actual installed LSP configuration differs\nGo=%s\nSource=%s\nSourceObservations=%s", got, want, output)
			}
			if name == "environment" || name == "missing-option-plugin-isolation" {
				warning := " [WARN] Missing environment variables in plugin LSP config: CC_HAHA_LSP_MISSING, CC_HAHA_LSP_WORKSPACE_MISSING\n"
				if name == "missing-option-plugin-isolation" {
					warning = " [ERROR] Failed to load LSP servers for plugin sample: Error: Missing required user configuration value: label. This should have been validated before variable substitution.\n"
				}
				sourceLog, err := os.ReadFile(filepath.Join(f.home, "source-lsp-debug.log"))
				if err != nil || bytes.Count(sourceLog, []byte(warning)) != 1 {
					t.Fatal("actual original missing-env warning premise", err, string(sourceLog))
				}
				m.Close()
				nativeLog, err := os.ReadFile(filepath.Join(f.home, "native-lsp-debug.log"))
				if err != nil || bytes.Count(nativeLog, []byte(warning)) != 1 {
					t.Fatal("native installed configuration omitted the actual original warning", err, string(nativeLog))
				}
			}
			after, err := os.ReadFile(filepath.Join(f.config, "settings.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("configuration inspection rewrote saved plugin options", err)
			}
		})
	}
}
