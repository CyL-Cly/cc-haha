package agentcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceJSONAgentPromptCapturesUTF16ForCLIAndSDKCatalogs(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	prompts := []json.RawMessage{json.RawMessage(`" \tJSON_AGENT_RAW:\ud800\nend\ufeff "`), json.RawMessage(`"\udfffJSON_AGENT_RAW:low"`), json.RawMessage(`" JSON_AGENT_RAW:\ud83d\ude00 "`), json.RawMessage(`"\ud800"`), json.RawMessage(`" \t\ufeff "`), json.RawMessage(`null`), json.RawMessage(`7`)}
	rows := []map[string]any{}
	definitions := []json.RawMessage{}
	for _, prompt := range prompts {
		definition := protocol.NewJSONObject(protocol.JSONField{Name: "description", Value: json.RawMessage(`"Actual JSON agent"`)}, protocol.JSONField{Name: "prompt", Value: prompt}, protocol.JSONField{Name: "tools", Value: json.RawMessage(`["Read"]`)})
		data, err := definition.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, data)
		rows = append(rows, map[string]any{"name": "raw-worker", "definition": definition, "memory": false})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-json.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_JSON_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows, "runtimePrompts": true})
	child.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows           []json.RawMessage
		RuntimePrompts []json.RawMessage
		Capabilities   struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.RuntimePrompts) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for index, definition := range definitions {
		for _, producer := range []string{"cli", "sdk"} {
			t.Run(producer+"/"+string(rune('a'+index)), func(t *testing.T) {
				f := newFixture(t)
				f.env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
				service := f.service()
				if err := f.owner.Run(ctx, func(scope *cooperative.Scope) error {
					input := protocol.NewJSONObject(protocol.JSONField{Name: "raw-worker", Value: definition})
					encoded, err := input.MarshalJSON()
					if err != nil {
						return err
					}
					if producer == "cli" {
						err = service.InstallCLIJSON(scope, encoded)
					} else {
						err = service.AppendSDKDefinitions(scope, encoded)
					}
					if err != nil {
						return err
					}
					catalog := service.cli
					if producer == "sdk" {
						catalog = service.sdk
					}
					if string(source.Rows[index]) == "null" {
						if len(catalog) != 0 {
							t.Error("Source rejected definition reached native catalog")
						}
						return nil
					}
					if len(catalog) != 1 {
						t.Errorf("Source accepted prompt was dropped by %s catalog: %s", producer, prompts[index])
						return nil
					}
					want, err := protocol.DecodeJavaScriptJSONString(source.RuntimePrompts[index])
					if err != nil {
						return err
					}
					for range 2 {
						got, err := service.PromptJSON(scope, catalog[0], agentbuiltinmetadata.GuideContext{})
						if err != nil {
							return err
						}
						units, err := protocol.DecodeJavaScriptJSONString(got)
						if err != nil || !reflect.DeepEqual(units, want) {
							t.Errorf("Source getSystemPrompt units differ: native %s Source %s error %v", got, source.RuntimePrompts[index], err)
						}
						catalog[0].Fields.Set("rawSystemPrompt", json.RawMessage(`"mutated metadata"`))
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
