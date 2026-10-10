package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualSourceSDKWorktreeCallbackRequestsSchemaAbortAndEOF(t *testing.T) {
	home, canonicalError := filepath.EvalSymlinks(t.TempDir())
	if canonicalError != nil {
		t.Fatal(canonicalError)
	}
	sid := "62900000-0000-4000-8000-000000000629"
	path := filepath.Join(home, "tree")
	rows := []map[string]any{{"name": "create", "id": "create629", "event": "WorktreeCreate", "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": path}, "unknown": "strip"}}, {"name": "remove", "id": "remove629", "event": "WorktreeRemove", "response": map[string]any{}}, {"name": "async", "id": "async629", "event": "WorktreeCreate", "response": map[string]any{"async": true}}, {"name": "peer-error", "id": "error629", "event": "WorktreeCreate", "error": true}, {"name": "invalid-schema", "id": "invalid629", "event": "WorktreeCreate", "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "WorktreeCreate", "worktreePath": 7}}}, {"name": "EOF", "id": "eof629", "event": "WorktreeRemove", "eof": true}, {"name": "abort", "id": "abort629", "event": "WorktreeCreate", "abort": true}}
	rows = append(rows, map[string]any{"name": "replay-success-camel-duplicate-orphan", "id": "replay629", "event": "WorktreeRemove", "response": map[string]any{}, "replay": true, "camel": true, "duplicate": true, "orphan": true}, map[string]any{"name": "replay-error", "id": "error-replay629", "event": "WorktreeRemove", "error": true, "replay": true})
	fixture, _ := filepath.Abs("../../testdata/native-sdk-worktree-callback.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_WORKTREE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"sid": sid, "path": path, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Name    string
			Request protocol.JSONObject
			Result  []queryhooks.OutsideResult
			Cancel  protocol.JSONObject
			Parsed  []protocol.JSONObject
		}
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		t.Run(row["name"].(string), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			var host *Host
			frames := []protocol.JSONObject{}
			callctx, abort := context.WithCancel(context.Background())
			defer abort()
			host = &Host{options: Options{Owner: owner, UUID: func() string { return "actual-request629" }, OnPhase: func(string) {}, Output: Output{Enqueue: func(data []byte) error {
				var event protocol.JSONObject
				if err := event.UnmarshalJSON(data); err != nil {
					return err
				}
				frames = append(frames, event.Clone())
				if event.String("type") == "control_cancel_request" {
					return nil
				}
				_, err := owner.SubmitIndependent(context.Background(), cooperative.Input, func(scope *cooperative.Scope) error {
					if row["abort"] == true {
						abort()
						return nil
					}
					if row["eof"] == true {
						return host.closeNativeSDKHookInput(scope)
					}
					response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: quote("actual-request629")}, protocol.JSONField{Name: "response", Value: raw(row["response"])})
					if row["error"] == true {
						response.Set("subtype", quote("error"))
						response.Set("error", quote("private peer error"))
					}
					_, err := host.nativeSDKHookResponse(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
					return err
				})
				return err
			}}}, sdkHooks: &nativeSDKHookState{open: true, pending: map[string]*OwnerFuture[protocol.JSONObject]{}}}
			fields := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quote(sid)}, protocol.JSONField{Name: "transcript_path", Value: quote("")}, protocol.JSONField{Name: "cwd", Value: quote(home)}, protocol.JSONField{Name: "hook_event_name", Value: quote(row["event"].(string))})
			if row["event"] == "WorktreeCreate" {
				fields.Set("name", quote("agent-a1234567"))
			} else {
				fields.Set("worktree_path", quote(path))
			}
			var result protocol.JSONObject
			err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				result, err = host.runNativeSDKHookCallback(callctx, queryhooks.Context{Scope: scope, HookID: "actual-tool629"}, protocol.NewJSONObject(protocol.JSONField{Name: "callback_id", Value: quote(row["id"].(string))}), fields)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) == 0 {
				t.Fatal("no actual SDK callback frame")
			}
			expected := source.Rows[i].Request.Clone()
			expected.Set("request_id", quote("actual-request629"))
			request, _ := expected.Object("request")
			request.Set("tool_use_id", quote("actual-tool629"))
			expected.Set("request", raw(request))
			decode := func(v protocol.JSONObject) any {
				b, _ := v.MarshalJSON()
				var out any
				_ = json.Unmarshal(b, &out)
				return out
			}
			if !reflect.DeepEqual(decode(expected), decode(frames[0])) {
				t.Fatal("Source request", string(raw(expected)), "Go", string(raw(frames[0])))
			}
			if len(source.Rows[i].Result) != 1 {
				t.Fatal("Source result", source.Rows[i].Result)
			}
			want := source.Rows[i].Result[0]
			output := ""
			if specific, ok := result.Object("hookSpecificOutput"); ok && specific.String("hookEventName") == "WorktreeCreate" {
				output = specific.String("worktreePath")
			}
			var sourceOutput string
			_ = json.Unmarshal(want.Output, &sourceOutput)
			if output != sourceOutput {
				t.Fatal("callback result", output, sourceOutput)
			}
			if row["abort"] == true && (len(frames) != 2 || frames[1].String("type") != "control_cancel_request") {
				t.Fatal("actual cancel frame", frames)
			}
			if row["replay"] == true {
				input := &PlainStructuredInput{owner: owner, allowSDKInitialize: true, sandboxReplay: true, sandboxResponse: func(next *cooperative.Scope, event protocol.JSONObject) (bool, error) {
					normalizeNativeSDKControlKeys(&event)
					return true, nil
				}}
				packet := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(map[string]any{"subtype": "success", "requestId": "legacy629", "response": map[string]any{}})})
				if row["error"] == true {
					packet.Set("response", raw(map[string]any{"subtype": "error", "requestId": "legacy629", "error": "private peer error"}))
				}
				var parsed parsedLine
				err = owner.Run(ctx, func(next *cooperative.Scope) error {
					future, e := input.processLine(next, string(raw(packet)))
					if e != nil {
						return e
					}
					parsed, e = future.Await(next)
					return e
				})
				if err != nil {
					t.Fatal(err)
				}
				expectedReplay := len(source.Rows[i].Parsed) == 1
				if parsed.present != expectedReplay {
					t.Fatal("Source actual control replay", parsed.present, source.Rows[i].Parsed)
				}
				if parsed.present {
					response, _ := parsed.message.Object("response")
					if response.String("request_id") != "legacy629" || response.Get("requestId") != nil {
						t.Fatal("actual canonical response key normalization", raw(parsed.message))
					}
				}
			}
			if len(host.sdkHooks.pending) != 0 {
				t.Fatal("pending callback leaked")
			}
		})
	}
}
