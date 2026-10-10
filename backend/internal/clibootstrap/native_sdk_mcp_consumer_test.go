package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sdkMCPRootPeer struct {
	mu             sync.Mutex
	input          *io.PipeWriter
	file           string
	frames         []protocol.JSONObject
	methods        []string
	calls          int
	failure        error
	ack            chan struct{}
	status         chan protocol.JSONObject
	failInitialize bool
}

func (p *sdkMCPRootPeer) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var frame protocol.JSONObject
	if e := frame.UnmarshalJSON(bytes.TrimSpace(data)); e != nil {
		return 0, e
	}
	p.frames = append(p.frames, frame.Clone())
	if frame.String("type") == "control_response" {
		r, _ := frame.Object("response")
		if r.String("request_id") == "sdk-status-P711" && p.status != nil {
			p.status <- r.Clone()
		}
		if r.String("request_id") == "sdk-root-init-P684" {
			select {
			case p.ack <- struct{}{}:
			default:
			}
		}
	}
	request, _ := frame.Object("request")
	if frame.String("type") == "control_request" && request.String("subtype") == "mcp_message" {
		message, _ := request.Object("message")
		method := message.String("method")
		p.methods = append(p.methods, method)
		var result any = map[string]any{}
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "actual private SDK filesystem reader", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "read_sdk", "description": "Read the private physical SDK acceptance file", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			p.calls++
			physical, e := os.ReadFile(p.file)
			if e != nil {
				p.failure = e
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(physical)}}}
		}
		response := map[string]any{}
		if message.Get("id") != nil {
			response["mcp_response"] = map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.Get("id")), "result": result}
		}
		envelope := map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": response}
		if p.failInitialize && method == "initialize" {
			envelope = map[string]any{"subtype": "error", "request_id": frame.String("request_id"), "error": "physical SDK initialize rejected"}
		}
		reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": envelope})
		go func() { _, _ = p.input.Write(append(reply, '\n')) }()
	}
	if frame.String("type") == "control_request" && request.String("subtype") == "can_use_tool" {
		if request.String("tool_name") != "mcp__sdk-p684__read_sdk" {
			p.failure = fmt.Errorf("unexpected permission tool %s", request.String("tool_name"))
		}
		reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}}})
		go func() { _, _ = p.input.Write(append(reply, '\n')) }()
	}
	if frame.String("type") == "result" {
		_ = p.input.Close()
	}
	return len(data), nil
}
func TestActualDefaultSDKMCPInitializeACKThenFirstASKToolAndPhysicalResult(t *testing.T) {
	runSDKMCPConsumer(t, false)
}
func TestActualDefaultSDKMCPStdioASKApprovalThenPhysicalResult(t *testing.T) {
	runSDKMCPConsumer(t, true)
}
func runSDKMCPConsumer(t *testing.T, stdio bool) {
	f := worktreeMainFixture(t)
	file := filepath.Join(f.home, "sdk-root-physical-P684.txt")
	if e := os.WriteFile(file, []byte("SDK_ROOT_PHYSICAL_ONLY_P684_7249\n"), 0600); e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{"permissions":{"allow":["mcp__sdk-p684__read_sdk"]}}`)
	if stdio {
		f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	}
	var lock sync.Mutex
	var requests [][]byte
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lock.Lock()
		requests = append(requests, body)
		n := len(requests)
		lock.Unlock()
		if n == 1 {
			if !bytes.Contains(body, []byte(`"name":"mcp__sdk-p684__read_sdk"`)) {
				t.Error("SDK ACK did not lead to actual first ASK tool catalogue")
			}
			emitWorktreeModel(w, n, "mcp__sdk-p684__read_sdk", `{}`, "")
		} else {
			emitWorktreeModel(w, n, "", "", "SDK_FIRST_ASK_P684_OK")
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := &sdkMCPRootPeer{input: writer, file: file, ack: make(chan struct{}, 1)}
	disabled := false
	var diagnostics bytes.Buffer
	permissionTool := ""
	if stdio {
		permissionTool = "stdio"
	}
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: permissionTool, ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	if m.sdkMCPState == nil {
		t.Fatal("actual constructor SDK state absent")
	}
	if m.options.DetachedAgent != nil {
		t.Fatalf("unexpected detached root %#v", m.options.DetachedAgent)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Run(ctx); done <- err }()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"sdk-root-init-P684","request":{"subtype":"initialize","sdkMcpServers":["sdk-p684"]}}` + "\n"))
	}()
	select {
	case <-peer.ack:
	case err := <-done:
		t.Fatal("run closed before ACK", err, diagnostics.String())
	case <-ctx.Done():
		t.Fatal("no SDK initialize ACK")
	}
	var own, host protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		own, e = m.sdkMCPState.Configurations(scope)
		if e != nil {
			return e
		}
		host, e = m.Host.NativeSDKMCPConfigurations(scope)
		return e
	})
	if err != nil {
		t.Fatal("actual SDK snapshot admission", err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal("actual SDK snapshot", err)
	}
	if own.Get("sdk-p684") == nil || host.Get("sdk-p684") == nil {
		t.Fatal("ACK without registered shared state", rawMain(own), rawMain(host))
	}
	go func() {
		_, _ = writer.Write([]byte(`{"type":"user","message":{"role":"user","content":"Read the actual SDK MCP physical input through its tool"}}` + "\n"))
	}()
	if e = <-done; e != nil {
		t.Fatal("actual SDK owner/ASK consumer", e, diagnostics.String())
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if peer.failure != nil || peer.calls != 1 || len(requests) != 2 {
		summary := []string{}
		for _, x := range peer.frames {
			r, _ := x.Object("response")
			q, _ := x.Object("request")
			summary = append(summary, x.String("type")+":"+q.String("subtype")+":"+r.String("request_id")+":"+r.String("subtype"))
		}
		t.Fatal("physical SDK consumer cardinality", peer.failure, peer.calls, len(requests), peer.methods, summary, diagnostics.String())
	}
	if !bytes.Contains(requests[1], []byte("SDK_ROOT_PHYSICAL_ONLY_P684_7249")) {
		t.Fatal("actual physical MCP result absent from the same Harness next request")
	}
	if stdio {
		approvals := 0
		for _, frame := range peer.frames {
			request, _ := frame.Object("request")
			if request.String("subtype") == "can_use_tool" {
				approvals++
			}
		}
		if approvals != 1 {
			t.Fatal("default ASK approval not delivered exactly once", approvals)
		}
	}
	ack, firstMCP := -1, -1
	for i, x := range peer.frames {
		if x.String("type") == "control_response" {
			r, _ := x.Object("response")
			if r.String("request_id") == "sdk-root-init-P684" && r.String("subtype") == "success" {
				ack = i
			}
		}
		r, _ := x.Object("request")
		if firstMCP < 0 && r.String("subtype") == "mcp_message" {
			firstMCP = i
		}
	}
	if ack < 0 || firstMCP <= ack {
		t.Fatal("handshake preceded original initialize ACK", ack, firstMCP)
	}
}
