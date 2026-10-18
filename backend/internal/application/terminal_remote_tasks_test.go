package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

const remoteTaskResumeSID = "34343434-3434-4434-8434-343434343434"

func terminalRemoteTaskOptions(t *testing.T, model, proxy, ca string) Options {
	t.Helper()
	o := terminalUsageFixtureOptions(t, model, proxy, ca)
	// RunCommand sets this actual TUI producer binding; plain Session fixtures
	// retain the separate headless/finite admission contract.
	o.ForegroundShellProgress = true
	o.Arguments.Tools = []string{"Read,Write,TaskOutput,TaskStop"}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	global := map[string]any{"oauthAccount": map[string]any{"organizationUuid": "owned-remote-task-org", "billingType": "stripe_subscription"}, "lastReleaseNotesSeen": o.Version, "future": 17}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config, "cache"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "cache", "changelog.md"), []byte("## 0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "tmp")
		env["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	}
	return o
}

func seedTerminalRemoteTask(t *testing.T, o Options, label string) string {
	t.Helper()
	project := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.WorkDir))
	if err := os.MkdirAll(filepath.Join(project, remoteTaskResumeSID, "remote-agents"), 0700); err != nil {
		t.Fatal(err)
	}
	rows := []any{
		map[string]any{"type": "user", "uuid": "remote-resume-old-user", "timestamp": "2026-10-01T00:00:00.000Z", "cwd": o.WorkDir, "sessionId": remoteTaskResumeSID, "parentUuid": nil, "message": map[string]any{"role": "user", "content": "OLD_REMOTE_TASK_RESUME_CONTEXT"}},
		map[string]any{"type": "assistant", "uuid": "remote-resume-old-assistant", "timestamp": "2026-10-01T00:00:01.000Z", "cwd": o.WorkDir, "sessionId": remoteTaskResumeSID, "parentUuid": "remote-resume-old-user", "message": map[string]any{"role": "assistant", "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "OLD_REMOTE_TASK_RESUME_ANSWER"}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}, "stop_reason": "end_turn"}},
	}
	var transcript []byte
	for _, row := range rows {
		transcript = append(transcript, raw(row)...)
		transcript = append(transcript, '\n')
	}
	if err := os.WriteFile(filepath.Join(project, remoteTaskResumeSID+".jsonl"), transcript, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, remoteTaskResumeSID, "remote-agents", "remote-agent-owned-"+label+".meta.json")
	meta := map[string]any{"taskId": "owned-" + label, "remoteTaskType": "remote-agent", "sessionId": "session-owned-" + label, "title": "Owned " + label, "command": "ORIGINAL_REMOTE_COMMAND_" + label, "spawnedAt": 1700000000000, "toolUseId": "owned-tool-" + label}
	if err := os.WriteFile(path, raw(meta), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func terminalRemoteTask(t *testing.T, s *Session, id string, ready func(protocol.JSONObject) bool) protocol.JSONObject {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for {
		value, err := s.process.TerminalPanelSnapshot(context.Background(), "tasks")
		if err != nil {
			t.Fatal(err)
		}
		var rows []protocol.JSONObject
		if err = json.Unmarshal(value.Get("tasks"), &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.String("id") == id && ready(row) {
				return row
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Actual same-owner remote task did not reach required state", string(raw(value)))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestActualNativeResumeAndInitialHistoryRestoreRemoteTaskAndNextHTTP(t *testing.T) {
	for _, profile := range []string{"initial-history", "slash-resume"} {
		t.Run(profile, func(t *testing.T) {
			var mu sync.Mutex
			var requests, bodies []string
			proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-usage-token" || r.Header.Get("x-organization-uuid") != "owned-remote-task-org" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" {
					t.Error("Actual task cached-account headers", r.Header)
				}
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.RequestURI())
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/sessions/session-owned-running":
					_, _ = w.Write([]byte(`{"session_status":"running"}`))
				case "/v1/sessions/session-owned-running/events":
					_, _ = w.Write(raw(map[string]any{"data": []any{map[string]any{"type": "assistant", "session_id": "session-owned-running", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "PHYSICAL_RESTORED_REMOTE_OUTPUT"}}}}, map[string]any{"type": "result", "subtype": "success", "session_id": "session-owned-running", "result": "PHYSICAL_REMOTE_RESULT"}}, "last_id": "owned-last", "has_more": false}))
				case "/v1/sessions/session-owned-missing":
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"error":{"message":"owned missing"}}`))
				case "/v1/sessions/session-owned-archived":
					_, _ = w.Write([]byte(`{"session_status":"archived"}`))
				case "/v1/sessions/session-owned-auth":
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":{"message":"owned recoverable"}}`))
				default:
					t.Error("unowned actual task API", r.URL)
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(body))
				mu.Unlock()
				response(w, "remote-task-next", "", nil, "REMOTE_TASK_NEXT_HUMAN_COMPLETE")
			}))
			defer model.Close()
			o := terminalRemoteTaskOptions(t, model.URL, proxy, ca)
			paths := map[string]string{}
			for _, label := range []string{"running", "missing", "archived", "auth"} {
				paths[label] = seedTerminalRemoteTask(t, o, label)
			}
			if profile == "initial-history" {
				o.Arguments.SessionID = ""
				o.Arguments.Resume = remoteTaskResumeSID
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if profile == "initial-history" {
				err = s.StartTerminalRemoteTasks(context.Background())
			} else {
				_, err = s.InvokeTerminalCommand(context.Background(), "resume", remoteTaskResumeSID, func(event tui.Event) error { t.Log("actual resume event", event.Kind, event.UserMessages); return nil })
			}
			if err != nil {
				t.Fatal("genuine resume/mount producer", err)
			}
			if s.process.Controller.SessionID() != remoteTaskResumeSID {
				t.Fatal("Actual resume callback did not adopt target", s.process.Controller.SessionID())
			}
			task := terminalRemoteTask(t, s, "owned-running", func(v protocol.JSONObject) bool {
				return v.String("status") == "completed" && string(v.Get("notified")) == "true"
			})
			if task.String("sessionId") != "session-owned-running" || task.String("command") != "ORIGINAL_REMOTE_COMMAND_running" || string(task.Get("startTime")) != "1700000000000" {
				t.Fatal("sidecar fields lost", string(raw(task)))
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				data, _ := os.ReadFile(task.String("outputFile"))
				_, running := os.Stat(paths["running"])
				_, missing := os.Stat(paths["missing"])
				_, archived := os.Stat(paths["archived"])
				if strings.Contains(string(data), "PHYSICAL_RESTORED_REMOTE_OUTPUT") && os.IsNotExist(running) && os.IsNotExist(missing) && os.IsNotExist(archived) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual output/drain/sidecar deletes not complete", string(data), running, missing, archived)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err = os.Stat(paths["auth"]); err != nil {
				t.Fatal("recoverable401 sidecar was dropped", err)
			}
			if err = s.Submit(context.Background(), tui.Input{Text: "NEXT_HUMAN_AFTER_REMOTE_RESTORE"}, nil); err != nil {
				t.Fatal("same Harness next ordinary model", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 6 {
				t.Fatal("actual Source fetch/poll/metadata contract", requests)
			}
			if len(bodies) == 0 || !strings.Contains(strings.Join(bodies, "\n"), "OLD_REMOTE_TASK_RESUME_CONTEXT") || !strings.Contains(strings.Join(bodies, "\n"), "NEXT_HUMAN_AFTER_REMOTE_RESTORE") {
				t.Fatal("same resumed model history missing", bodies)
			}
		})
	}
}

