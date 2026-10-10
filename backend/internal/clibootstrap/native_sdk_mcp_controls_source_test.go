package clibootstrap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

type sdkControlCount struct {
	Initialize int `json:"initialize"`
	List       int `json:"list"`
	Call       int `json:"call"`
}
type sdkControlPhysicalPeer struct {
	mu            sync.Mutex
	wire          sync.Mutex
	input         *io.PipeWriter
	command       *exec.Cmd
	stdin         io.WriteCloser
	scanner       *bufio.Scanner
	diagnostics   bytes.Buffer
	frames        []protocol.JSONObject
	counts        map[string]sdkControlCount
	acks          chan protocol.JSONObject
	failure       error
	closeOnResult bool
}

func sdkControlSourceCommand(t *testing.T, ctx context.Context, home, config string, peer bool) *exec.Cmd {
	t.Helper()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-controls.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_MCP_CONTROLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	if peer {
		cmd.Env = append(cmd.Env, "CC_HAHA_SDK_MCP_CONTROLS_PEER=1")
	}
	return cmd
}
func sdkControlStartPeer(t *testing.T, ctx context.Context, home, config string, input *io.PipeWriter) *sdkControlPhysicalPeer {
	t.Helper()
	p := &sdkControlPhysicalPeer{input: input, counts: map[string]sdkControlCount{}, acks: make(chan protocol.JSONObject, 40)}
	peerContext, finishPeer := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(finishPeer)
	p.command = sdkControlSourceCommand(t, peerContext, home, config, true)
	p.command.Stderr = &p.diagnostics
	var err error
	p.stdin, err = p.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.scanner = bufio.NewScanner(out)
	if err = p.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.stdin.Close()
		if err := p.command.Wait(); err != nil {
			t.Error("actual physical SDK Server exit", err, p.diagnostics.String())
		}
	})
	return p
}
func (p *sdkControlPhysicalPeer) Write(data []byte) (int, error) {
	var frame protocol.JSONObject
	if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.frames = append(p.frames, frame.Clone())
	p.mu.Unlock()
	if frame.String("type") == "control_response" {
		reply, _ := frame.Object("response")
		p.acks <- reply.Clone()
	}
	request, _ := frame.Object("request")
	if frame.String("type") == "control_request" && request.String("subtype") == "mcp_message" {
		message, _ := request.Object("message")
		name := request.String("server_name")
		p.mu.Lock()
		c := p.counts[name]
		switch message.String("method") {
		case "initialize":
			c.Initialize++
		case "tools/list":
			c.List++
		case "tools/call":
			c.Call++
		}
		p.counts[name] = c
		p.mu.Unlock()
		go func() {
			p.wire.Lock()
			defer p.wire.Unlock()
			body, _ := protocol.EncodeNDJSON(message)
			_, err := p.stdin.Write(body)
			var physical json.RawMessage
			if err == nil {
				if p.scanner.Scan() {
					physical = append(json.RawMessage(nil), p.scanner.Bytes()...)
				} else {
					err = fmt.Errorf("actual SDK peer stdout: %w", p.scanner.Err())
				}
			}
			response := map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"mcp_response": physical}}
			if err != nil {
				p.mu.Lock()
				p.failure = err
				p.mu.Unlock()
				response = map[string]any{"subtype": "error", "request_id": frame.String("request_id"), "error": err.Error()}
			}
			reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": response})
			_, _ = p.input.Write(append(reply, '\n'))
		}()
	}
	if frame.String("type") == "control_request" && request.String("subtype") == "can_use_tool" {
		reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}}})
		go func() { _, _ = p.input.Write(append(reply, '\n')) }()
	}
	if p.closeOnResult && frame.String("type") == "result" {
		_ = p.input.Close()
	}
	return len(data), nil
}
func (p *sdkControlPhysicalPeer) countSnapshot() map[string]sdkControlCount {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]sdkControlCount{}
	for k, v := range p.counts {
		out[k] = v
	}
	return out
}

