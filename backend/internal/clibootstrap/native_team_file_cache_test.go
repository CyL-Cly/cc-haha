package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualTeammateFirstTurnEmptyCacheThenHistoryTurnClonesParentReadState(t *testing.T) {
	var calls atomic.Int32
	target := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		ordinal := calls.Add(1)
		switch ordinal {
		case 1:
			emitNativeTaskTool(w, "cache-first-write", "Write", map[string]string{"file_path": target, "content": "FIRST_TURN_MUST_NOT_WRITE"})
		case 2:
			if !strings.Contains(string(data), "File has not been read yet") {
				t.Error("Source first teammate turn must have empty cache")
			}
			emitPlanFixture(w, int(ordinal), "", "FIRST_CACHE_DENIED")
		case 3:
			emitNativeTaskTool(w, "cache-next-write", "Write", map[string]string{"file_path": target, "content": "SECOND_PARENT_CACHE_WRITE_OK\n"})
		case 4:
			var request struct{ Messages []protocol.JSONObject }
			if err := json.Unmarshal(data, &request); err != nil {
				t.Error(err)
			}
			found := false
			for _, message := range request.Messages {
				for _, block := range taskBlocks(message.Get("content")) {
					if block.String("type") == "tool_result" && block.String("tool_use_id") == "cache-next-write" {
						found = true
						if string(block.Get("is_error")) == "true" {
							t.Errorf("history-bearing teammate did not receive parent cache %s", rawMain(block))
						}
					}
				}
			}
			if !found {
				t.Error("actual Write result missing")
			}
			emitPlanFixture(w, int(ordinal), "", "SECOND_CACHE_WRITE_DONE")
		default:
			t.Error("extra request", ordinal)
			http.Error(w, "extra", 500)
		}
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write"}
		o.Arguments.PermissionMode = "bypassPermissions"
	})
	target = filepath.Join(parent.workingDirectory(), "parent-read-shared.txt")
	if err := os.WriteFile(target, []byte("PARENT_READ_BEFORE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"file_path": target})
	callActualTeamTool(t, parent, "Read", string(payload))
	identity := sessioncontroller.InProcessTeammateIdentity{TeamName: "cache-source", AgentName: "worker", AgentID: "worker@cache-source", ParentSessionID: parent.Controller.SessionID()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first, err := parent.runNativeInProcessTurn(ctx, nativeInProcessTurn{Identity: identity, Prompt: "Try the existing-file Write and report its validation result.", PermissionMode: "bypassPermissions"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(target)
	if err != nil || string(before) != "PARENT_READ_BEFORE\n" {
		t.Fatalf("first cached grant leaked %q %v", before, err)
	}
	_, err = parent.runNativeInProcessTurn(ctx, nativeInProcessTurn{Identity: identity, Prompt: "Now directly Write using the original parent Read state.", PermissionMode: "bypassPermissions", History: first.Messages})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != "SECOND_PARENT_CACHE_WRITE_OK\n" || calls.Load() != 4 {
		t.Fatalf("actual second clone write %q calls%d %v", after, calls.Load(), err)
	}
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, exists, err := parent.nativeFileCache.Get(scope, target)
		if err != nil {
			return err
		}
		if !exists || string(state.Content) != string(quoteMain("PARENT_READ_BEFORE\n")) {
			return fmt.Errorf("child mutated parent cache %s", state.Content)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
