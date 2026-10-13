package application

import (
	"bytes"
	"context"
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

func TestDesktopPlanApprovalUpdatesSamePermissionStateAndExecutesActualFiles(t *testing.T) {
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
			if !bytes.Contains(body, []byte("## Exited Plan Mode")) || !bytes.Contains(body, []byte("Approved Plan")) {
				t.Errorf("approval did not resume actual model with plan exit: %s", body)
			}
			response(w, "desktop-plan-execute", "Write", map[string]any{"file_path": filepath.Join(work, "plan-result.txt"), "content": "PLAN_DESKTOP_STATE_OK"}, "")
		case 5:
			response(w, "desktop-plan-read", "Read", map[string]any{"file_path": filepath.Join(work, "plan-result.txt")}, "")
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
	send(map[string]any{"type": "permission_response", "requestId": permission.String("requestId"), "allowed": true, "permissionUpdates": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}})
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
	metadata, err := host.Client().Get(host.URL + "/api/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Body.Close()
	_, _ = io.ReadAll(metadata.Body)
}
