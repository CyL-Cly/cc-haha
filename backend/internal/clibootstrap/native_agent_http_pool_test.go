//go:build darwin

package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The frozen public Source CLI performs the same real Agent -> private Read ->
// TaskOutput -> parent model sequence. Once the peer retires the parent socket,
// the completed child's idle socket remains the process fetch pool's resource.
func TestActualAsyncAgentReadTaskOutputAndParentHTTPShareProcessPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mu sync.Mutex
	parentRounds, childRounds, heads := 0, 0, 0
	var childPorts, parentPorts []string
	var file string
	childAllowed := make(chan struct{})
	var release sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			mu.Lock()
			heads++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodPost {
			t.Error("unexpected actual method", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var value protocol.JSONObject
		if err := value.UnmarshalJSON(body); err != nil {
			t.Error(err)
			return
		}
		isParent := strings.Contains(string(value.Get("system")), "HTTP_POOL_ACTUAL_PARENT_ONLY")
		mu.Lock()
		ownedFile := file
		var round int
		if isParent {
			parentRounds++
			round = parentRounds
			parentPorts = append(parentPorts, request.RemoteAddr)
		} else {
			childRounds++
			round = childRounds
			childPorts = append(childPorts, request.RemoteAddr)
		}
		mu.Unlock()
		if isParent {
			switch round {
			case 1:
				emitNativeTaskTool(w, "actual-pool-agent", "Agent", map[string]any{"description": "actual pool child", "prompt": "Read " + ownedFile + " and report completion.", "subagent_type": "general-purpose", "run_in_background": true, "max_turns": 4})
			case 2:
				match := regexp.MustCompile(`agentId:\s+([a-zA-Z0-9_-]+)`).FindSubmatch(body)
				if len(match) != 2 {
					t.Errorf("actual async Agent result missing: %.3000s", body)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				// Actual server framing, not an injected scheduling operation:
				// the parent must acquire another transport after TaskOutput.
				w.Header().Set("Connection", "close")
				emitNativeTaskTool(w, "actual-pool-task-output", "TaskOutput", map[string]any{"task_id": string(match[1]), "block": true, "timeout": 10000})
				release.Do(func() { close(childAllowed) })
			default:
				if !strings.Contains(string(body), "CHILD_POOL_ACTUAL_COMPLETE") || round == 3 && !strings.Contains(string(body), "<retrieval_status>success</retrieval_status>") {
					t.Errorf("parent lacks actual child result: %.3000s", body)
				}
				emitPlanFixture(w, round, "", "PARENT_POOL_ACTUAL_RESTORED")
			}
			return
		}
		switch round {
		case 1:
			select {
			case <-childAllowed:
			case <-request.Context().Done():
				return
			}
			emitNativeTaskTool(w, "actual-pool-child-read", "Read", map[string]any{"file_path": ownedFile})
		case 2:
			if !strings.Contains(string(body), "POOL_ACTUAL_OWNED_BYTES") {
				t.Errorf("next child HTTP lacks physical Read bytes: %.3000s", body)
			}
			emitPlanFixture(w, round, "", "CHILD_POOL_ACTUAL_COMPLETE")
		default:
			t.Error("unexpected child HTTP round", round)
			emitPlanFixture(w, round, "", "CHILD_POOL_ACTUAL_COMPLETE")
		}
	}))
	defer peer.Close()
	defer release.Do(func() { close(childAllowed) })
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain("actual-pool-parent-user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Launch the asynchronous reader and retrieve its actual result."})})
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Arguments.SystemPrompt = "HTTP_POOL_ACTUAL_PARENT_ONLY"
		options.Arguments.Tools = []string{"Agent,Read,TaskOutput"}
		options.Input = strings.NewReader(string(rawMain(input)) + "\n")
	})
	mu.Lock()
	file = filepath.Join(m.options.Native.CWD, "pool-owned-file.txt")
	mu.Unlock()
	if err := os.WriteFile(file, []byte("POOL_ACTUAL_OWNED_BYTES\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginNativeAPIPreconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if code, err := m.Run(ctx); code != 0 || err != nil {
		t.Fatal("actual CLI Harness", code, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if heads != 1 || childRounds != 2 || parentRounds < 3 {
		t.Fatal("real transport cardinality", heads, parentRounds, childRounds)
	}
	if childPorts[1] != parentPorts[2] {
		t.Fatal("child completion lost the shared physical process pool", childPorts, parentPorts)
	}
}
