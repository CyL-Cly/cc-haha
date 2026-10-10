package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"golang.org/x/net/websocket"
)

func TestActualDesktopFileHistoryBeforeAfterFrozenDiffAndRewind(t *testing.T) {
	const before = "ORIGINAL_PHYSICAL_CHECKPOINT_893A\n"
	const after = "EDITED_PHYSICAL_CHECKPOINT_4DB9\n"
	var calls atomic.Int32
	var path string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			response(w, "history-read-before", "Read", map[string]any{"file_path": path}, "")
		case 2:
			response(w, "history-edit", "Edit", map[string]any{"file_path": path, "old_string": strings.TrimSpace(before), "new_string": strings.TrimSpace(after)}, "")
		case 3:
			response(w, "history-read-after", "Read", map[string]any{"file_path": path}, "")
		case 4:
			response(w, "history-final", "", nil, "FILE_HISTORY_E2E_OK")
		default:
			t.Error("unexpected new model loop")
			http.Error(w, "extra call", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = nil
	o.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	o.Native.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	path = filepath.Join(o.Native.CWD, "checkpoint.txt")
	if e := os.WriteFile(path, []byte(before), 0640); e != nil {
		t.Fatal(e)
	}
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path string, body any, expected int) json.RawMessage {
		t.Helper()
		var input io.Reader
		if body != nil {
			input = bytes.NewReader(raw(body))
		}
		r, e := http.NewRequest(method, host.URL+path, input)
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		res, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != expected {
			t.Fatal(res.StatusCode, e, string(data))
		}
		return data
	}
	id := obj(request("POST", "/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}, 201)).String("sessionId")
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if e != nil {
		t.Fatal(e)
	}
	ws.SetDeadline(time.Now().Add(20 * time.Second))
	if e = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Read, edit and verify the physical checkpoint file."}))); e != nil {
		t.Fatal(e)
	}
	complete := false
	for {
		var frame string
		if e = websocket.Message.Receive(ws, &frame); e != nil {
			t.Fatal(e)
		}
		v := obj([]byte(frame))
		if v.String("type") == "error" {
			t.Fatal(frame)
		}
		if v.String("type") == "message_complete" {
			complete = true
		}
		if complete && v.String("type") == "session_state" && v.String("turnState") == "idle" {
			break
		}
	}
	ws.Close()
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != after {
		t.Fatal("actual Edit failed", e, string(data))
	}
	checkpointRaw := request("GET", "/api/sessions/"+id+"/turn-checkpoints?frozen=true", nil, 200)
	var checkpoint struct {
		Checkpoints []sessionrewind.TurnCheckpoint
	}
	if e = json.Unmarshal(checkpointRaw, &checkpoint); e != nil || len(checkpoint.Checkpoints) != 1 {
		t.Fatal(e, string(checkpointRaw))
	}
	turn := checkpoint.Checkpoints[0]
	if !turn.RestoreAvailable || !turn.Code.Available || len(turn.Code.FilesChanged) != 1 || turn.Code.FilesChanged[0] != path {
		t.Fatal("actual snapshots did not support frozen restore", string(checkpointRaw))
	}
	// Freeze the after boundary: later external edits must not change this
	// turn's diff or be silently used as the stored after content.
	if e = os.WriteFile(path, []byte("EXTERNAL_LATER_EDIT\n"), 0640); e != nil {
		t.Fatal(e)
	}
	diff := request("GET", "/api/sessions/"+id+"/turn-checkpoints/diff?targetUserMessageId="+turn.Target.TargetUserMessageID+"&path="+url.QueryEscape(path)+"&frozen=true", nil, 200)
	if !bytes.Contains(diff, []byte(strings.TrimSpace(before))) || !bytes.Contains(diff, []byte(strings.TrimSpace(after))) || bytes.Contains(diff, []byte("EXTERNAL_LATER_EDIT")) {
		t.Fatal("frozen checkpoint used today's disk", string(diff))
	}
	request("POST", "/api/sessions/"+id+"/rewind", map[string]any{"targetUserMessageId": turn.Target.TargetUserMessageID, "mode": "both", "dryRun": true}, 200)
	request("POST", "/api/sessions/"+id+"/rewind", map[string]any{"targetUserMessageId": turn.Target.TargetUserMessageID, "mode": "both"}, 200)
	data, e = os.ReadFile(path)
	if e != nil || string(data) != before {
		t.Fatal("physical restore failed", e, string(data))
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0640 {
		t.Fatal("restore mode changed", e)
	}
}
