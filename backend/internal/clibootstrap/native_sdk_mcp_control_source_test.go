package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The original handleMcpSetServers probe has published pending clients and ACK
// before setupSdkMcpClients. This test reaches the actual public print input and
// next default ASK, preserving the real Owner/permissions/tool catalogue.
func TestActualDefaultSDKMCPSetServersACKThenPhysicalTool(t *testing.T) {
	f := worktreeMainFixture(t)
	file := filepath.Join(f.home, "sdk-control-physical.txt")
	if e := os.WriteFile(file, []byte("SDK_CONTROL_PHYSICAL_ONLY\n"), 0600); e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{"permissions":{"allow":["mcp__sdk-p684__read_sdk"]}}`)
	var requests int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests++
		if requests == 1 {
			if !bytes.Contains(body, []byte(`"name":"mcp__sdk-p684__read_sdk"`)) {
				t.Error("ACK then next ASK lost actual SDK catalogue")
			}
			emitWorktreeModel(w, requests, "mcp__sdk-p684__read_sdk", `{}`, "")
		} else {
			if !bytes.Contains(body, []byte("SDK_CONTROL_PHYSICAL_ONLY")) {
				t.Error("model did not receive physical SDK result")
			}
			emitWorktreeModel(w, requests, "", "", "SDK_SET_CONTROL_OK")
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
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.Run(ctx); done <- e }()
	request := map[string]any{"type": "control_request", "request_id": "sdk-root-init-P684", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"sdk-p684": map[string]any{"type": "sdk", "name": "sdk-p684"}}}}
	data, _ := json.Marshal(request)
	go func() { _, _ = writer.Write(append(data, '\n')) }()
	select {
	case <-peer.ack:
	case e := <-done:
		t.Fatal("actual public mcp_set_servers rejected before ACK", e, diagnostics.String())
	case <-ctx.Done():
		t.Fatal("public mcp_set_servers ACK missing", ctx.Err())
	}
	if _, e = writer.Write([]byte(`{"type":"user","session_id":"","message":{"role":"user","content":"Read the actual SDK peer file"}}` + "\n")); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal("actual next ASK failed", e, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal("actual set-server ASK did not finish", ctx.Err())
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if requests != 2 || peer.calls != 1 || peer.failure != nil {
		t.Fatal("actual model/tool pipeline", requests, peer.calls, peer.failure)
	}
	ackAt, initializeAt := -1, -1
	for i, frame := range peer.frames {
		if frame.String("type") == "control_response" {
			reply, _ := frame.Object("response")
			if reply.String("request_id") == "sdk-root-init-P684" {
				ackAt = i
			}
		}
		request, _ := frame.Object("request")
		message, _ := request.Object("message")
		if request.String("subtype") == "mcp_message" && message.String("method") == "initialize" && initializeAt < 0 {
			initializeAt = i
		}
	}
	if ackAt < 0 || initializeAt <= ackAt {
		t.Fatal("original ACK must precede SDK initialize to avoid control-channel deadlock", ackAt, initializeAt)
	}
}
