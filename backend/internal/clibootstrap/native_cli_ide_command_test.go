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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/coder/websocket"
)

func TestIDEChooserActualSourceConfigAndJoinedPhysicalOwnerSelectCancelDisconnect(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	var mu sync.Mutex
	connections := 0
	closes := make(chan struct{}, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
		if e != nil {
			return
		}
		defer conn.CloseNow()
		mu.Lock()
		connections++
		mu.Unlock()
		for {
			_, data, e := conn.Read(r.Context())
			if e != nil {
				closes <- struct{}{}
				return
			}
			var input protocol.JSONObject
			_ = input.UnmarshalJSON(data)
			if input.Get("id") == nil {
				continue
			}
			result := json.RawMessage(`{}`)
			switch input.String("method") {
			case "initialize":
				result = []byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"real-choice-peer","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[{"name":"getDiagnostics","inputSchema":{"type":"object"},"description":"actual diagnostics"}]}`)
			}
			reply := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result})
			if e := conn.Write(r.Context(), websocket.MessageText, rawMain(reply)); e != nil {
				return
			}
		}
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		t.Error("IDE local selection must not start model")
		w.WriteHeader(500)
	}))
	defer model.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(peer.URL, "http://127.0.0.1:"))
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
		path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "ide", strconv.Itoa(port)+".lock")
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if e := os.WriteFile(path, rawMain(map[string]any{"workspaceFolders": []string{n.CWD}, "pid": os.Getpid(), "transport": "ws", "ideName": "Actual IDE 中文", "authToken": "private-loopback-token"}), 0600); e != nil {
			t.Fatal(e)
		}
	}, func(o *MainPrintOptions) { o.Interactive = true; o.Arguments.IDE = false; o.Error = io.Discard })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	props, e := m.NativeIDECommand(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(props.Options) != 2 || !props.AskAutoConnect {
		t.Fatal("actual selection/default preference missing", props)
	}
	notice, e := m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Cancelled: true})
	if e != nil || notice != "IDE selection cancelled" {
		t.Fatal(notice, e)
	}
	mu.Lock()
	count := connections
	mu.Unlock()
	if count != 0 {
		t.Fatal("cancel established IDE connection")
	}
	props, e = m.NativeIDECommand(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	disabled := false
	notice, e = m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Value: strconv.Itoa(port), AutoConnect: &disabled})
	if e != nil || notice != "Connected to Actual IDE 中文." {
		t.Fatal(notice, e)
	}
	var config protocol.JSONObject
	e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		config = nativeInProcessObject(m.nativeDynamicMCP.Get("ide"))
		_, tools, e := m.mcpTools.Catalog(scope)
		if len(tools) != 1 || tools[0].Name != "mcp__ide__getDiagnostics" {
			t.Error("same Owner catalogue not updated", tools)
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	global, e := m.Global.Get()
	if e != nil || string(global.Get("autoConnectIde")) != "false" || string(global.Get("hasIdeAutoConnectDialogBeenShown")) != "true" {
		t.Fatal("actual preference persistence missing", global, e)
	}
	ide := ideintegration.IDE{Name: "Actual IDE 中文", URL: "ws" + strings.TrimPrefix(peer.URL, "http"), Port: float64(port), Valid: true, WorkspaceFolders: []string{m.options.Native.CWD}, RunningInWindows: false, AuthToken: "private-loopback-token"}
	ideRaw := nativeInProcessObject(rawMain(ide))
	ideRaw.Set("authToken", quoteMain(ide.AuthToken))
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-ide-command.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "cases": []map[string]any{{"config": map[string]any{}, "selected": ideRaw}, {"config": map[string]any{"ide": config}, "current": ideRaw}}}))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var rows [][]protocol.JSONObject
	if e = json.Unmarshal(out, &rows); e != nil {
		t.Fatal(e, string(out))
	}
	var sourceCfg protocol.JSONObject
	for _, event := range rows[0] {
		if event.Get("config") != nil {
			sourceCfg = nativeInProcessObject(nativeInProcessObject(event.Get("config")).Get("ide"))
		}
	}
	if !bytes.Equal(rawMain(sourceCfg), rawMain(config)) {
		t.Fatal("selected Source dynamic config differs", string(rawMain(sourceCfg)), string(rawMain(config)))
	}
	props, e = m.NativeIDECommand(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	notice, e = m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Value: "None"})
	if e != nil || notice != rows[1][len(rows[1])-1].String("notice") {
		t.Fatal("Source disconnect notice differs", notice, e, string(out))
	}
	select {
	case <-closes:
	case <-ctx.Done():
		t.Fatal("IDE disconnect did not close physical transport")
	}
	e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, e := m.mcpTools.Catalog(scope)
		if len(tools) != 0 || m.nativeDynamicMCP.Get("ide") != nil {
			t.Error("disconnected same Owner retained IDE actions", tools)
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Value: strconv.Itoa(port)}); e == nil {
		t.Fatal("stale callback reconnected")
	}
}
