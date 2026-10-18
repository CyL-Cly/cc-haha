package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type sdkPermissionRootPeer struct {
	sync.Mutex
	input         *io.PipeWriter
	row           map[string]any
	abort         context.CancelFunc
	ack           chan struct{}
	request       protocol.JSONObject
	cancel        protocol.JSONObject
	cancelWritten chan struct{}
}

func (p *sdkPermissionRootPeer) Write(data []byte) (int, error) {
	p.Lock()
	defer p.Unlock()
	var frame protocol.JSONObject
	if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		return 0, err
	}
	if frame.String("type") == "control_response" {
		response, _ := frame.Object("response")
		if response.String("request_id") == "permission-init-P702" {
			select {
			case p.ack <- struct{}{}:
			default:
			}
		}
	}
	if frame.String("type") == "control_cancel_request" {
		p.cancel = frame.Clone()
		close(p.cancelWritten)
	}
	request, _ := frame.Object("request")
	if frame.String("type") == "control_request" && request.String("subtype") == "can_use_tool" {
		p.request = frame.Clone()
		if p.row["abort"] == true {
			p.abort()
			if p.row["abortThenEOF"] == true {
				_ = p.input.Close()
			} else if p.row["abortThenResponse"] == true {
				reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": p.row["response"]}})
				go func() { _, _ = p.input.Write(append(reply, '\n')) }()
			}
			return len(data), nil
		}
		if p.row["eof"] == true {
			_ = p.input.Close()
			return len(data), nil
		}
		response := map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": p.row["response"]}
		if p.row["peerError"] == true {
			response = map[string]any{"subtype": "error", "request_id": frame.String("request_id"), "error": "physical peer rejected"}
		}
		reply, _ := json.Marshal(map[string]any{"type": "control_response", "response": response})
		go func() { _, _ = p.input.Write(append(reply, '\n')) }()
	}
	return len(data), nil
}
func TestActualMainPrintSDKStdioPermissionsOriginalDecisionsRequestAbortAndEOF(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"name": "allow-empty-mobile-input", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "unknown": "strip"}},
		{"name": "allow-replacement", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{"actual": "replacement"}, "toolUseID": "original-id", "decisionClassification": "user_permanent"}},
		{"name": "allow-malformed-update-classification-fallback", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{}}, "decisionClassification": "not-valid"}},
		{"name": "allow-empty-updates", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{}}},
		{"name": "allow-real-session-rule-update", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{"type": "addRules", "rules": []any{map[string]any{"toolName": "mcp__sdk-p684__read_sdk", "unknown": "strip"}}, "behavior": "allow", "destination": "session", "unknown": "strip"}}}},
		{"name": "allow-real-session-mode-update", "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}},
		{"name": "deny", "response": map[string]any{"behavior": "deny", "message": "actual denial", "toolUseID": "permission-tool-P700", "decisionClassification": "user_reject"}},
		{"name": "deny-interrupt", "response": map[string]any{"behavior": "deny", "message": "actual interrupt", "interrupt": true}},
		{"name": "invalid-allow", "response": map[string]any{"behavior": "allow", "updatedInput": 7}},
		{"name": "invalid-deny", "response": map[string]any{"behavior": "deny", "message": 7}},
		{"name": "peer-error", "peerError": true, "response": map[string]any{}},
		{"name": "abort", "abort": true, "response": map[string]any{}},
		{"name": "abort-before-eof", "abort": true, "abortThenEOF": true, "response": map[string]any{}},
		{"name": "abort-before-late-response", "abort": true, "abortThenResponse": true, "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}},
		{"name": "EOF", "eof": true, "response": map[string]any{}},
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-stdio-permission.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	source.Dir = home
	source.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_STDIO_PERMISSION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	source.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": rows}))
	var sourceErr bytes.Buffer
	source.Stderr = &sourceErr
	data, err := source.Output()
	if err != nil {
		t.Fatal("actual Source SDK permission consumer", err, sourceErr.String())
	}
	var original struct {
		Rows []struct {
			Request, Decision, Cancel protocol.JSONObject
			ParentAborted             bool
			Pending                   int
			PermissionMode            string
			SessionAllowRules         json.RawMessage
		}
	}
	if err = json.Unmarshal(data, &original); err != nil || len(original.Rows) != len(rows) {
		t.Fatal(err, string(data))
	}
	for i, row := range rows {
		t.Run(row["name"].(string), func(t *testing.T) {
			f := worktreeMainFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			decisionContext, abort := context.WithCancel(context.Background())
			defer abort()
			peer := &sdkPermissionRootPeer{input: writer, row: row, abort: abort, ack: make(chan struct{}, 1), cancelWritten: make(chan struct{})}
			disabled := false
			var diagnostics bytes.Buffer
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: "stdio", ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: peer, Error: &diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			go func() { _, e := m.Run(ctx); done <- e }()
			defer func() {
				_ = writer.Close()
				_ = reader.Close()
				if !joined {
					<-done
				}
				_ = m.ShutdownSession(context.Background())
			}()
			go func() {
				_, _ = writer.Write([]byte(`{"type":"control_request","request_id":"permission-init-P702","request":{"subtype":"initialize"}}` + "\n"))
			}()
			select {
			case <-peer.ack:
			case e := <-done:
				t.Fatal("before initialize ACK", e, diagnostics.String())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			input := protocol.NewJSONObject(protocol.JSONField{Name: "actual", Value: quoteMain("physical")})
			var selected queryloop.ToolPermission
			var permissionState protocol.JSONObject
			receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				var e error
				selected, e = m.Host.ResolveNativeSDKPermission(decisionContext, scope, queryloop.RegisteredTool{Name: "mcp__sdk-p684__read_sdk", IsMCP: true}, &input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("permission-tool-P700"), CancelModel: abort})
				if e != nil {
					return e
				}
				permissionState, e = m.Host.NativePermissionContext(scope)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = receipt.Wait(); err != nil {
				t.Fatal("actual SDK owned permission", err, diagnostics.String())
			}
			if permissionState.String("mode") != original.Rows[i].PermissionMode {
				t.Fatal("actual permission update mode", permissionState.String("mode"), original.Rows[i].PermissionMode)
			}
			if expectedRules := original.Rows[i].SessionAllowRules; expectedRules != nil {
				rules, _ := permissionState.Object("alwaysAllowRules")
				var a, b any
				_ = json.Unmarshal(rules.Get("session"), &a)
				_ = json.Unmarshal(expectedRules, &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatal("actual session rule consumer", rules.Get("session"), expectedRules)
				}
			}
			decision := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: quoteMain(selected.Behavior)}, protocol.JSONField{Name: "message", Value: selected.Message}, protocol.JSONField{Name: "decisionReason", Value: selected.DecisionReason}, protocol.JSONField{Name: "updatedInput", Value: nil})
			if selected.UpdatedInput != nil {
				decision.Set("updatedInput", rawMain(selected.UpdatedInput))
			}
			expected := original.Rows[i].Decision
			for _, key := range []string{"behavior", "message", "decisionReason", "updatedInput"} {
				var a, b any
				_ = json.Unmarshal(decision.Get(key), &a)
				_ = json.Unmarshal(expected.Get(key), &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("actual Source decision field %s\nGo %s\nSource %s", key, decision.Get(key), expected.Get(key))
				}
			}
			// Permission resolution confirms FIFO admission. Like Source's outbound
			// next(), wait for the actual cancellation consumer before sampling stdout.
			if row["abort"] == true {
				select {
				case <-peer.cancelWritten:
				case <-ctx.Done():
					t.Fatal("actual cancellation frame was not written", ctx.Err())
				}
			}
			peer.Lock()
			request := peer.request.Clone()
			cancelFrame := peer.cancel.Clone()
			peer.Unlock()
			request.Set("request_id", quoteMain("<request>"))
			if cancelFrame.IsObject() {
				cancelFrame.Set("request_id", quoteMain("<request>"))
			}
			var a, b any
			_ = json.Unmarshal(rawMain(request), &a)
			_ = json.Unmarshal(rawMain(original.Rows[i].Request), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatal("actual Source permission request", rawMain(request), rawMain(original.Rows[i].Request))
			}
			if row["abort"] == true {
				_ = json.Unmarshal(rawMain(cancelFrame), &a)
				_ = json.Unmarshal(rawMain(original.Rows[i].Cancel), &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatal("actual cancellation frame", rawMain(cancelFrame), rawMain(original.Rows[i].Cancel))
				}
			}
			if (decisionContext.Err() != nil) != original.Rows[i].ParentAborted {
				t.Fatal("original parent model abort state", decisionContext.Err(), original.Rows[i].ParentAborted)
			}
			_ = writer.Close()
			err = <-done
			joined = true
			if err != nil {
				t.Fatal("actual EOF/owner drain", err, diagnostics.String())
			}
		})
	}
}
