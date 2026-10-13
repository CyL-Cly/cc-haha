package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func TestDesktopPlanRuntimeApprovalAcknowledgesPersistsAllowsAndChangesNextASK(t *testing.T) {
	var calls atomic.Int32
	var work string
	var planPath string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "desktop-plan-enter", "EnterPlanMode", map[string]any{}, "")
		case 2:
			match := regexp.MustCompile(`No plan file exists yet\. You should create your plan at ([^ ]+) using the Write tool\.`).FindSubmatch(body)
			if len(match) != 2 {
				t.Errorf("actual plan reminder missing %s", body)
				http.Error(w, "no plan reminder", 500)
				return
			}
			planPath = string(match[1])
			response(w, "desktop-plan-write", "Write", map[string]any{"file_path": planPath, "content": "# Actual plan\nWrite a test marker and read it."}, "")
		case 3:
			response(w, "desktop-plan-exit", "ExitPlanMode", map[string]any{}, "")
		case 4:
			if !bytes.Contains(body, []byte(`"model":"fixture-model"`)) {
				t.Errorf("same ASK changed its originally captured model: %s", body)
			}
			if !bytes.Contains(body, []byte("## Exited Plan Mode")) || !bytes.Contains(body, []byte("Approved Plan")) {
				t.Errorf("approval did not resume actual model with plan exit: %s", body)
			}
			response(w, "desktop-plan-execute", "Write", map[string]any{"file_path": filepath.Join(work, "plan-result.txt"), "content": "PLAN_DESKTOP_STATE_OK"}, "")
		case 5:
			response(w, "desktop-plan-read", "Read", map[string]any{"file_path": filepath.Join(work, "plan-result.txt")}, "")
		case 7:
			if !bytes.Contains(body, []byte(`"model":"fixture-model-2"`)) {
				t.Errorf("next ASK did not read acknowledged model: %s", body)
			}
			response(w, "desktop-plan-next-ask", "", nil, "NEXT ASK MODEL OK")
		default:
			response(w, "desktop-plan-final", "", nil, "PLAN_DESKTOP_STATE_OK")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	work = o.WorkDir
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"EnterPlanMode,ExitPlanMode,Write,Read"}
	s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	created, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": work, "permissionMode": "default"})))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	if created.StatusCode != 201 {
		t.Fatal(created.StatusCode, string(body))
	}
	id := obj(body).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	send := func(v any) {
		t.Helper()
		if e := websocket.Message.Send(ws, string(raw(v))); e != nil {
			t.Fatal(e)
		}
	}
	modes := []string{}
	receive := func(kind string) protocol.JSONObject {
		t.Helper()
		for i := 0; i < 200; i++ {
			var data string
			if e := websocket.Message.Receive(ws, &data); e != nil {
				t.Fatal(e)
			}
			frame := obj([]byte(data))
			if kind != "permission_request" && frame.String("type") == "permission_request" {
				t.Fatalf("unexpected extra permission: %s", data)
			}
			if frame.String("type") == "error" {
				t.Fatal("actual desktop plan failed", data)
			}
			if frame.String("type") == "permission_mode_changed" {
				modes = append(modes, frame.String("mode"))
			}
			if frame.String("type") == kind {
				return frame
			}
		}
		t.Fatal("missing frame", kind)
		return protocol.JSONObject{}
	}
	receive("connected")
	send(map[string]any{"type": "user_message", "content": "Plan the test, ask for approval, then write and read the marker."})
	permission := receive("permission_request")
	if permission.String("toolName") != "ExitPlanMode" || calls.Load() != 3 || permission.String("description") != "Exit plan mode?" {
		t.Fatal("actual plan Write wrongly asked or approval lost provenance", permission, calls.Load())
	}
	var input protocol.JSONObject
	_ = input.UnmarshalJSON(permission.Get("input"))
	if input.String("plan") != "# Actual plan\nWrite a test marker and read it." || input.String("planFilePath") != planPath {
		t.Fatal("approval did not carry the actual disk plan", input)
	}
	send(map[string]any{"type": "permission_response", "requestId": permission.String("requestId"), "allowed": true, "runtimeOverride": map[string]any{"providerId": nil, "modelId": "fixture-model-2"}, "permissionUpdates": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}})
	applied := receive("runtime_config_applied")
	if applied.String("modelId") != "fixture-model-2" {
		t.Fatal("accepted runtime receipt lost original selected model", applied)
	}
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	data, err := os.ReadFile(filepath.Join(work, "plan-result.txt"))
	if err != nil || string(data) != "PLAN_DESKTOP_STATE_OK" {
		t.Fatal("actual approved execution was not written", string(data), err)
	}
	if calls.Load() != 6 || len(modes) != 2 || modes[0] != "plan" || modes[1] != "acceptEdits" {
		t.Fatal("shared permission transition or next tools needed extra approval", calls.Load(), modes)
	}
	if _, err := os.Stat(planPath); err != nil {
		t.Fatal("actual plan file missing", err)
	}
	send(map[string]any{"type": "user_message", "content": "Report marker again without tools."})
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	if calls.Load() != 7 {
		t.Fatal("next ASK did not use the same live session", calls.Load())
	}
	metadata, err := host.Client().Get(host.URL + "/api/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Body.Close()
	_, _ = io.ReadAll(metadata.Body)
}

