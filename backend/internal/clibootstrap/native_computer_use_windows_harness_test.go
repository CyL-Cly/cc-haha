package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func emitWindowsCUNativeModel(w http.ResponseWriter, round int, name string, args any) {
	if name == "" {
		emitPlanFixture(w, round, "", "actual Windows tool consumer completed")
		return
	}
	input, _ := json.Marshal(args)
	part, _ := json.Marshal(string(input))
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"actual-windows-cu-model\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
	fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"windows-cu-%d\",\"name\":%q,\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", round, name, part)
}
func TestActualWindowsComputerUseSharedMainPrintTwoTurnsNoMacWorker(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "loopback-cu-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	s, err := computeruse.New(computeruse.Options{HomeDir: f.home, ConfigDir: f.config, WorkDir: f.cwd, TempDir: f.n.Temp, Platform: "win32", Environment: f.n.Environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if err = s.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"clipboardRead":false,"clipboardWrite":false,"systemKeyCombos":false}}`)); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1200; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	var encoded bytes.Buffer
	if err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	shot := base64.StdEncoding.EncodeToString(encoded.Bytes())
	marker := filepath.Join(f.cwd, "windows-native-input.txt")
	var mu sync.Mutex
	calls := []string{}
	clipboard := "owned original clipboard"
	requests := 0
	options := &NativeComputerUseOptions{Service: s, Executable: filepath.Join(f.home, "no-mac-kernel"), RuntimeOptions: &curepl.RuntimeOptions{Executable: filepath.Join(f.home, "must-not-start-js-worker"), Platform: "win32"}, WindowsOptions: &computeruse.WindowsToolsOptions{Sleep: func(context.Context, time.Duration) error { return nil }}}
	options.NativeCall = func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, name)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(args)
		switch name {
		case "get_display_size":
			return json.RawMessage(`{"width":1200,"height":800,"scaleFactor":1,"originX":0,"originY":0,"displayId":1,"label":"Owned test display"}`), nil
		case "resolve_prepare_capture", "screenshot":
			raw, _ := json.Marshal(map[string]any{"base64": shot, "width": 1200, "height": 800, "displayWidth": 1200, "displayHeight": 800, "originX": 0, "originY": 0, "displayId": 1})
			return raw, nil
		case "list_displays":
			return json.RawMessage(`[{"width":1200,"height":800,"scaleFactor":1,"originX":0,"originY":0,"displayId":1,"label":"Owned test display"}]`), nil
		case "frontmost_app":
			return json.RawMessage(`{"bundleId":"owned.fixture","displayName":"Owned fixture","pid":1234}`), nil
		case "prepare_for_action":
			return json.RawMessage(`[]`), nil
		case "read_clipboard":
			raw, _ := json.Marshal(clipboard)
			return raw, nil
		case "write_clipboard":
			clipboard = input.String("text")
			return json.RawMessage(`null`), nil
		case "key":
			if input.String("keySequence") == "ctrl+v" {
				if err := os.WriteFile(marker, []byte(clipboard), 0600); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`null`), nil
		case "type":
			if err := os.WriteFile(marker, []byte(input.String("text")), 0600); err != nil {
				return nil, err
			}
			return json.RawMessage(`null`), nil
		default:
			return nil, fmt.Errorf("unexpected native platform action %s", name)
		}
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		round := requests
		mu.Unlock()
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(body)
		var tools []protocol.JSONObject
		_ = json.Unmarshal(request.Get("tools"), &tools)
		cuCount := 0
		for _, tool := range tools {
			if strings.HasPrefix(tool.String("name"), "mcp__computer-use__") {
				cuCount++
				if tool.String("name") == "mcp__computer-use__js" || tool.String("name") == "mcp__computer-use__js_reset" {
					t.Error("Windows inherited macOS JS face")
				}
			}
		}
		if cuCount != 22 {
			t.Errorf("actual model-visible Windows tools=%d", cuCount)
		}
		if round == 2 || round == 4 || round == 7 {
			if !bytes.Contains(body, []byte(`"type":"image"`)) {
				t.Error("physical screenshot did not reach next model consumer")
			}
		}
		if round == 3 && !bytes.Contains(body, []byte("Typed (via clipboard).")) {
			t.Error("real Windows Unicode input result missing")
		}
		if round == 6 && !bytes.Contains(body, []byte("Typed 29 grapheme(s).")) {
			t.Error("actual whole Windows input receipt missing from next model consumer")
		}
		switch round {
		case 1, 3, 6:
			emitWindowsCUNativeModel(w, round, "mcp__computer-use__screenshot", map[string]any{})
		case 2:
			emitWindowsCUNativeModel(w, round, "mcp__computer-use__type", map[string]any{"text": "第一轮电脑工具"})
		case 5:
			emitWindowsCUNativeModel(w, round, "mcp__computer-use__type", map[string]any{"text": "CU_WINDOWS_NATIVE_TWO_TURN_OK"})
		case 4, 7:
			emitWindowsCUNativeModel(w, round, "", nil)
		default:
			t.Errorf("unexpected upstream request %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	m, err := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "actual shared Windows CU fixture", Tools: []string{"Read,Write"}, SessionID: "22222222-2222-4222-8222-222222222222", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "windows", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 2; turn++ {
		var input protocol.JSONObject
		_ = input.UnmarshalJSON([]byte(fmt.Sprintf(`{"type":"user","uuid":"windows-turn-%d","message":{"role":"user","content":"operate owned Windows native fixture"}}`, turn)))
		if err = m.RunTurn(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(f.config, "computer-use.lock")); !os.IsNotExist(err) {
			t.Fatal("completed turn retained lock", turn, err)
		}
	}
	physical, err := os.ReadFile(marker)
	if err != nil || string(physical) != "CU_WINDOWS_NATIVE_TWO_TURN_OK" {
		t.Fatal("native OS primitive physical result", err, string(physical))
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 7 || clipboard != "owned original clipboard" {
		t.Fatal("actual model or clipboard restoration", requests, clipboard, calls)
	}
}
