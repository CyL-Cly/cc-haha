package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestActualPluginAgentMergedOptionsReachChildModelWithoutCodeUnitLoss(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	schema := `{"region":{"type":"string","title":"Region","description":"Region"},"token":{"type":"string","title":"Token","description":"Token","sensitive":true}}`
	settings := `{"enabledPlugins":{"sample@mkt":true},"pluginConfigs":{"sample@mkt":{"options":{"region":"PLAIN_MUST_NOT_WIN","token":"OLD_PLAIN_TOKEN"}}}}`
	credentials := `{"pluginSecrets":{"sample@mkt":{"region":"SECURE_LEFT\ud800RIGHT\udc00","token":"PRIVATE_TOKEN_MUST_NOT_REACH_MODEL"}}}`
	body := "PLUGIN_RAW_AGENT:${user_config.region}:${user_config.token}:${user_config.constructor}"
	content := "---\nname: worker\ndescription: Actual plugin worker\ntools: Read\nmodel: inherit\n---\n" + body
	f.write(t, filepath.Join(f.config, "settings.json"), settings)
	f.write(t, filepath.Join(f.config, ".credentials.json"), credentials)
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0","userConfig":`+schema+`}`)
	f.write(t, filepath.Join(f.plugin, "agents", "worker.md"), content)
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(f.plugin, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest protocol.JSONObject
	if err := manifest.UnmarshalJSON(manifestBytes); err != nil {
		t.Fatal(err)
	}
	manifest.Set("installedAt", quoteMain("2026-10-01T00:00:00.000Z"))
	if err := installed.Add(context.Background(), "sample@mkt", "user", f.plugin, manifest, nil); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(f.cwd, "plugin-agent-read.txt")
	f.write(t, physical, "PLUGIN_AGENT_PHYSICAL_READ")
	// The reference's full loader reads actual private settings and credential
	// files, and calls its returned getSystemPrompt closure. No expected prompt
	// or Source-prepared definition is supplied to the native implementation.
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-plugin.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_PLUGIN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	var parsedSchema protocol.JSONObject
	_ = parsedSchema.UnmarshalJSON([]byte(schema))
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "optionsProfile": true, "settingsSource": settings, "credentialsSource": credentials, "schema": parsedSchema, "pluginID": "sample@mkt", "cases": []map[string]any{{"content": content, "memoryEnabled": false}}}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source plugin Agent", err, string(output))
	}
	var source struct {
		Prompts      []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Prompts) != 1 || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid original Agent receipt", err, string(output))
	}
	want, err := protocol.DecodeJavaScriptJSONString(source.Prompts[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source.Prompts[0], []byte("PLAIN_MUST_NOT_WIN")) || !bytes.Contains(source.Prompts[0], []byte(`SECURE_LEFT\ud800RIGHT\udc00`)) {
		t.Fatal("actual Source did not consume the private secure-store merge", string(source.Prompts[0]))
	}
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-plugin-agent-model-key"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	var mu sync.Mutex
	parent, child := 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request protocol.JSONObject
		if err := request.UnmarshalJSON(data); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if bytes.Contains(request.Get("system"), []byte("PLUGIN_RAW_AGENT:")) {
			child++
			found := false
			for _, block := range taskBlocks(request.Get("system")) {
				units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
				// runAgent's actual caller appends enhanceSystemPromptWithEnvDetails
				// Notes and per-process env after the captured definition prompt.
				// Compare that exact prefix at the known Notes boundary, retaining
				// every UTF-16 unit; no normalization or replacement is allowed.
				separator, _ := protocol.DecodeJavaScriptJSONString(quoteMain("\n\nNotes:\n"))
				for i := 0; i+len(separator) <= len(units); i++ {
					if reflect.DeepEqual(units[i:i+len(separator)], separator) {
						units = units[:i]
						break
					}
				}
				if err == nil && reflect.DeepEqual(units, want) {
					found = true
				}
			}
			if !found {
				t.Error("actual child prompt prefix differs from full Source getSystemPrompt")
			}
			if bytes.Contains(data, []byte("PRIVATE_TOKEN_MUST_NOT_REACH_MODEL")) || bytes.Contains(data, []byte("PLAIN_MUST_NOT_WIN")) {
				t.Error("Source store merge or sensitive filter lost")
			}
			if child == 1 {
				emitNativeTaskTool(w, "plugin-agent-read", "Read", map[string]any{"file_path": physical})
			} else {
				if !bytes.Contains(request.Get("messages"), []byte("PLUGIN_AGENT_PHYSICAL_READ")) {
					t.Error("real child Read result missing")
				}
				emitPlanFixture(w, child, "", "PLUGIN_AGENT_FINISHED")
			}
		} else {
			parent++
			if parent == 1 {
				emitNativeTaskTool(w, "plugin-parent-agent", "Agent", map[string]any{"description": "Use installed plugin worker", "prompt": "Read the actual private file", "subagent_type": "sample:worker"})
			} else {
				if !bytes.Contains(request.Get("messages"), []byte("PLUGIN_AGENT_FINISHED")) {
					t.Error("parent did not consume child result")
				}
				emitPlanFixture(w, parent, "", "PLUGIN_PARENT_FINISHED")
			}
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual private parent", Tools: []string{"Agent,Read"}, Sources: DefaultState(), MaxTurns: 8}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "fixture", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	input := objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use sample:worker to read the physical file"}}))
	if err := m.RunTurn(ctx, input); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if parent != 2 || child != 2 {
		t.Fatal("actual parent and child request counts", parent, child)
	}
}
