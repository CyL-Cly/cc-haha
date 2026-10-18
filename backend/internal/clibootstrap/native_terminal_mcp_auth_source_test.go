package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func runActualMCPAuthenticationSource(t *testing.T, x *terminalMCPAuthFixture, mode string) []byte {
	return runActualMCPAuthenticationSourceWithInput(t, x, mode, nil)
}
func runActualMCPAuthenticationSourceWithInput(t *testing.T, x *terminalMCPAuthFixture, mode string, extra map[string]any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Bun not configured")
	}
	home := filepath.Join(x.f.home, "source-ui")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	x.f.write(t, filepath.Join(home, ".claude", ".credentials.json"), `{"future":{"keep":"owned-source-ui"}}`)
	x.f.write(t, filepath.Join(home, "private-browser"), "#!/bin/sh\nprintf '%s' \"$1\" > \"$AUTH_BROWSER_RECEIPT\"\n")
	if err := os.Chmod(filepath.Join(home, "private-browser"), 0700); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-mcp-menu-auth-ui.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + x.f.home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_MCP_AUTH_UI_SOURCE=1", "BROWSER=" + filepath.Join(home, "private-browser"), "AUTH_BROWSER_RECEIPT=" + filepath.Join(home, "browser-url"), "NODE_ENV=production", "USER=owned-source-user", "PATH=" + home, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input := map[string]any{"home": home, "url": x.peer.URL + "/mcp", "port": x.port, "mode": mode}
	for key, value := range extra {
		input[key] = value
	}
	command.Stdin = bytes.NewReader(rawMain(input))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("whole original MCP auth UI: %v\n%s\n%s", err, output, stderr.Bytes())
	}
	return output
}

func TestActualWholeMCPSettingsOAuthManualClearAndUnmountConsumer(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	output := runActualMCPAuthenticationSource(t, x, "menu")
	var observed struct {
		Completions []string `json:"completions"`
		Connected   struct {
			Tools []string `json:"tools"`
		} `json:"connected"`
		Cleared struct {
			Tools   []string `json:"tools"`
			Clients []struct {
				Type string `json:"type"`
			} `json:"clients"`
		} `json:"cleared"`
		ListenerClosed bool `json:"listenerClosed"`
	}
	if err := json.Unmarshal(output, &observed); err != nil {
		t.Fatal("invalid Source UI receipt", string(output), err)
	}
	if len(observed.Completions) != 2 || observed.Completions[0] != "Authentication successful. Connected to auth." || observed.Completions[1] != "Authentication cleared for auth." || len(observed.Connected.Tools) != 1 || observed.Connected.Tools[0] != "mcp__auth__inspect" || len(observed.Cleared.Tools) != 0 || len(observed.Cleared.Clients) != 1 || observed.Cleared.Clients[0].Type != "failed" || !observed.ListenerClosed || x.tokens.Load() != 1 || x.revokes.Load() != 2 || x.connects.Load() != 1 {
		t.Fatal("actual Source UI/HTTP outcome differs", string(output), x.tokens.Load(), x.revokes.Load(), x.connects.Load())
	}
	t.Log(string(output))
}

func TestActualSourceMCPAuthenticationToolDiscoverySchemaURLAndBackgroundCatalog(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	output := runActualMCPAuthenticationSource(t, x, "auth-tool")
	var source struct {
		Parsed, Permission, Mapped, Data json.RawMessage
		Name, Description, Prompt        string
		ConcurrencySafe, ReadOnly        bool
		Threshold                        int
		Connected                        struct {
			Tools   []string
			Clients []struct{ Name, Type string }
		}
		Unsupported []struct{ Status, Message string }
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if string(source.Parsed) != "{}" || source.Name != "mcp__auth__authenticate" || source.ConcurrencySafe || source.ReadOnly || source.Threshold != 10000 || len(source.Connected.Tools) != 1 || source.Connected.Tools[0] != "mcp__auth__inspect" || len(source.Connected.Clients) != 1 || source.Connected.Clients[0].Type != "connected" || len(source.Unsupported) != 2 || source.Unsupported[0].Status != "unsupported" || source.Unsupported[0].Message != `This is a claude.ai MCP connector. Ask the user to run /mcp and select "auth" to authenticate.` || source.Unsupported[1].Status != "unsupported" || source.Unsupported[1].Message != `Server "auth" uses stdio transport which does not support OAuth from this tool. Ask the user to run /mcp and authenticate manually.` {
		t.Fatal("actual original authentication tool outcome differs", string(output))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		definitions, tools, err := x.m.mcpTools.Catalog(scope)
		if err != nil {
			return err
		}
		for index, definition := range definitions {
			if definition.Name != source.Name {
				continue
			}
			var description, prompt string
			if json.Unmarshal(definition.Description, &description) != nil || json.Unmarshal(definition.Prompt, &prompt) != nil || description != source.Description || prompt != source.Prompt {
				return fmt.Errorf("actual initial auth description differs from Source: %s", definition.Description)
			}
			tool := tools[index]
			parsed, err := tool.ParseInput(protocol.NewJSONObject(protocol.JSONField{Name: "dropped", Value: quoteMain("Source empty object strips extras")}))
			if err != nil || len(parsed.OwnProperties()) != 0 || tool.IsConcurrencySafe(parsed) || tool.PersistenceThreshold != float64(source.Threshold) {
				return fmt.Errorf("actual auth parse/concurrency/result boundary differs: %v", err)
			}
			mapped, err := tool.MapResultJSON(source.Data, quoteMain("owned-auth-tool-id"))
			if err != nil {
				return err
			}
			var expected protocol.JSONObject
			if err := expected.UnmarshalJSON(source.Mapped); err != nil {
				return err
			}
			for _, name := range []string{"type", "tool_use_id", "content"} {
				if !bytes.Equal(mapped.Get(name), expected.Get(name)) {
					return fmt.Errorf("actual Source tool result mapping differs for %s", name)
				}
			}
			return nil
		}
		return fmt.Errorf("actual native discovery omitted initial Source auth tool")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(x.browser); !os.IsNotExist(err) || x.tokens.Load() != 1 || x.connects.Load() != 1 || x.calls.Load() != 0 || x.models.Load() != 0 {
		t.Fatal("Source skipBrowser/background physical counts", err, x.tokens.Load(), x.connects.Load(), x.calls.Load(), x.models.Load())
	}
	t.Log(string(output))
}

func TestActualSourceMCPAuthenticationToolPendingCallbackDoesNotHoldProcessEOF(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	output := runActualMCPAuthenticationSource(t, x, "auth-tool-pending")
	var observed struct {
		Pending bool
		Data    struct {
			Status, AuthURL string `json:",omitempty"`
		}
		Connected struct{ Tools []string }
	}
	if err := json.Unmarshal(output, &observed); err != nil {
		t.Fatal(err, string(output))
	}
	if !observed.Pending || observed.Data.Status != "auth_url" || len(observed.Connected.Tools) != 1 || observed.Connected.Tools[0] != "mcp__auth__authenticate" || x.tokens.Load() != 0 || x.connects.Load() != 0 {
		t.Fatal("actual Source pending flow did not exit naturally", string(output), x.tokens.Load(), x.connects.Load())
	}
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", x.port), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("Source exited but owned callback port survived")
	}
	t.Log(string(output))
}
