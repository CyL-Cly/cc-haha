package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/ccrsessions"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeRemoteAgentPollBusinessTransitionsMatchWholeOriginalRestore(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned original Bun required")
	}
	assistant := func(text string) map[string]any {
		return map[string]any{"type": "assistant", "session_id": "owned-poll-session", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
	}
	result := func(status string) map[string]any {
		return map[string]any{"type": "result", "subtype": status, "session_id": "owned-poll-session"}
	}
	hook := func(text string) map[string]any {
		return map[string]any{"type": "system", "subtype": "hook_progress", "hook_event": "SessionStart", "session_id": "owned-poll-session", "stdout": text}
	}
	todo := map[string]any{"type": "assistant", "session_id": "owned-poll-session", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "TodoWrite", "input": map[string]any{"todos": []any{map[string]any{"content": "owned todo", "status": "in_progress", "activeForm": "Doing owned todo", "ignored": "strip"}}}}}}}
	for _, tc := range []struct {
		name              string
		events            []any
		flags             map[string]any
		running, archived bool
	}{
		{"success", []any{assistant("owned output"), result("success")}, nil, false, false},
		{"failure", []any{assistant("owned error output"), result("error_during_execution")}, nil, false, false},
		{"long-running-todo", []any{todo, result("success")}, map[string]any{"isLongRunning": true, "remoteTaskType": "autofix-pr"}, true, false},
		{"truthy-long-running-string", []any{assistant("owned truthy long-running"), result("success")}, map[string]any{"isLongRunning": "false"}, true, false},
		{"ultraplan-result-not-complete", []any{assistant("owned ultraplan"), result("success")}, map[string]any{"isUltraplan": true, "remoteTaskType": "ultraplan"}, true, false},
		{"review-progress-tag", []any{hook(`<remote-review-progress>{"stage":"finding","bugs_found":7,"bugs_verified":2}</remote-review-progress><remote-review>owned review</remote-review>`)}, map[string]any{"isRemoteReview": true, "remoteTaskType": "ultrareview"}, false, false},
		{"review-split-hook", []any{hook("<remote-re"), hook("view>owned split review</remote-review>")}, map[string]any{"isRemoteReview": true}, false, false},
		{"review-assistant-result", []any{assistant("owned untagged findings"), result("success")}, map[string]any{"isRemoteReview": true}, false, false},
		{"review-error", []any{result("error_during_execution")}, map[string]any{"isRemoteReview": true}, false, false},
		{"truthy-review-string", []any{assistant("owned truthy review"), result("success")}, map[string]any{"isRemoteReview": "false"}, false, false},
		{"archived-after-delta", []any{assistant("owned archived output")}, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(home, ".claude")
			if err = os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{filepath.Join(config, ".credentials.json"): `{"claudeAiOauth":{"accessToken":"owned-poll-token","refreshToken":null,"expiresAt":4102444800000,"scopes":["user:inference","user:profile","user:sessions:claude_code"],"subscriptionType":"max"}}`, filepath.Join(config, ".claude.json"): `{"oauthAccount":{"organizationUuid":"owned-poll-org"}}`} {
				if err = os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			gets := 0
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer owned-poll-token" || r.Header.Get("x-organization-uuid") != "owned-poll-org" {
					t.Error("full original task auth", r.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/sessions/owned-poll-session/events" {
					_, _ = w.Write(rawMain(map[string]any{"data": tc.events, "has_more": false, "last_id": "owned-last"}))
					return
				}
				if r.URL.Path != "/v1/sessions/owned-poll-session" {
					t.Error("unowned full Source HTTP", r.URL)
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				gets++
				status := "running"
				if tc.archived && gets > 1 {
					status = "archived"
				}
				_, _ = w.Write(rawMain(map[string]any{"session_status": status, "session_context": map[string]any{}}))
			}))
			defer peer.Close()
			ca := filepath.Join(home, "owned-ca.pem")
			if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			meta := map[string]any{"taskId": "owned-poll", "remoteTaskType": "remote-agent", "sessionId": "owned-poll-session", "title": "Owned poll", "command": "ORIGINAL_COMMAND", "spawnedAt": 1700000000000, "toolUseId": "owned-tool"}
			for k, v := range tc.flags {
				meta[k] = v
			}
			input := map[string]any{"home": home, "peer": peer.URL, "sid": "12121212-1212-4212-8212-121212121212", "metadata": []any{meta}}
			if tc.running {
				input["waitLog"] = "owned-poll"
				input["logLength"] = len(tc.events)
			} else {
				input["waitResult"] = "owned-poll"
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-agent-restore.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_AGENT_RESTORE=1"}
			cmd.Stdin = bytes.NewReader(rawMain(input))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal("whole actual Source restore/poll", err, string(out), stderr.String())
			}
			var source struct {
				After map[string]protocol.JSONObject
				Queue []protocol.JSONObject
				Caps  struct{ Blocked []string }
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Caps.Blocked) != 0 {
				t.Fatal(err, string(out))
			}
			original := source.After["owned-poll"]
			var metaObject protocol.JSONObject
			_ = metaObject.UnmarshalJSON(rawMain(meta))
			task := nativeRemoteAgentTaskData(metaObject, filepath.Join(home, "independent-go-owned.output"), float64(time.Now().UnixMilli()))
			var events []protocol.JSONObject
			_ = json.Unmarshal(rawMain(tc.events), &events)
			status := "running"
			if tc.archived {
				status = "archived"
			}
			poll := nativeRemoteAgentPoll{log: []protocol.JSONObject{}}
			got := poll.consume(task, ccrsessions.TaskPoll{Events: events, LastEventID: quoteMain("owned-last"), SessionStatus: quoteMain(status)}, float64(time.Now().UnixMilli()))
			if got.notification != "" {
				got.task.Set("notified", rawMain(true))
			}
			projection := func(v protocol.JSONObject) any {
				var obj map[string]any
				_ = json.Unmarshal(rawMain(v), &obj)
				delete(obj, "outputFile")
				delete(obj, "pollStartedAt")
				if _, ok := obj["endTime"]; ok {
					obj["endTime"] = "observed-original-clock"
				}
				return obj
			}
			if !reflect.DeepEqual(projection(got.task), projection(original)) {
				t.Fatal("Go original task business state differs", string(rawMain(got.task)), string(rawMain(original)), string(out))
			}
			if got.notification == "" {
				if len(source.Queue) != 0 {
					t.Fatal("unexpected original notification", string(out))
				}
			} else {
				if len(source.Queue) != 1 {
					t.Fatal("actual Source notification count", string(out))
				}
				want := strings.ReplaceAll(source.Queue[0].String("value"), original.String("outputFile"), "<owned-output>")
				actual := strings.ReplaceAll(got.notification, got.task.String("outputFile"), "<owned-output>")
				if want != actual {
					t.Fatal("actual Source queued notification differs", actual, want, string(out))
				}
			}
			t.Log("whole Source native task profile", tc.name, string(out))
		})
	}
}