func TestActualRemoteTaskStopHeldPollAndGlobalCloseDoNotResurrectOrArchiveOnExit(t *testing.T) {
	for _, profile := range []string{"stop-held-poll", "global-close"} {
		t.Run(profile, func(t *testing.T) {
			started, joined, release, archived := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/sessions/session-owned-held":
					_, _ = w.Write([]byte(`{"session_status":"running"}`))
				case "/v1/sessions/session-owned-held/events":
					close(started)
					select {
					case <-r.Context().Done():
						close(joined)
						return
					case <-release:
					}
					_, _ = w.Write(raw(map[string]any{"data": []any{map[string]any{"type": "assistant", "session_id": "session-owned-held", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "LATE_REAL_REMOTE_OUTPUT"}}}}, map[string]any{"type": "result", "subtype": "success", "session_id": "session-owned-held"}}, "has_more": false, "last_id": "held-last"}))
					close(joined)
				case "/v1/sessions/session-owned-held/archive":
					if r.Method != "POST" {
						t.Error(r.Method)
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != "{}" {
						t.Error("archive body", string(body))
					}
					once.Do(func() { close(archived) })
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Error("unowned held remote API", r.URL)
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			o := terminalRemoteTaskOptions(t, "http://127.0.0.1:9", proxy, ca)
			path := seedTerminalRemoteTask(t, o, "held")
			o.Arguments.SessionID = ""
			o.Arguments.Resume = remoteTaskResumeSID
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.StartTerminalRemoteTasks(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("real held poll not started")
			}
			if profile == "global-close" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err = s.Close(ctx); err != nil {
					t.Fatal("global close did not join owned poll", err)
				}
				select {
				case <-joined:
				case <-time.After(time.Second):
					t.Fatal("held physicalHTTP not canceled/joined")
				}
				select {
				case <-archived:
					t.Fatal("exit archived cloud session")
				default:
				}
				if _, err = os.Stat(path); err != nil {
					t.Fatal("global exit removed resumable metadata", err)
				}
				return
			}
			result, err := s.process.StopBackgroundTask(context.Background(), "owned-held")
			if err != nil || string(result.Get("ok")) != "true" {
				t.Fatal("actual TaskStop consumer", string(raw(result)), err)
			}
			select {
			case <-joined:
				t.Fatal("kill canceled Source held poll")
			default:
			}
			select {
			case <-archived:
			case <-time.After(time.Second):
				t.Fatal("actual archive HTTP missing")
			}
			close(release)
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Fatal("actual late poll did not return")
			}
			task := terminalRemoteTask(t, s, "owned-held", func(v protocol.JSONObject) bool {
				return v.String("status") == "killed" && string(v.Get("notified")) == "true"
			})
			deadline := time.Now().Add(2 * time.Second)
			for {
				data, _ := os.ReadFile(task.String("outputFile"))
				if strings.Contains(string(data), "LATE_REAL_REMOTE_OUTPUT") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("late actual output was lost", string(data))
				}
				time.Sleep(10 * time.Millisecond)
			}
			value, _ := s.process.TerminalPanelSnapshot(context.Background(), "tasks")
			if strings.Contains(string(raw(value)), `"status":"completed"`) {
				t.Fatal("held result resurrected killed task", string(raw(value)))
			}
		})
	}
}
