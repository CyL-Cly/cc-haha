package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualSDKWorktreeChildEnterExitCallbacksUseLiveParentOwnerAndTransport(t *testing.T) {
	f := worktreeMainFixture(t)
	var lock sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lock.Lock()
		requests = append(requests, body)
		n := len(requests)
		lock.Unlock()
		switch n {
		case 1:
			emitWorktreeModel(w, n, "Agent", `{"prompt":"Enter a session worktree, Read hook-owned.txt, then ExitWorktree and remove that session tree","description":"outer SDK isolated reader","subagent_type":"general-purpose","isolation":"worktree"}`, "")
		case 2:
			emitWorktreeModel(w, n, "EnterWorktree", `{"name":"inner-session629"}`, "")
		case 3:
			emitWorktreeModel(w, n, "Read", `{"file_path":"hook-owned.txt"}`, "")
		case 4:
			emitWorktreeModel(w, n, "ExitWorktree", `{"action":"remove","discard_changes":true}`, "")
		case 5:
			emitWorktreeModel(w, n, "", "", "outer actual SDK tree complete")
		case 6:
			emitWorktreeModel(w, n, "", "", "actual parent complete")
		default:
			t.Errorf("unexpected model request %d", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &sdkWorktreePeerWriter{input: writer, home: f.home, oneTurn: true}
	var diagnostics bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: output, Error: &diagnostics})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		_, _ = writer.Write([]byte(`{"type":"control_request","requestId":"init-nested629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["create-nested629"]}],"WorktreeRemove":[{"hookCallbackIds":["remove-nested629"]}]}}}` + "\n" + `{"type":"user","message":{"role":"user","content":"Create an outer isolated Agent that creates one nested isolated reader"}}` + "\n"))
	}()
	if _, e = m.Run(ctx); e != nil {
		t.Fatal("actual child parent SDK callback", e, diagnostics.String())
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if len(requests) != 6 || output.first == "" || output.second == "" {
		t.Fatal("actual nested callback/model cardinality", len(requests), output.first, output.second, diagnostics.String(), string(requests[2]))
	}
	if !bytes.Contains(requests[1], []byte("Working directory: "+output.first)) || !bytes.Contains(requests[2], []byte("Created worktree at "+output.second)) || !bytes.Contains(requests[3], []byte("SDK_ACTUAL_PHYSICAL_629 inner-session629")) {
		t.Fatal("child returned worktree path/current physical Read did not reach model")
	}
	initialized, callbackCount := false, 0
	for _, event := range output.frames {
		if event.String("type") == "control_response" {
			response, _ := event.Object("response")
			initialized = initialized || response.String("request_id") == "init-nested629"
		}
		request, _ := event.Object("request")
		if request.String("subtype") == "hook_callback" {
			callbackCount++
			input, _ := request.Object("input")
			if input.String("session_id") != m.Controller.SessionID() || request.String("callback_id") != "create-nested629" && request.String("callback_id") != "remove-nested629" {
				t.Fatal("nested callback escaped parent control transport")
			}
		}
	}
	if _, e := os.Stat(output.second); !os.IsNotExist(e) {
		t.Fatal("child SDK Remove callback did not remove physical directory", e)
	}
	if _, e := os.Stat(output.first); e != nil {
		t.Fatal("child session cleanup removed outer Agent tree", e)
	}
	if !initialized || callbackCount != 3 {
		t.Fatal("normalized initialize/live inherited callback", initialized, callbackCount)
	}
	if strings.Contains(diagnostics.String(), "forbidden") {
		t.Fatal("SDK nested immutable bridge bypassed rather than obeyed parent Owner", diagnostics.String())
	}
}
