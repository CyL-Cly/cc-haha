package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
)

func TestActualSourceWindowsHelperEnvelopeRawErrorThroughNativeToolsAndMCP(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []object{}
	for _, detail := range []json.RawMessage{json.RawMessage(`{"message":"\ud800"}`), json.RawMessage(`{"message":"raw\udc00","code":"\ud800"}`), json.RawMessage(`{"message":"","code":"runtime_error"}`), json.RawMessage(`{"message":{"x":"\ud800"},"code":17}`), json.RawMessage(`{"message":["\ud800",true],"code":"runtime_error"}`), json.RawMessage(`{"message":"refused\ud800","code":"user_interference"}`)} {
		for _, tool := range []string{"read_clipboard", "switch_display"} {
			command := "read_clipboard"
			args := json.RawMessage(`{}`)
			if tool == "switch_display" {
				command = "list_displays"
				args = json.RawMessage(`{"display":"Secondary"}`)
			}
			rows = append(rows, object{"command": pixelQuoted(command), "tool": pixelQuoted(tool), "args": args, "envelope": encode(object{"ok": json.RawMessage(`false`), "error": detail})})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/windows_error_utf16.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_WINDOWS_ERROR_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"rows": rows}))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source bridge %v %s", e, out)
	}
	var source []object
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		command, _ := stringValue(row["command"])
		tool, _ := stringValue(row["tool"])
		_, failure := windowsEnvelope(CommandResult{Stdout: string(row["envelope"])}, command)
		if failure == nil || !bytes.Equal(exactPixelJSON(nativeErrorMessageJSON(failure)), exactPixelJSON(source[i]["bridgeMessage"])) {
			t.Fatalf("bridge row%d %v Go %s Source %s", i, failure, nativeErrorMessageJSON(failure), source[i]["bridgeMessage"])
		}
		o := fixtureOptions(t)
		o.Platform = "win32"
		service := fixtureService(t, o)
		if e = service.UpdateConfig(ctx, json.RawMessage(`{"enabled":true}`)); e != nil {
			t.Fatal(e)
		}
		tools, e := NewWindowsTools(WindowsToolsOptions{Native: NativeToolsOptions{Service: service, SessionID: "owned-error", NativeCall: func(_ context.Context, actual string, _ json.RawMessage) (json.RawMessage, error) {
			if actual == command {
				return windowsEnvelope(CommandResult{Stdout: string(row["envelope"])}, actual)
			}
			return json.RawMessage(`null`), nil
		}}})
		if e != nil {
			t.Fatal(e)
		}
		result, e := tools.Call(ctx, tool, row["args"])
		if e != nil {
			t.Fatal(e)
		}
		_ = tools.Close(context.Background())
		if !bytes.Equal(exactPixelJSON(encode(result)), exactPixelJSON(source[i]["result"])) {
			t.Fatalf("native row%d Go %s Source %s", i, encode(result), source[i]["result"])
		}
		// Exact MCP text projection passes through curepl's private UTF16 units.
		mcp := curepl.Result{IsError: result.IsError}
		for _, content := range result.Content {
			var converted curepl.Content
			if e = json.Unmarshal(encode(content), &converted); e != nil {
				t.Fatal(e)
			}
			mcp.Content = append(mcp.Content, converted)
		}
		var projected object
		_ = json.Unmarshal(encode(mcp), &projected)
		if !bytes.Equal(exactPixelJSON(projected["content"]), exactPixelJSON(mustErrorContent(t, source[i]["result"]))) {
			t.Fatalf("MCP UTF16 row%d %s", i, encode(mcp))
		}
	}
}
func mustErrorContent(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value object
	if e := json.Unmarshal(raw, &value); e != nil {
		t.Fatal(e)
	}
	return value["content"]
}
