package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sdkWorktreePeerWriter struct {
	mu             sync.Mutex
	frames         []protocol.JSONObject
	input          *io.PipeWriter
	home           string
	first, second  string
	fail           error
	final          int
	oneTurn        bool
	duplicateReply bool
}

func (p *sdkWorktreePeerWriter) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var event protocol.JSONObject
	if e := event.UnmarshalJSON(bytes.TrimSpace(data)); e != nil {
		return 0, e
	}
	p.frames = append(p.frames, event.Clone())
	request, _ := event.Object("request")
	if event.String("type") == "control_request" && request.String("subtype") == "hook_callback" {
		input, _ := request.Object("input")
		response := protocol.NewJSONObject()
		switch input.String("hook_event_name") {
		case "WorktreeCreate":
			path := filepath.Join(p.home, input.String("name"))
			if p.first == "" {
				p.first = path
			} else {
				p.second = path
			}
			if e := os.MkdirAll(path, 0700); e != nil {
				p.fail = e
			}
			if e := os.WriteFile(filepath.Join(path, "hook-owned.txt"), []byte("SDK_ACTUAL_PHYSICAL_629 "+input.String("name")), 0600); e != nil {
				p.fail = e
			}
			response.Set("hookSpecificOutput", rawMain(map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": path}))
			response.Set("peer_unknown", quoteMain("strip"))
		case "WorktreeRemove":
			if e := os.RemoveAll(input.String("worktree_path")); e != nil {
				p.fail = e
			}
		}
		packet := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_response")}, protocol.JSONField{Name: "response", Value: rawMain(map[string]any{"subtype": "success", "request_id": event.String("request_id"), "response": response})})
		encoded, _ := protocol.EncodeNDJSON(packet)
		if p.duplicateReply {
			encoded = append(encoded, encoded...)
			extra, _ := protocol.EncodeNDJSON(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_response")}, protocol.JSONField{Name: "response", Value: rawMain(map[string]any{"subtype": "success", "request_id": "orphan629", "response": map[string]any{}})}))
			encoded = append(encoded, extra...)
		}
		go func() { _, _ = p.input.Write(encoded) }()
	}
	if event.String("type") == "result" {
		p.final++
		if p.oneTurn {
			_ = p.input.Close()
			return len(data), nil
		}
		if p.final == 1 {
			go func() {
				_, _ = p.input.Write([]byte(`{"type":"user","uuid":"62900000-0000-4000-8000-000000000632","message":{"role":"user","content":"Now enter a worktree, read its physical file, then exit and remove it"}}` + "\n"))
			}()
		} else {
			_ = p.input.Close()
		}
	}
	return len(data), nil
}
func TestActualSDKWorktreeCallbacksInitializeThenDefaultAgentAndEnterExitPhysicalConsumers(t *testing.T) {
	f := worktreeMainFixture(t)
	var lock sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lock.Lock()
		requests = append(requests, body)
		round := len(requests)
		lock.Unlock()
		switch round {
		case 1:
			emitWorktreeModel(w, round, "Agent", `{"prompt":"Read hook-owned.txt and report it","description":"SDK hook isolated reader","subagent_type":"general-purpose","isolation":"worktree"}`, "")
		case 2:
			emitWorktreeModel(w, round, "Read", `{"file_path":"hook-owned.txt"}`, "")
		case 3:
			emitWorktreeModel(w, round, "", "", "actual child done")
		case 4:
			emitWorktreeModel(w, round, "", "", "actual parent done")
		case 5:
			emitWorktreeModel(w, round, "EnterWorktree", `{"name":"sdk-session629"}`, "")
		case 6:
			emitWorktreeModel(w, round, "Read", `{"file_path":"hook-owned.txt"}`, "")
		case 7:
			emitWorktreeModel(w, round, "ExitWorktree", `{"action":"remove","discard_changes":true}`, "")
		case 8:
			emitWorktreeModel(w, round, "", "", "actual worktree removed")
		default:
			t.Errorf("unexpected real model requests %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	peer := &sdkWorktreePeerWriter{input: writer, home: f.home}
	var diagnostics bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, SessionID: "62900000-0000-4000-8000-000000000629", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"init629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"matcher":"ignored-worktree-query","hookCallbackIds":["create629"]}],"WorktreeRemove":[{"hookCallbackIds":["remove629"]}]}}}` + "\n" + `{"type":"user","uuid":"62900000-0000-4000-8000-000000000630","message":{"role":"user","content":"Run one Agent with worktree isolation and read hook-owned.txt"}}` + "\n"))
	}()
	if _, e = m.Run(ctx); e != nil {
		t.Fatal("actual SDK/default MainPrint consumer", e, diagnostics.String())
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.fail != nil {
		t.Fatal(peer.fail)
	}
	lock.Lock()
	defer lock.Unlock()
	if len(requests) != 8 {
		t.Fatal("real model sequence", len(requests), diagnostics.String())
	}
	if peer.first == "" || peer.second == "" {
		t.Fatal("actual callback created no physical trees")
	}
	if _, e = os.Stat(peer.first); e != nil {
		t.Fatal("ordinary hook Agent must preserve worktree", e)
	}
	if _, e = os.Stat(peer.second); !os.IsNotExist(e) {
		t.Fatal("ExitWorktree callback did not remove actual tree", e)
	}
	if !bytes.Contains(requests[1], []byte("Working directory: "+peer.first)) || !bytes.Contains(requests[2], []byte("SDK_ACTUAL_PHYSICAL_629")) || !bytes.Contains(requests[6], []byte("SDK_ACTUAL_PHYSICAL_629")) {
		t.Fatal("actual child/parent prompt and Read did not consume returned hook path")
	}
	creates, removes := 0, 0
	for _, event := range peer.frames {
		request, _ := event.Object("request")
		if request.String("subtype") != "hook_callback" {
			continue
		}
		input, _ := request.Object("input")
		if input.String("session_id") != m.Controller.SessionID() || event.String("request_id") == "" || request.String("tool_use_id") == "" {
			t.Fatal("callback lost parent owner identity", string(rawMain(event)))
		}
		if input.String("hook_event_name") == "WorktreeCreate" {
			creates++
		} else if input.String("hook_event_name") == "WorktreeRemove" {
			removes++
			if input.String("worktree_path") != peer.second {
				t.Fatal("remove touched wrong tree")
			}
		}
	}
	if creates != 2 || removes != 1 || peer.final != 2 {
		t.Fatal("actual callback/full print result counts", creates, removes, peer.final)
	}
	if m.state.Snapshot().CWD != f.cwd || m.state.Snapshot().OriginalCWD != f.cwd {
		t.Fatal("ExitWorktree did not restore actual parent cwd")
	}
	metadata, e := filepath.Glob(filepath.Join(f.config, "projects", "*", m.Controller.SessionID(), "subagents", "agent-*.meta.json"))
	if e != nil || len(metadata) != 1 {
		t.Fatal("actual agent metadata", metadata, e)
	}
	raw, e := os.ReadFile(metadata[0])
	if e != nil || !strings.Contains(string(raw), peer.first) || !strings.Contains(string(raw), `"agentType":"general-purpose"`) {
		t.Fatal("actual child physical metadata", string(raw), e)
	}
}
