package computerusemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func cuSourcePeer(t *testing.T, input any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source MCP fixture requires pinned Bun")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-mcp-server.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CU_MCP_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	data, _ := json.Marshal(input)
	child.Stdin = bytes.NewReader(data)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source SDK peer", err, string(output))
	}
	return output
}
func TestActualSourceWindowsDefinitionsAndBothPlatformUnboundStdio(t *testing.T) {
	requests := []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"private-cu-consumer","version":"1.0"}}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"screenshot","arguments":{}}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":4,"method":"ping"}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":5,"method":"missing/method","params":{}}`),
	}
	for _, platform := range []string{"win32", "darwin"} {
		for _, disabled := range []bool{false, true} {
			t.Run(platform+map[bool]string{false: "-enabled", true: "-disabled"}[disabled], func(t *testing.T) {
				output := cuSourcePeer(t, map[string]any{"requests": requests, "platform": platform, "disabled": disabled})
				var source struct {
					Responses   []protocol.JSONObject
					Definitions map[string][]protocol.JSONObject
				}
				if err := json.Unmarshal(output, &source); err != nil {
					t.Fatal(err, string(output))
				}
				for _, mode := range []string{"pixels", "normalized_0_100"} {
					actual, err := WindowsDefinitions(mode)
					if err != nil || len(actual) != 22 {
						t.Fatal(err, len(actual))
					}
					a, _ := json.Marshal(actual)
					b, _ := json.Marshal(source.Definitions[mode])
					var av, bv any
					_ = json.Unmarshal(a, &av)
					_ = json.Unmarshal(b, &bv)
					if !reflect.DeepEqual(av, bv) {
						t.Fatal("full original Windows tool definition changed", mode, string(a), string(b))
					}
				}
				root := t.TempDir()
				s, err := computeruse.New(computeruse.Options{HomeDir: root, ConfigDir: filepath.Join(root, ".claude"), WorkDir: root, TempDir: root, Platform: platform, Environment: map[string]string{"HOME": root, "USERPROFILE": root, "CLAUDE_CONFIG_DIR": filepath.Join(root, ".claude")}})
				if err != nil {
					t.Fatal(err)
				}
				patch, _ := json.Marshal(map[string]bool{"enabled": !disabled})
				if err = s.UpdateConfig(context.Background(), patch); err != nil {
					t.Fatal(err)
				}
				var input strings.Builder
				for _, row := range requests {
					input.Write(row)
					input.WriteByte('\n')
				}
				var actual bytes.Buffer
				if err = RunStdio(context.Background(), StdioOptions{Service: s, Platform: platform, Input: strings.NewReader(input.String()), Output: &actual}); err != nil {
					t.Fatal(err)
				}
				goRows := []protocol.JSONObject{}
				for _, line := range strings.Split(strings.TrimSpace(actual.String()), "\n") {
					var row protocol.JSONObject
					if err = row.UnmarshalJSON([]byte(line)); err != nil {
						t.Fatal(err, line)
					}
					goRows = append(goRows, row)
				}
				sort.Slice(goRows, func(i, j int) bool { return string(goRows[i].Get("id")) < string(goRows[j].Get("id")) })
				sort.Slice(source.Responses, func(i, j int) bool {
					return string(source.Responses[i].Get("id")) < string(source.Responses[j].Get("id"))
				})
				g, _ := json.Marshal(goRows)
				want, _ := json.Marshal(source.Responses)
				var gv, wv any
				_ = json.Unmarshal(g, &gv)
				_ = json.Unmarshal(want, &wv)
				if !reflect.DeepEqual(gv, wv) {
					t.Fatalf("actual Source MCP=%s\nGo=%s", want, g)
				}
			})
		}
	}
}
func TestWindowsBoundMCPStripsMetadataAndObeysDisabledClosedState(t *testing.T) {
	root := t.TempDir()
	s, err := computeruse.New(computeruse.Options{HomeDir: root, ConfigDir: filepath.Join(root, ".claude"), WorkDir: root, TempDir: root, Platform: "win32", Environment: map[string]string{"HOME": root}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	tools, err := computeruse.NewWindowsTools(computeruse.WindowsToolsOptions{Native: computeruse.NativeToolsOptions{Service: s, SessionID: "actual-bound-sid", NativeCall: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("bad_args dispatched helper")
		return nil, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWindows(tools)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	catalog, err := b.Tools(context.Background())
	if err != nil || len(catalog) != 22 {
		t.Fatal(err, len(catalog))
	}
	result := b.Call(context.Background(), "left_click", json.RawMessage(`{"coordinate":[-1,2]}`))
	raw, _ := json.Marshal(result)
	if !result.IsError || bytes.Contains(raw, []byte("telemetry")) || bytes.Contains(raw, []byte("nativeCallNotDispatched")) || bytes.Contains(raw, []byte("screenshot")) {
		t.Fatal(string(raw))
	}
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	catalog, err = b.Tools(context.Background())
	if err != nil || len(catalog) != 0 {
		t.Fatal(err, catalog)
	}
	if result = b.Call(context.Background(), "screenshot", json.RawMessage(`{}`)); !result.IsError {
		t.Fatal(result)
	}
	if err = b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result = b.Call(context.Background(), "screenshot", json.RawMessage(`{}`)); !result.IsError || result.Content[0].Text != "Computer Use cancelled before dispatch." {
		t.Fatal(result)
	}
}
