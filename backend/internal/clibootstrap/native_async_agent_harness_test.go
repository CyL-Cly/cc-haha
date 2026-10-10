package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualBackgroundAgentToolCompletesSharedDesktopAndCLIHarnessWithHeldResult(t *testing.T) {
	for _, surface := range []string{"desktop", "cli"} {
		t.Run(surface, func(t *testing.T) {
			var parentRounds, childRounds atomic.Int32
			childAllowed := make(chan struct{})
			var unlock sync.Once
			var childDone atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var input protocol.JSONObject
				_ = input.UnmarshalJSON(body)
				system := string(input.Get("system"))
				if strings.Contains(system, "private actual parent harness") {
					count := parentRounds.Add(1)
					switch count {
					case 1:
						emitNativeTaskTool(w, "parent-bg-tool", "Agent", map[string]any{"description": "Actual shared async task", "prompt": "Return CHILD_COMPLETE_8A3E", "run_in_background": true})
					case 2:
						if !strings.Contains(string(body), "Async agent launched successfully.") {
							t.Errorf("async tool actual mapper missing %.1000s", body)
						}
						emitPlanFixture(w, int(count), "", "PARENT_HELD_8A3E")
						unlock.Do(func() { close(childAllowed) })
					default:
						if !strings.Contains(string(body), "CHILD_COMPLETE_8A3E") || !strings.Contains(string(body), "<task-notification>") {
							t.Errorf("actual child notification not consumed %.1600s", body)
						}
						emitPlanFixture(w, int(count), "", "PARENT_FOLLOWUP_8A3E")
					}
				} else {
					childRounds.Add(1)
					select {
					case <-childAllowed:
					case <-r.Context().Done():
						return
					}
					time.Sleep(200 * time.Millisecond)
					emitPlanFixture(w, 1, "", "CHILD_COMPLETE_8A3E")
					childDone.Store(true)
				}
			}))
			defer peer.Close()
			m := backgroundAgentMain(t, peer.URL)
			var mu sync.Mutex
			frames := []protocol.JSONObject{}
			m.options.OnOutput = func(data []byte) error {
				event := taskObject(t, data)
				if event.String("type") == "result" && !childDone.Load() {
					t.Error("Source hold-back emitted result while actual child running")
				}
				mu.Lock()
				frames = append(frames, event)
				mu.Unlock()
				return nil
			}
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain("actual-parent-human")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Launch the genuine async Agent")}))})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if surface == "desktop" {
				if e := m.RunTurn(ctx, input); e != nil {
					t.Fatal(e)
				}
			} else {
				m.options.Input = strings.NewReader(string(rawMain(input)) + "\n")
				code, e := m.Run(ctx)
				if e != nil || code != 0 {
					t.Fatal(code, e)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if parentRounds.Load() != 3 || childRounds.Load() != 1 {
				t.Fatal("genuine harness cardinality", parentRounds.Load(), childRounds.Load())
			}
			results := 0
			started := false
			notified := false
			for _, event := range frames {
				if event.String("type") == "result" {
					results++
					if event.String("result") != "PARENT_FOLLOWUP_8A3E" || string(event.Get("is_error")) != "false" {
						t.Fatalf("result %s", rawMain(event))
					}
				}
				if event.String("type") == "system" && event.String("subtype") == "task_started" && event.String("task_type") == "local_agent" {
					started = true
				}
				if event.String("type") == "system" && event.String("subtype") == "task_notification" && event.String("status") == "completed" && event.String("result") == "CHILD_COMPLETE_8A3E" {
					notified = true
				}
			}
			if results != 1 || !started || !notified {
				t.Fatalf("held result or actual SDK task missing results%d started%v notified%v", results, started, notified)
			}
		})
	}
}
