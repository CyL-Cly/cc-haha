package application

import (
	"bytes"
	"context"
	"encoding/json"
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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This full factory test supplies only the provider HTTP protocol fixture.
// A separate official-provider UI run is required for live evidence.
func TestActualPublicPairingUsesSameDesktopHarnessAndWritesCanonicalArtifact(t *testing.T) {
	var calls atomic.Int32
	var target string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "public-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_PUBLIC_GO_ARTIFACT"}, "")
		case 2:
			response(w, "public-read", "Read", map[string]any{"file_path": target}, "")
		default:
			if !bytes.Contains(data, []byte("ACTUAL_PUBLIC_GO_ARTIFACT")) {
				t.Error("real Read result missing from shared ASK")
			}
			response(w, "public-complete", "", nil, "ACTUAL_PUBLIC_GO_COMPLETED")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	// An existing Desktop auto-mode opt-in must not prevent an ordinary
	// bypassPermissions conversation from constructing its actual SDK host.
	if e := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"skipAutoPermissionPrompt":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	o.Arguments.Tools = []string{"Read,Write"}
	o.Environment[access.LocalAccessTokenEnv] = "private-public-fixture"
	target = filepath.Join(o.WorkDir, "public-real-artifact.txt")
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	// Account status and both standalone callback routing and the main harness
	// come from this actual desktop factory, without starting a model turn.
	accountRequest, _ := http.NewRequest("GET", host.URL+"/api/haha-openai-oauth", nil)
	accountRequest.Header.Set("Authorization", "Bearer private-public-fixture")
	accountResponse, e := host.Client().Do(accountRequest)
	if e != nil {
		t.Fatal(e)
	}
	accountBody, _ := io.ReadAll(accountResponse.Body)
	_ = accountResponse.Body.Close()
	if accountResponse.StatusCode != 200 || string(obj(accountBody).Get("loggedIn")) != "false" {
		t.Fatalf("actual OpenAI factory status %d %s", accountResponse.StatusCode, accountBody)
	}
	callbackResponse, e := host.Client().Get(host.URL + "/auth/callback")
	if e != nil {
		t.Fatal(e)
	}
	callbackBody, _ := io.ReadAll(callbackResponse.Body)
	_ = callbackResponse.Body.Close()
	if callbackResponse.StatusCode != 200 || !bytes.Contains(callbackBody, []byte("OpenAI Login Failed")) || !bytes.Contains(callbackBody, []byte("Missing code or state parameter")) {
		t.Fatal("standalone OpenAI callback fell into auth or SPA fallback")
	}
	r, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	r.Header.Set("Authorization", "Bearer private-public-fixture")
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
	control := func(route string, body any) protocol.JSONObject {
		t.Helper()
		req, _ := http.NewRequest("POST", host.URL+"/api/public-access/"+route, bytes.NewReader(raw(body)))
		req.Header.Set("Authorization", "Bearer private-public-fixture")
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("control %s %d %s", route, res.StatusCode, payload)
		}
		return obj(payload)
	}
	enabled := control("enable", map[string]string{"publicUrl": "https://fixture.invalid"})
	var port int
	_ = json.Unmarshal(enabled.Get("port"), &port)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	remote := func(route string, body any) *http.Response {
		req, _ := http.NewRequest("POST", base+route, bytes.NewReader(raw(body)))
		req.Header.Set("Origin", "https://fixture.invalid")
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	code := control("pairing", map[string]any{}).String("secret")
	pendingRes := remote("/api/public-access/pair", map[string]string{"secret": code, "name": "actual desktop remote"})
	pendingData, _ := io.ReadAll(pendingRes.Body)
	_ = pendingRes.Body.Close()
	if pendingRes.StatusCode != 200 {
		t.Fatal(string(pendingData))
	}
	pending := obj(pendingData)
	control("approve", map[string]string{"id": pending.String("id")})
	claim := remote("/api/public-access/claim", map[string]string{"id": pending.String("id"), "claimSecret": pending.String("claimSecret")})
	cookie := strings.Split(claim.Header.Get("Set-Cookie"), ";")[0]
	_ = claim.Body.Close()
	if claim.StatusCode != 200 || cookie == "" {
		t.Fatal("pair approval failed")
	}
	config, _ := websocket.NewConfig("ws"+strings.TrimPrefix(base, "http")+"/ws/"+id, "https://fixture.invalid")
	config.Header.Set("Cookie", cookie)
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
		case "message_complete":
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
	if e != nil || string(artifact) != "ACTUAL_PUBLIC_GO_ARTIFACT" {
		t.Fatal("physical Write/Read failed", e, string(artifact))
	}
	entries, e := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", id+".jsonl"))
	if e != nil || len(entries) != 1 {
		t.Fatal(e, entries)
	}
	transcript, e := os.ReadFile(entries[0])
	if e != nil || !bytes.Contains(transcript, []byte("ACTUAL_PUBLIC_GO_COMPLETED")) || !bytes.Contains(transcript, []byte(`"name":"Write"`)) {
		t.Fatal("canonical actual history missing", e, string(transcript))
	}
}
