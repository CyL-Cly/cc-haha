package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualForegroundAgentPromotesToSameIDIndependentHarness(t *testing.T) {
	for _, mode := range []string{"manual", "manual-fast", "auto"} {
		t.Run(mode, func(t *testing.T) {
			foregroundStarted := make(chan struct{})
			oldAllowed := make(chan struct{})
			var oldFinished atomic.Bool
			var promotedAt atomic.Int64
			var newStartedAt atomic.Int64
			var parentReturnedAt atomic.Int64
			backgroundAllowed := make(chan struct{})
			var release sync.Once
			var oldRelease sync.Once
			var children, parent atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "private actual parent harness") {
					n := parent.Add(1)
					if n == 1 {
						emitNativeTaskTool(w, "promote-original-card", "Agent", map[string]any{"description": "Actual promotable worker", "prompt": "ACTUAL_PROMOTION_ORIGINAL_PROMPT_C7A2"})
						return
					}
					if n == 2 {
						parentReturnedAt.Store(time.Now().UnixNano())
						if !strings.Contains(string(body), "Async agent launched successfully") {
							t.Errorf("original Tool did not return Source async result %.1700s", body)
						}
						emitPlanFixture(w, int(n), "", "PROMOTION_PARENT_HELD_C7A2")
						release.Do(func() { close(backgroundAllowed) })
						return
					}
					if !strings.Contains(string(body), "ACTUAL_PROMOTION_CHILD_FINISHED_C7A2") {
						t.Error("background notification not delivered")
					}
					emitPlanFixture(w, int(n), "", "ACTUAL_PROMOTION_PARENT_DONE_C7A2")
					return
				}
				n := children.Add(1)
				if n == 1 {
					close(foregroundStarted)
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					select {
					case <-oldAllowed:
						emitPlanFixture(w, int(n), "", "LOST_PENDING_FOREGROUND_MESSAGE_C7A2")
						oldFinished.Store(true)
					case <-r.Context().Done():
						t.Error("promotion cancelled old transport instead of queued iterator.return")
					}
					return
				}
				newStartedAt.Store(time.Now().UnixNano())
				oldRelease.Do(func() { close(oldAllowed) })
				if !strings.Contains(string(body), "ACTUAL_PROMOTION_ORIGINAL_PROMPT_C7A2") {
					t.Error("restart lost original prompt")
				}
				select {
				case <-backgroundAllowed:
				case <-r.Context().Done():
					return
				}
				emitPlanFixture(w, int(n), "", "ACTUAL_PROMOTION_CHILD_FINISHED_C7A2")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
				if mode == "auto" {
					n.Environment["CLAUDE_AUTO_BACKGROUND_TASKS"] = "1"
					n.Timer = func(delay time.Duration, callback func()) func() {
						if delay != 120*time.Second {
							t.Error("Source auto-background timer must be 120 seconds", delay)
						}
						timer := time.AfterFunc(80*time.Millisecond, callback)
						return func() { timer.Stop() }
					}
				}
			})
			if e := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				copy := current.Clone()
				copy.Set("hasUsedBackgroundTask", rawMain(false))
				copy.Set("futureBackgroundField", rawMain(map[string]any{"preserve": true}))
				return &copy, nil
			}); e != nil {
				t.Fatal(e)
			}
			idle, e := m.BackgroundFromTerminal(context.Background())
			if e != nil || idle {
				t.Fatal("idle terminal mutation", idle, e)
			}
			config, e := m.Global.Get()
			if e != nil || string(config.Get("hasUsedBackgroundTask")) != "false" {
				t.Fatal("idle wrote flag", config, e)
			}
			states := make(chan bool, 8)
			current, unsubscribe, e := m.SubscribeForegroundTasks(context.Background(), func(value bool) {
				select {
				case states <- value:
				default:
					t.Error("foreground callback produced unexpected excess changes")
				}
			})
			if e != nil || current {
				t.Fatal("initial foreground subscription", current, e)
			}
			defer unsubscribe()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			done := make(chan error, 1)
			var starts atomic.Int32
			var id string
			m.options.OnOutput = func(data []byte) error {
				row := taskObject(t, data)
				if row.String("type") == "system" && row.String("subtype") == "task_started" && row.String("task_type") == "local_agent" {
					id = row.String("task_id")
					starts.Add(1)
				}
				return nil
			}
			go func() {
				done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run the actual foreground Agent"})}))
			}()
			select {
			case <-foregroundStarted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case value := <-states:
				if !value {
					t.Fatal("real registration did not publish foreground")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			has, e := m.HasForegroundTasks(ctx)
			if strings.HasPrefix(mode, "manual") {
				if e != nil || !has {
					t.Fatal("missing actual foreground runtime", has, e)
				}
				m.options.Native.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"] = "1"
				disabled, e := m.BackgroundFromTerminal(ctx)
				if e != nil || disabled {
					t.Fatal("disabled promoted actual task", disabled, e)
				}
				config, e = m.Global.Get()
				if e != nil || string(config.Get("hasUsedBackgroundTask")) != "false" {
					t.Fatal("disabled wrote flag", config, e)
				}
				delete(m.options.Native.Environment, "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS")
				promotedAt.Store(time.Now().UnixNano())
				changed, e := m.BackgroundFromTerminal(ctx)
				if mode == "manual-fast" {
					time.AfterFunc(50*time.Millisecond, func() { oldRelease.Do(func() { close(oldAllowed) }) })
				}
				if e != nil || !changed {
					t.Fatal("actual promotion", changed, e)
				}
			}
			select {
			case value := <-states:
				if value {
					t.Fatal("actual promotion must drop foreground context")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			config, e = m.Global.Get()
			if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(config.Get("futureBackgroundField"))) != `{"preserve":true}` {
				t.Fatalf("unknown global field lost %s/%v", rawMain(config), e)
			}
			want := "false"
			if strings.HasPrefix(mode, "manual") {
				want = "true"
			}
			if string(config.Get("hasUsedBackgroundTask")) != want {
				t.Fatal("shortcut/auto flag", mode, config)
			}
			if mode == "manual" && (time.Duration(newStartedAt.Load()-promotedAt.Load()) < 950*time.Millisecond || time.Duration(parentReturnedAt.Load()-promotedAt.Load()) > 500*time.Millisecond) {
				t.Error("new background started before pending-next grace")
			}
			if mode == "manual-fast" && time.Duration(newStartedAt.Load()-promotedAt.Load()) > 800*time.Millisecond {
				t.Error("settled return still waited full grace")
			}
			if !oldFinished.Load() {
				t.Error("old iterator never resumed its own finally")
			}
			if starts.Load() != 1 || children.Load() != 2 || parent.Load() != 3 {
				t.Fatal("did not reuse actual same task/one restarted child", starts.Load(), children.Load(), parent.Load())
			}
			task := waitActualAgent(t, m, id)
			if task.Data.String("status") != "completed" || string(task.Data.Get("isBackgrounded")) != "true" || task.Data.String("toolUseId") != "promote-original-card" {
				t.Fatal("promotion lost actual task", rawMain(task.Data))
			}
			data, e := os.ReadFile(task.Data.String("outputFile"))
			if e != nil {
				t.Fatal(e)
			}
			promptCount := 0
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var row protocol.JSONObject
				if json.Unmarshal([]byte(line), &row) != nil {
					t.Fatal("invalid actual sidechain")
				}
				api, _ := row.Object("message")
				if row.String("type") == "user" && strings.Contains(string(api.Get("content")), "ACTUAL_PROMOTION_ORIGINAL_PROMPT_C7A2") {
					promptCount++
				}
			}
			if !strings.Contains(string(data), "LOST_PENDING_FOREGROUND_MESSAGE_C7A2") {
				t.Error("losing pending-next assistant was not physically recorded")
			}
			if strings.Contains(string(task.Data.Get("result")), "LOST_PENDING_FOREGROUND_MESSAGE_C7A2") {
				t.Error("losing pending-next value entered restarted background result")
			}
			if promptCount != 1 {
				t.Fatalf("Source restart must reuse actual prompt UUID: physical prompts=%d", promptCount)
			}
			has, e = m.HasForegroundTasks(ctx)
			if e != nil || has {
				t.Fatal("completed background still claims FG", has, e)
			}
		})
	}
}