func TestActualSDKMCPControlsOriginalBranchesAndPhysicalServer(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.CWD = f.home
	f.write(t, filepath.Join(f.config, "settings.json"), `{"allowedMcpServers":[],"deniedMcpServers":[{"serverName":"sdk-p684"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rows := []map[string]any{
		{"id": "add", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"sdk-p684": map[string]any{"type": "sdk", "name": "original", "marker": 1}}}, "waitRefresh": true},
		{"id": "same", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"sdk-p684": map[string]any{"type": "sdk", "name": "changed", "marker": 2}}}, "waitRefresh": true},
		{"id": "reconnect", "request": map[string]any{"subtype": "mcp_reconnect", "serverName": "sdk-p684"}},
		{"id": "disable", "request": map[string]any{"subtype": "mcp_toggle", "serverName": "sdk-p684", "enabled": false}},
		{"id": "enable", "request": map[string]any{"subtype": "mcp_toggle", "serverName": "sdk-p684", "enabled": true}},
		{"id": "failed-same", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{"sdk-p684": map[string]any{"type": "sdk", "name": "ignored", "marker": 3}}}, "waitRefresh": true},
		{"askRefresh": true},
		{"id": "persisted-disable", "persist": false, "request": map[string]any{"subtype": "mcp_toggle", "serverName": "sdk-p684", "enabled": true, "alreadyPersisted": true}},
		{"id": "persisted-enable", "persist": true, "request": map[string]any{"subtype": "mcp_toggle", "serverName": "sdk-p684", "enabled": false, "alreadyPersisted": true}},
		{"askRefresh": true},
		{"id": "missing", "request": map[string]any{"subtype": "mcp_toggle", "serverName": "absent", "enabled": false}},
		{"id": "remove", "request": map[string]any{"subtype": "mcp_set_servers", "servers": map[string]any{}}, "waitRefresh": true},
	}
	source := sdkControlSourceCommand(t, ctx, f.home, f.config, false)
	source.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": rows}))
	var diagnostics bytes.Buffer
	source.Stderr = &diagnostics
	data, err := source.Output()
	if err != nil {
		t.Fatal("actual Source SDK controls", err, diagnostics.String())
	}
	var expected struct {
		Observations []struct {
			Event *struct{ Ack protocol.JSONObject }
			After struct {
				Configs protocol.JSONObject
				Clients []protocol.JSONObject
				Status  []protocol.JSONObject
				Tools   []string
			}
			Counts  map[string]sdkControlCount
			Enabled bool
		}
	}
	if err = json.Unmarshal(data, &expected); err != nil || len(expected.Observations) != len(rows) {
		t.Fatal(err, string(data))
	}
	// The Source fixture used this private directory. Recreate its initial saved
	// state before the real Go public input loop exercises the same rows.
	_ = os.Remove(filepath.Join(f.config, ".claude.json"))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := sdkControlStartPeer(t, ctx, f.home, f.config, writer)
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	joined := false
	go func() { _, e := m.Run(ctx); done <- e }()
	defer func() {
		_ = writer.Close()
		_ = reader.Close()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
	}()
	// Bind the public SDK IO transport without creating an initial MCP server.
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}` + "\n"))
	}()
	select {
	case <-peer.acks:
	case err := <-done:
		joined = true
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i, row := range rows {
		request, _ := row["request"].(map[string]any)
		if persisted, ok := row["persist"].(bool); ok {
			editor, e := m.nativeSettings.MCPEditor(m.mcpConfigurations)
			if e != nil {
				t.Fatal(e)
			}
			if e = editor.SetEnabled(ctx, request["serverName"].(string), persisted); e != nil {
				t.Fatal(e)
			}
		}
		var gotACK protocol.JSONObject
		if row["askRefresh"] != true {
			body, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": row["id"], "request": request})
			_, err = writer.Write(append(body, '\n'))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case gotACK = <-peer.acks:
			case err := <-done:
				joined = true
				t.Fatal("public controls closed", i, err, diagnostics.String())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			sdkControlEqual(t, i, "ACK", gotACK, expected.Observations[i].Event.Ack)
		}
		if row["waitRefresh"] == true {
			var refresh *sdkhost.OwnerFuture[struct{}]
			receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				var err error
				refresh, err = m.Host.NativeSDKMCPControlRefresh(scope)
				return err
			})
			if e == nil {
				e = receipt.Wait()
			}
			if e != nil {
				t.Fatal(e)
			}
			if refresh != nil {
				if _, e = refresh.Wait(ctx); e != nil {
					t.Fatal("post ACK physical refresh", i, e)
				}
			}
		}
		var configs protocol.JSONObject
		var clients, status []protocol.JSONObject
		var tools []string
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			if row["askRefresh"] == true {
				if e := m.prepareNativeSDKMCPTurn(scope); e != nil {
					return e
				}
			}
			var e error
			configs, e = m.sdkMCPState.Configurations(scope)
			if e != nil {
				return e
			}
			peers, e := m.mcpTools.AgentConnections(scope)
			if e != nil {
				return e
			}
			clients = []protocol.JSONObject{}
			tools = []string{}
			for _, p := range peers {
				if p.Config.String("type") == "sdk" {
					clients = append(clients, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: rawMain(p.Name)}, protocol.JSONField{Name: "type", Value: rawMain(p.Status)}, protocol.JSONField{Name: "config", Value: rawMain(p.Config)}))
					if p.Status == "connected" {
						for _, meta := range p.Tools {
							tools = append(tools, "mcp__"+p.Name+"__"+meta.String("name"))
						}
					}
				}
			}
			status, e = m.mcpTools.ServerStatuses(ctx, scope)
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			t.Fatal("same Owner control observation", i, e)
		}
		enabled, e := m.nativeSettings.MCPExecutionEnabled(ctx, "sdk-p684", protocol.NewJSONObject())
		if e != nil {
			t.Fatal(e)
		}
		sdkControlEqual(t, i, "configs", configs, expected.Observations[i].After.Configs)
		sdkControlEqual(t, i, "clients", clients, expected.Observations[i].After.Clients)
		sdkControlEqual(t, i, "status", status, expected.Observations[i].After.Status)
		sdkControlEqual(t, i, "tools", tools, expected.Observations[i].After.Tools)
		sdkControlEqual(t, i, "counts", peer.countSnapshot(), expected.Observations[i].Counts)
		if enabled != expected.Observations[i].Enabled {
			t.Fatal("actual persisted enablement", i, enabled, expected.Observations[i].Enabled)
		}
	}
	_ = writer.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	joined = true
}
func sdkControlEqual(t *testing.T, row int, label string, a, b any) {
	t.Helper()
	aa, bb := rawMain(a), rawMain(b)
	var x, y any
	_ = json.Unmarshal(aa, &x)
	_ = json.Unmarshal(bb, &y)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("row %d %s Go=%s Source=%s", row, label, aa, bb)
	}
}

