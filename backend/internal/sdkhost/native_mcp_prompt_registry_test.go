package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualSourceMCPCommandMergeKeepsLocalCollisionAndMountedProducer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	type command struct{ Name, Body string }
	local := []command{{"mcp__peer__collision", "old local"}, {"sample:skill", "old plugin"}}
	batches := [][]command{{{"mcp__peer__old", "old MCP"}}, {{"mcp__peer__collision", "new MCP loses"}, {"mcp__peer__new", "new MCP"}}, {}}
	convert := func(rows []command) []map[string]string {
		out := []map[string]string{}
		for _, row := range rows {
			out = append(out, map[string]string{"name": row.Name, "body": row.Body})
		}
		return out
	}
	input := []any{}
	for _, batch := range batches {
		input = append(input, convert(batch))
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-mcp-command-merge.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	process := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	process.Dir = home
	process.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_COMMAND_MERGE=1", "PATH=" + home}
	process.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "local": convert(local), "mcp": input}))
	var stderr bytes.Buffer
	process.Stderr = &stderr
	output, err := process.Output()
	if err != nil {
		t.Fatal("actual Source command hook", err, string(output), stderr.String())
	}
	var source struct {
		Observations []struct {
			Rows                      []command
			SameLocalObjects          bool
			EmptyReturnsOriginalArray *bool
		}
		Blocked []string
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Blocked) != 0 || len(source.Observations) != len(batches) {
		t.Fatal(err, string(output))
	}
	makeDefinitions := func(rows []command, mcp bool) []skillcommands.Definition {
		out := []skillcommands.Definition{}
		for _, row := range rows {
			metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("prompt")}, protocol.JSONField{Name: "name", Value: quote(row.Name)}, protocol.JSONField{Name: "source", Value: quote("plugin")})
			if mcp {
				metadata.Set("source", quote("mcp"))
				metadata.Set("isMcp", json.RawMessage(`true`))
			}
			body := row.Body
			definition, err := skillcommands.NewNativeCommand(metadata, func(context.Context, *string, skillcommands.Invocation) (string, error) { return body, nil })
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, definition)
		}
		return out
	}
	registry := &PrintRegistry{}
	registry.ReplaceNativePrompts(makeDefinitions(local, false), nil, false)
	for i, batch := range batches {
		registry.ReplaceNativeMCPPrompts(makeDefinitions(batch, true), false)
		got := []command{}
		for _, prompt := range registry.prompts {
			body, err := prompt.Prompt(ctx, nil, skillcommands.Invocation{})
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, command{prompt.Name(), body})
		}
		if !bytes.Equal(raw(got), raw(source.Observations[i].Rows)) || !source.Observations[i].SameLocalObjects || i == len(batches)-1 && (source.Observations[i].EmptyReturnsOriginalArray == nil || !*source.Observations[i].EmptyReturnsOriginalArray) {
			t.Fatal("MCP merge changed Source local identity/order/collision", i, string(raw(got)), string(output))
		}
	}
	t.Log(string(output))
}

func TestMCPPromptPublicationPreservesMountedLocalClosuresAndPluginMetadata(t *testing.T) {
	for _, headless := range []bool{false, true} {
		t.Run(map[bool]string{false: "interactive", true: "headless"}[headless], func(t *testing.T) {
			definition := func(name, source, content string, mcp bool) skillcommands.Definition {
				t.Helper()
				metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("prompt")}, protocol.JSONField{Name: "name", Value: quote(name)}, protocol.JSONField{Name: "source", Value: quote(source)})
				if mcp {
					metadata.Set("isMcp", json.RawMessage(`true`))
				}
				value, err := skillcommands.NewNativeCommand(metadata, func(context.Context, *string, skillcommands.Invocation) (string, error) { return content, nil })
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			// These are actual immutable command producers. An MCP-looking local
			// name and an exact collision must keep their local implementation.
			local := definition("mcp__peer__collision", "userSettings", "mounted local", false)
			plugin := definition("sample:skill", "plugin", "mounted old plugin", false)
			oldMCP := definition("mcp__peer__old", "mcp", "retired MCP", true)
			plugins := []json.RawMessage{json.RawMessage(`{"name":"sample","path":"old-owned-plugin","future":{"keep":true}}`)}
			registry := &PrintRegistry{}
			registry.ReplaceNativePrompts([]skillcommands.Definition{local, plugin, oldMCP}, plugins, headless)
			registry.ReplaceNativeMCPPrompts([]skillcommands.Definition{definition("mcp__peer__collision", "mcp", "must lose to local", true), definition("mcp__peer__new", "mcp", "new MCP", true)}, headless)
			assert := func(name, content string) {
				t.Helper()
				command, ok := registry.Prompt(name)
				if !ok {
					t.Fatal("lost mounted command", name)
				}
				got, err := command.Prompt(context.Background(), nil, skillcommands.Invocation{})
				if err != nil || got != content {
					t.Fatal("command closure changed", name, got, err)
				}
			}
			assert(local.Name(), "mounted local")
			assert(plugin.Name(), "mounted old plugin")
			assert("mcp__peer__new", "new MCP")
			if _, present := registry.Prompt(oldMCP.Name()); present {
				t.Fatal("retired MCP command survived")
			}
			if !bytes.Equal(raw(registry.Catalog().Plugins), raw(plugins)) {
				t.Fatal("MCP callback rebuilt unrelated plugin metadata")
			}
			registry.ReplaceNativeMCPPrompts(nil, headless)
			assert(local.Name(), "mounted local")
			assert(plugin.Name(), "mounted old plugin")
			if _, present := registry.Prompt("mcp__peer__new"); present {
				t.Fatal("empty MCP catalog retained real MCP command")
			}
		})
	}
}