// Every branch below starts the actual MainPrint producer and stops at its real
// permission. Failure injection is confined to declared host metadata/caller
// capabilities; it does not replace model, approval, tool or Owner business.
func TestDesktopPlanRuntimeApprovalFailureRetainsActualPermission(t *testing.T) {
	for _, name := range []string{"invalid-model-retry", "nonobject-retry", "ack-caller-cancel-retry", "persist-failure-retry", "same-value", "deny-ignores-override", "mismatched-id", "closed-engine", "foreign-engine", "side-chat-provider"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch calls.Add(1) {
				case 1:
					response(w, "boundary-enter", "EnterPlanMode", map[string]any{}, "")
				case 2:
					match := regexp.MustCompile(`No plan file exists yet\. You should create your plan at ([^ ]+) using the Write tool\.`).FindSubmatch(body)
					if len(match) != 2 {
						t.Error("actual Source-compatible plan reminder missing")
						http.Error(w, "missing plan", 400)
						return
					}
					response(w, "boundary-plan", "Write", map[string]any{"file_path": string(match[1]), "content": "# Private plan\nExecute after approval."}, "")
				case 3:
					response(w, "boundary-exit", "ExitPlanMode", map[string]any{}, "")
				default:
					response(w, "boundary-final", "", nil, "BOUNDARY DONE")
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.PermissionMode = "default"
			o.Arguments.Tools = []string{"EnterPlanMode,ExitPlanMode,Write"}
			frames := make(chan protocol.JSONObject, 256)
			metadata := []protocol.JSONObject{}
			failingMetadata := name == "persist-failure-retry"
			live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir, UpdateLaunchMetadata: func(_ context.Context, data json.RawMessage) error {
				// This actual callback may run during plan mode changes too. Only
				// runtime persistence is the original declared failing host leaf.
				value := obj(data)
				if value.Get("runtimeModelId") != nil {
					if calls.Load() != 3 {
						t.Error("approval was released before persistence callback")
					}
					metadata = append(metadata, value)
					if failingMetadata {
						return errors.New("original metadata persistence rejected")
					}
				}
				return nil
			}}, func(data json.RawMessage) { frames <- obj(data) })
			if err != nil {
				t.Fatal(err)
			}
			d := live.(*DesktopSession)
			defer d.Close(context.Background())
			done := make(chan error, 1)
			go func() { done <- d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Plan and ask."})) }()
			next := func(kind string) protocol.JSONObject {
				t.Helper()
				for {
					select {
					case frame := <-frames:
						if frame.String("type") == kind {
							return frame
						}
					case err := <-done:
						t.Fatalf("actual turn settled before %s: %v", kind, err)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			// Only a client sync_state publishes a snapshot. Keep the actual
			// completion frame and handler settlement as separate barriers.
			waitForSessionState := func() protocol.JSONObject {
				t.Helper()
				for {
					select {
					case frame := <-frames:
						if frame.String("type") == "error" {
							t.Fatal("actual desktop plan failed", frame)
						}
						if frame.String("type") == "session_state" {
							return frame
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			p := next("permission_request")
			if p.String("toolName") != "ExitPlanMode" || calls.Load() != 3 {
				t.Fatal("not the actual plan permission", p, calls.Load())
			}
			before := d.currentEngine()
			approval := map[string]any{"type": "permission_response", "requestId": p.String("requestId"), "allowed": true, "runtimeOverride": map[string]any{"providerId": nil, "modelId": "fixture-model-2"}}
			retry := true
			switch name {
			case "invalid-model-retry":
				approval["runtimeOverride"] = map[string]any{"providerId": nil, "modelId": " "}
			case "nonobject-retry":
				approval["runtimeOverride"] = true
			case "ack-caller-cancel-retry":
				cancelled, stop := context.WithCancel(ctx)
				stop()
				if err := d.Handle(cancelled, raw(approval)); err != nil {
					t.Fatal(err)
				}
				f := next("error")
				if f.String("code") != "RUNTIME_CONFIG_INVALID" || len(before.PendingPermissions()) != 1 || len(metadata) != 0 {
					t.Fatal("actual ACK failure released permission or persisted", f, metadata)
				}
				approval["runtimeOverride"] = map[string]any{"providerId": nil, "modelId": "fixture-model-2"}
			case "same-value":
				approval["runtimeOverride"] = map[string]any{"providerId": nil, "modelId": "fixture-model"}
				retry = false
			case "deny-ignores-override":
				approval["allowed"] = false
				retry = false
			case "mismatched-id":
				approval["requestId"] = "original-not-pending"
				if err := d.Handle(ctx, raw(approval)); err == nil || len(before.PendingPermissions()) != 1 {
					t.Fatal("unknown ID must not apply an override", err)
				}
				if len(metadata) != 0 || d.options.Arguments.Model != "fixture-model" {
					t.Fatal("mismatched ID changed runtime", metadata)
				}
				approval["requestId"] = p.String("requestId")
			case "closed-engine":
				d.mu.Lock()
				d.closed = true
				d.mu.Unlock()
				if err := d.approvePlanRuntime(ctx, obj(raw(approval)), before); !errors.Is(err, context.Canceled) || len(before.PendingPermissions()) != 1 {
					t.Fatal("closed approval admitted", err)
				}
				d.mu.Lock()
				d.closed = false
				d.mu.Unlock()
			case "foreign-engine":
				if err := d.approvePlanRuntime(ctx, obj(raw(approval)), &Session{}); !errors.Is(err, context.Canceled) {
					t.Fatal("obsolete engine approval admitted", err)
				}
			case "side-chat-provider":
				d.sideChat = true
				approval["runtimeOverride"] = map[string]any{"providerId": "other-original-provider", "modelId": "fixture-model-2"}
			}
			if name != "ack-caller-cancel-retry" {
				if err := d.Handle(ctx, raw(approval)); err != nil {
					t.Fatal(err)
				}
			} else if err := d.Handle(ctx, raw(approval)); err != nil {
				t.Fatal(err)
			}
			if name == "invalid-model-retry" || name == "nonobject-retry" || name == "persist-failure-retry" || name == "side-chat-provider" {
				f := next("error")
				code := "RUNTIME_CONFIG_INVALID"
				if name == "side-chat-provider" {
					code = "SIDE_CHAT_RUNTIME_RESTART_UNAVAILABLE"
				}
				if f.String("code") != code || len(before.PendingPermissions()) != 1 || calls.Load() != 3 {
					t.Fatal("failure did not preserve retryable pending", f, calls.Load())
				}
				d.sideChat = false
				failingMetadata = false
				if name == "persist-failure-retry" && (d.options.Arguments.Model != "fixture-model-2" || len(metadata) != 1) {
					t.Fatal("Source commits runtime selection before persistence await", metadata, d.options.Arguments.Model)
				}
				approval["runtimeOverride"] = map[string]any{"providerId": nil, "modelId": "fixture-model-2"}
				if err := d.Handle(ctx, raw(approval)); err != nil {
					t.Fatal(err)
				}
			}
			completed := false
			for !completed {
				select {
				case frame := <-frames:
					if frame.String("type") == "error" {
						t.Fatal("actual desktop plan failed", frame)
					}
					completed = frame.String("type") == "message_complete"
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// The WebSocket sync_state branch calls the live Snapshot consumer,
			// rather than forwarding that control to LiveSession.Handle.
			snapshot, err := d.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, frame := range snapshot {
				frames <- obj(frame)
			}
			assertDesktopIdleSnapshot(t, waitForSessionState())
			if d.currentEngine() != before || calls.Load() != 4 {
				t.Fatal("approval restarted engine or made extra attempts", calls.Load())
			}
			if name == "persist-failure-retry" && len(metadata) != 1 {
				t.Fatal("same-selection retry incorrectly repeated failed persistence", len(metadata))
			}
			if !retry && (len(metadata) != 0 || d.options.Arguments.Model != "fixture-model") {
				t.Fatal("same value/deny applied runtime override", metadata, d.options.Arguments.Model)
			}
		})
	}
}

func TestDesktopPlanRuntimeApprovalNonExitIgnoresOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var calls atomic.Int32
	var work string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			response(w, "nonexit-write", "Write", map[string]any{"file_path": filepath.Join(work, "original.txt"), "content": "ORIGINAL_WRITE"}, "")
		} else {
			if !bytes.Contains(body, []byte(`"model":"fixture-model"`)) {
				t.Error("non-ExitPlanMode override affected model")
			}
			response(w, "nonexit-final", "", nil, "NONEXIT DONE")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	work = o.WorkDir
	o.Arguments.PermissionMode = "default"
	frames := make(chan protocol.JSONObject, 256)
	var metadata atomic.Int32
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: work, UpdateLaunchMetadata: func(_ context.Context, data json.RawMessage) error {
		if obj(data).Get("runtimeModelId") != nil {
			metadata.Add(1)
		}
		return nil
	}}, func(data json.RawMessage) { frames <- obj(data) })
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	defer d.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Write the original private file."}))
	}()
	for {
		select {
		case f := <-frames:
			if f.String("type") == "permission_request" {
				if f.String("toolName") != "Write" {
					t.Fatal(f)
				}
				err = d.Handle(ctx, raw(map[string]any{"type": "permission_response", "requestId": f.String("requestId"), "allowed": true, "runtimeOverride": map[string]any{"providerId": nil, "modelId": "fixture-model-2"}}))
				if err != nil {
					t.Fatal(err)
				}
			}
		case err := <-done:
			if err != nil || metadata.Load() != 0 || d.options.Arguments.Model != "fixture-model" {
				t.Fatal(err, metadata.Load(), d.options.Arguments.Model)
			}
			value, err := os.ReadFile(filepath.Join(work, "original.txt"))
			if err != nil || string(value) != "ORIGINAL_WRITE" || calls.Load() != 2 {
				t.Fatal(string(value), err, calls.Load())
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