func TestActualDefaultSDKSetServersPhysicalPeerApprovalAndNextASK(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.CWD = f.home
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.write(t, filepath.Join(f.home, "sdk-control-physical.txt"), "SDK_PUBLIC_CONTROL_APPROVAL_PHYSICAL\n")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := sdkControlStartPeer(t, ctx, f.home, f.config, writer)
	peer.closeOnResult = true
	var requests [][]byte
	var lock sync.Mutex
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lock.Lock()
		requests = append(requests, body)
		n := len(requests)
		lock.Unlock()
		if n == 1 {
			if !bytes.Contains(body, []byte(`"name":"mcp__sdk-p684__read_sdk"`)) {
				t.Error("public set did not refresh real default ASK tool catalogue")
			}
			emitWorktreeModel(w, n, "mcp__sdk-p684__read_sdk", `{}`, "")
		} else {
			if !bytes.Contains(body, []byte("SDK_PUBLIC_CONTROL_APPROVAL_PHYSICAL")) {
				t.Error("actual physical MCP result missing next model request")
			}
			emitWorktreeModel(w, n, "", "", "SDK_PUBLIC_CONTROL_OK")
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: "stdio", ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	done := make(chan error, 1)
	go func() { _, e := m.Run(ctx); done <- e }()
	_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"physical-set","request":{"subtype":"mcp_set_servers","servers":{"sdk-p684":{"type":"sdk","name":"sdk-p684"}}}}` + "\n"))
	select {
	case ack := <-peer.acks:
		if ack.String("subtype") != "success" {
			t.Fatal(rawMain(ack))
		}
	case err := <-done:
		t.Fatal(err, diagnostics.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, _ = writer.Write([]byte(`{"type":"user","message":{"role":"user","content":"Read through the private SDK MCP control peer"}}` + "\n"))
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	acks, initialize, approval := -1, -1, 0
	for i, frame := range peer.frames {
		reply, _ := frame.Object("response")
		request, _ := frame.Object("request")
		message, _ := request.Object("message")
		if reply.String("request_id") == "physical-set" {
			acks = i
		}
		if message.String("method") == "initialize" && initialize < 0 {
			initialize = i
		}
		if request.String("subtype") == "can_use_tool" {
			approval++
		}
	}
	if peer.failure != nil || acks < 0 || initialize <= acks || approval != 1 || peer.counts["sdk-p684"].Call != 1 {
		t.Fatal("real SDK control/approval/physical cardinality", peer.failure, acks, initialize, approval, peer.counts)
	}
	lock.Lock()
	defer lock.Unlock()
	if len(requests) != 2 {
		t.Fatal("actual model requests", len(requests))
	}
}
