package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The same physical HTTP peer is consumed by the original Source reconciliation
// and the public Go print loop. Its tools read an actual private file.
func sdkControlHTTPPeer(t *testing.T, file string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		if e := request.UnmarshalJSON(body); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if request.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch request.String("method") {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "actual process control file peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "read_process", "description": "Read the private physical control file", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			physical, e := os.ReadFile(file)
			if e != nil {
				t.Error(e)
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(physical)}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.Get("id")), "result": result})
	}))
}
func TestActualSDKSetServersProcessPolicyReconcileSourceAndNextASK(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.CWD = f.home
	file := filepath.Join(f.home, "process-control-physical.txt")
	f.write(t, file, "PROCESS_CONTROL_PHYSICAL_ONLY\n")
	f.write(t, filepath.Join(f.config, "settings.json"), `{"deniedMcpServers":[{"serverName":"blocked"}],"permissions":{"allow":["mcp__process__read_process"]}}`)
	peer := sdkControlHTTPPeer(t, file)
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg := func(path string) map[string]any { return map[string]any{"type": "http", "url": peer.URL + path} }
	rows := []map[string]any{
		{"id": "process-add", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"process": cfg("/first"), "blocked": cfg("/blocked")}}},
		{"id": "process-unchanged", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"process": cfg("/first")}}},
		{"id": "process-replace", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"process": cfg("/replaced")}}},
		{"id": "process-disable", "request": map[string]any{"subtype": "mcp_toggle", "serverName": "process", "enabled": false}},
		{"id": "process-enable", "request": map[string]any{"subtype": "mcp_toggle", "serverName": "process", "enabled": true}},
		{"id": "process-reconnect", "request": map[string]any{"subtype": "mcp_reconnect", "serverName": "process"}},
		{"id": "process-missing", "request": map[string]any{"subtype": "mcp_reconnect", "serverName": "missing"}},
		{"id": "process-remove", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{}}},
		{"id": "process-return", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"process": cfg("/final")}}},
	}
	source := sdkControlSourceCommand(t, ctx, f.home, f.config, false)
	source.Env = append(source.Env, "CC_HAHA_SDK_MCP_CONTROLS_LOOPBACK_ORIGIN="+peer.URL)
	source.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": rows}))
	var diagnostics bytes.Buffer
	source.Stderr = &diagnostics
	data, e := source.Output()
	if e != nil {
		t.Fatal("actual Source process controls", e, diagnostics.String())
	}
	var expected struct {
		Observations []struct {
			Event struct{ Ack protocol.JSONObject }
			After struct{ Status []protocol.JSONObject }
		}
	}
	if e = json.Unmarshal(data, &expected); e != nil || len(expected.Observations) != len(rows) {
		t.Fatal(e, string(data))
	}
	_ = os.Remove(filepath.Join(f.config, ".claude.json"))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	public := &sdkControlPhysicalPeer{input: writer, counts: map[string]sdkControlCount{}, acks: make(chan protocol.JSONObject, 40), closeOnResult: true}
	var lock sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lock.Lock()
		requests = append(requests, body)
		n := len(requests)
		lock.Unlock()
		if n == 1 {
			if !bytes.Contains(body, []byte(`"name":"mcp__process__read_process"`)) {
				t.Error("real process controls did not reach next default ASK catalogue")
			}
			emitWorktreeModel(w, n, "mcp__process__read_process", `{}`, "")
		} else {
			if !bytes.Contains(body, []byte("PROCESS_CONTROL_PHYSICAL_ONLY")) {
				t.Error("actual process result not consumed by next model request")
			}
			emitWorktreeModel(w, n, "", "", "PROCESS_CONTROL_OK")
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: public, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	joined := false
	go func() { _, err := m.Run(ctx); done <- err }()
	defer func() {
		_ = writer.Close()
		_ = reader.Close()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
	}()
	for i, row := range rows {
		body, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": row["id"], "request": row["request"]})
		go func() { _, _ = writer.Write(append(body, '\n')) }()
		select {
		case ack := <-public.acks:
			sdkControlEqual(t, i, "process ACK", ack, expected.Observations[i].Event.Ack)
		case e := <-done:
			joined = true
			t.Fatal("public process control loop", i, e, diagnostics.String())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var status []protocol.JSONObject
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			var e error
			status, e = m.mcpTools.ServerStatuses(ctx, scope)
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			t.Fatal(e)
		}
		sdkControlEqual(t, i, "process status", status, expected.Observations[i].After.Status)
	}
	go func() {
		_, _ = writer.Write([]byte(`{"type":"user","message":{"role":"user","content":"Read the private process MCP file after reconciling controls"}}` + "\n"))
	}()
	select {
	case e = <-done:
		joined = true
		if e != nil {
			t.Fatal(e, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lock.Lock()
	defer lock.Unlock()
	if len(requests) != 2 {
		t.Fatal("next actual ASK/model requests", len(requests))
	}
}
