package application

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualPetFollowupUsesSameDesktopHarnessAndWritesCanonicalArtifact(t *testing.T) {
	var calls atomic.Int32
	var target string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "pet-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_PET_GO_ARTIFACT"}, "")
		case 2:
			response(w, "pet-read", "Read", map[string]any{"file_path": target}, "")
		default:
			if !bytes.Contains(data, []byte("ACTUAL_PET_GO_ARTIFACT")) {
				t.Error("real Read result missing from shared ASK")
			}
			response(w, "pet-complete", "", nil, "ACTUAL_PET_GO_COMPLETED")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = []string{"Read,Write"}
	o.Environment[access.PetAccessTokenEnv] = "private-pet-fixture"
	target = filepath.Join(o.WorkDir, "pet-real-artifact.txt")
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	r, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	r.Header.Set("Authorization", "Bearer fake-only-loopback")
	created, e := host.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(created.Body)
	_ = created.Body.Close()
	if created.StatusCode != 201 {
		t.Fatal(created.StatusCode, string(data))
	}
	id := obj(data).String("sessionId")
	config, _ := websocket.NewConfig("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id+"?token=private-pet-fixture", host.URL)
	ws, e := websocket.DialConfig(config)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(12 * time.Second))
	if e = websocket.Message.Send(ws, `{"type":"user_message","content":"Write the actual artifact and Read it back"}`); e != nil {
		t.Fatal(e)
	}
	completed, idle := false, false
	for i := 0; i < 100; i++ {
		var text string
		if e = websocket.Message.Receive(ws, &text); e != nil {
			t.Fatal(e)
		}
		frame := obj([]byte(text))
		switch frame.String("type") {
		case "error":
			t.Fatal(text)
		case "content_delta", "content_start", "tool_result", "permission_request":
			t.Fatal("pet leaked full desktop frame", text)
		case "message_complete":
			if string(frame.Get("usage")) != `{"input_tokens":0,"output_tokens":0}` {
				t.Fatal("usage projection", text)
			}
			completed = true
		case "session_state":
			if completed && frame.String("turnState") == "idle" {
				idle = true
			}
		}
		if idle {
			break
		}
	}
	if !completed || !idle || calls.Load() != 3 {
		t.Fatalf("actual completed%v idle%v POST%d", completed, idle, calls.Load())
	}
	artifact, e := os.ReadFile(target)
	if e != nil || string(artifact) != "ACTUAL_PET_GO_ARTIFACT" {
		t.Fatal("physical Write/Read failed", e, string(artifact))
	}
	entries, e := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", id+".jsonl"))
	if e != nil || len(entries) != 1 {
		t.Fatal(e, entries)
	}
	transcript, e := os.ReadFile(entries[0])
	if e != nil || !bytes.Contains(transcript, []byte("ACTUAL_PET_GO_COMPLETED")) || !bytes.Contains(transcript, []byte(`"name":"Write"`)) {
		t.Fatal("canonical actual history missing", e, string(transcript))
	}
}
