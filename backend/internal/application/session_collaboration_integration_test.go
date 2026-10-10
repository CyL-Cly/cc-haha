package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopCollaborationToolsCreateReadSendWaitAndExecuteSharedChild(t *testing.T) {
	var parentID, childID atomic.Value
	parentID.Store("")
	childID.Store("")
	var parentCalls, childCalls atomic.Int32
	var parentStep atomic.Int32
	parentStep.Store(1)
	childFinalGate := make(chan struct{})
	var releaseChildFinal sync.Once
	var nonTerminalWaits atomic.Int32
	var target string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		request := obj(data)
		metadata := obj(request.Get("metadata"))
		identity := obj([]byte(metadata.String("user_id")))
		id := identity.String("session_id")
		if id != parentID.Load().(string) {
			childID.Store(id)
			switch childCalls.Add(1) {
			case 1:
				response(w, "collaboration-child-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_GO_COLLABORATION_ARTIFACT"}, "")
			case 2:
				response(w, "collaboration-child-read", "Read", map[string]any{"file_path": target}, "")
			case 3:
				if !bytes.Contains(data, []byte("ACTUAL_GO_COLLABORATION_ARTIFACT")) {
					t.Error("child's actual Read result did not reach its own Harness")
				}
				// Keep the actual child active until the parent has consumed a real
				// nonterminal WaitSessions snapshot. This proves the wait/read ordering
				// even when the loopback provider can otherwise finish in one scheduler tick.
				select {
				case <-childFinalGate:
				case <-r.Context().Done():
					return
				}
				response(w, "collaboration-child-complete", "", nil, "CHILD_COLLABORATION_OK")
			default:
				response(w, "collaboration-child-message", "", nil, "CHILD_MESSAGE_RECEIVED")
			}
			return
		}
		for _, name := range []string{"ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions"} {
			if !bytes.Contains(request.Get("tools"), []byte(name)) {
				t.Errorf("actual desktop inference did not advertise %s", name)
			}
		}
		call := parentCalls.Add(1)
		switch parentStep.Load() {
		case 1:
			parentStep.Store(2)
			response(w, "collaboration-parent-list", "ListSessions", map[string]any{}, "")
		case 2:
			parentStep.Store(3)
			response(w, "collaboration-parent-create", "CreateSession", map[string]any{"prompt": "Write and read the actual collaboration artifact", "title": "Actual collaboration child"}, "")
		case 3:
			// A provider response is not an SDK result or a committed transcript.
			// Use the real collaboration protocol's revision/state before ReadSession.
			createdChild := latestCollaborationToolResult(request, "call-collaboration-parent-create")
			targetID := createdChild.String("sessionId")
			if targetID == "" {
				t.Error("actual CreateSession returned no child identity")
			}
			snapshot := latestCollaborationToolResult(request, "call-collaboration-parent-wait-child-")
			complete := false
			observedTarget := false
			for _, member := range collaborationJSONObjects(snapshot.Get("members")) {
				if member.String("sessionId") == targetID {
					observedTarget = true
					if member.String("state") == "failed" {
						t.Error("actual child failed before ReadSession")
					}
					complete = member.String("state") == "completed"
				}
			}
			if complete {
				if snapshot.String("waitReason") == "capacity_blocked" {
					t.Error("completion incorrectly reported capacity blocked")
				}
				parentStep.Store(4)
				response(w, "collaboration-parent-read", "ReadSession", map[string]any{"sessionId": targetID}, "")
			} else {
				if observedTarget {
					nonTerminalWaits.Add(1)
					releaseChildFinal.Do(func() { close(childFinalGate) })
				}
				var revision int64
				_ = json.Unmarshal(snapshot.Get("revision"), &revision)
				response(w, fmt.Sprintf("collaboration-parent-wait-child-%d", call), "WaitSessions", map[string]any{"afterRevision": revision, "sessionIds": []string{targetID}, "timeoutMs": 10000}, "")
			}
		case 4:
			history := latestCollaborationToolResult(request, "call-collaboration-parent-read")
			if !bytes.Contains(history.Get("messages"), []byte("CHILD_COLLABORATION_OK")) {
				t.Error("ReadSession did not read actual child history after authoritative completion")
			}
			parentStep.Store(5)
			response(w, "collaboration-parent-send", "SendSessionMessage", map[string]any{"targetSessionId": childID.Load().(string), "content": "Please acknowledge receipt"}, "")
		case 5:
			parentStep.Store(6)
			response(w, "collaboration-parent-wait", "WaitSessions", map[string]any{"afterRevision": 0, "sessionIds": []string{childID.Load().(string)}, "timeoutMs": 10000}, "")
		default:
			response(w, "collaboration-parent-complete", "", nil, "ROOT_COLLABORATION_OK")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	target = filepath.Join(o.WorkDir, "actual-collaboration.txt")
	o.Environment["ENABLE_TOOL_SEARCH"] = "false"
	o.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "0"
	o.Arguments.Tools = []string{"Read,Write,ListSessions,ReadSession,CreateSession,SendSessionMessage,WaitSessions"}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	url := "http://" + listener.Addr().String()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, listener) }()
	defer func() {
		cancel()
		select {
		case e := <-served:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("actual collaboration server did not stop its children")
		}
	}()
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, url+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer fake-only-loopback")
	created, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	if created.StatusCode != 201 {
		t.Fatal(created.StatusCode, string(data))
	}
	id := obj(data).String("sessionId")
	parentID.Store(id)
	config, e := websocket.NewConfig("ws"+strings.TrimPrefix(url, "http")+"/ws/"+id, url)
	if e != nil {
		t.Fatal(e)
	}
	config.Header.Set("Authorization", "Bearer fake-only-loopback")
	ws, e := websocket.DialConfig(config)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(23 * time.Second))
	if e = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Create a child conversation, inspect its completed task, message it and wait for its status."}))); e != nil {
		t.Fatal(e)
	}
	completed := false
	finalText := false
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		var data string
		if e = websocket.Message.Receive(ws, &data); e != nil {
			t.Fatal(e)
		}
		frame := obj([]byte(data))
		if frame.String("type") == "error" {
			t.Fatal("actual collaboration Harness error", data)
		}
		if frame.String("type") == "content_start" && frame.String("blockType") == "tool_use" {
			seen[frame.String("toolName")] = true
		}
		if strings.Contains(data, "ROOT_COLLABORATION_OK") {
			finalText = true
		}
		if frame.String("type") == "message_complete" && finalText {
			completed = true
		}
		if completed && frame.String("type") == "session_state" && frame.String("turnState") == "idle" {
			break
		}
	}
	if !completed {
		t.Fatal("canonical parent did not complete")
	}
	if nonTerminalWaits.Load() == 0 {
		t.Fatal("fixture never exercised authoritative wait before child completion")
	}
	for _, name := range []string{"ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions"} {
		if !seen[name] {
			t.Fatal("actual tool did not cross desktop projection", name, seen)
		}
	}
	artifact, e := os.ReadFile(target)
	if e != nil || string(artifact) != "ACTUAL_GO_COLLABORATION_ARTIFACT" {
		t.Fatal("child did not execute real tools", e, string(artifact))
	}
	statePath := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "session-collaboration", "state.json")
	state, e := os.ReadFile(statePath)
	if e != nil {
		t.Fatal(e)
	}
	var store protocol.JSONObject
	_ = json.Unmarshal(state, &store)
	if !bytes.Contains(state, []byte(childID.Load().(string))) {
		t.Fatal("shared collaboration state omitted child")
	}
}

func collaborationJSONObjects(value json.RawMessage) []protocol.JSONObject {
	var rows []protocol.JSONObject
	_ = json.Unmarshal(value, &rows)
	return rows
}
func latestCollaborationToolResult(request protocol.JSONObject, prefix string) protocol.JSONObject {
	messages := collaborationJSONObjects(request.Get("messages"))
	for i := len(messages) - 1; i >= 0; i-- {
		blocks := collaborationJSONObjects(messages[i].Get("content"))
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].String("type") == "tool_result" && strings.HasPrefix(blocks[j].String("tool_use_id"), prefix) {
				return obj([]byte(blocks[j].String("content")))
			}
		}
	}
	return protocol.NewJSONObject()
}
