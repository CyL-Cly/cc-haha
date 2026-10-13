package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestActualSourceAndPublicSDKURLV1UnauthorizedPersistenceExitsAndCancelsRuntime(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-v1-fatal-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=2", "-tags=native_process_fixture", "-o", binary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		modelCalls.Add(1)
		<-r.Context().Done()
	}))
	defer model.Close()
	for _, source := range []bool{true, false} {
		t.Run(map[bool]string{true: "actual-source-storage", false: "public-go-cli"}[source], func(t *testing.T) {
			home := resumeErrorHome(t, root, map[bool]string{true: "source-v1-fatal", false: "go-v1-fatal"}[source])
			var puts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ingress/private" {
					if r.Header.Get("Authorization") != "Bearer fake-v1-fatal" {
						t.Error("unexpected ingress authentication")
					}
					if r.Method == http.MethodGet {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"loglines":[{"type":"user","uuid":"old-v1-user","parentUuid":null,"sessionId":"10101010-1010-4010-8010-101010101010","timestamp":"2026-10-01T00:00:00.000Z","message":{"role":"user","content":"OLD_REMOTE"}},{"type":"assistant","uuid":"old-v1-assistant","parentUuid":"old-v1-user","sessionId":"10101010-1010-4010-8010-101010101010","timestamp":"2026-10-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"OLD_ANSWER"}]}}]}`)
						return
					}
					puts.Add(1)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				initial := map[string]any{"type": "control_request", "request_id": "fatal-init", "request": map[string]string{"subtype": "initialize"}}
				data, _ := json.Marshal(initial)
				if err = conn.Write(ctx, websocket.MessageText, append(data, '\n')); err != nil {
					t.Error(err)
					return
				}
				for {
					_, data, err := conn.Read(ctx)
					if err != nil {
						return
					}
					var frame map[string]any
					json.Unmarshal(data, &frame)
					response, _ := frame["response"].(map[string]any)
					if frame["type"] == "control_response" && response["request_id"] == "fatal-init" {
						data, _ = json.Marshal(map[string]any{"type": "user", "uuid": "new-go-v1", "message": map[string]string{"role": "user", "content": "V1_NEW_USER"}})
						if err = conn.Write(ctx, websocket.MessageText, append(data, '\n')); err != nil {
							t.Error(err)
							return
						}
					}
				}
			}))
			defer peer.Close()
			env := append(resumeErrorEnv(home, root, model.URL), "CLAUDE_CODE_SESSION_ACCESS_TOKEN=fake-v1-fatal", "ENABLE_SESSION_PERSISTENCE=1", "TEST_ENABLE_SESSION_PERSISTENCE=1", "CLAUDE_CODE_SIMPLE=1")
			var command *exec.Cmd
			if source {
				fixture, _ := filepath.Abs("../../testdata/native-remoteio-http.fixture.ts")
				command = exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				command.Env = append(env, "CC_HAHA_REMOTEIO_FIXTURE=1")
				input, _ := json.Marshal(map[string]any{"legacy": true, "resume": true, "persistAfterRestore": true, "url": peer.URL + "/ingress/private", "env": map[string]string{}})
				command.Stdin = bytes.NewReader(input)
			} else {
				command = exec.CommandContext(ctx, binary, "--sdk-url", strings.Replace(peer.URL, "http", "ws", 1)+"/ws/private", "--bare", "--resume", peer.URL+"/ingress/private", "--model", "fixture-model", "--no-computer-use", "--thinking", "disabled", "--tools", "", "--system-prompt", "Private V1 fatal persistence")
				command.Env = env
			}
			command.Dir = home
			code, _, stderr := resumeErrorRun(t, command, false)
			// The record starts independently of the first model request; either
			// may win admission. Fatal persistence cancels that request and must
			// prohibit subsequent requests or a second append.
			if code != 1 || puts.Load() != 1 || modelCalls.Load() > 1 {
				t.Fatalf("Source/public V1 fatal code=%d puts=%d model=%d stderr=%s", code, puts.Load(), modelCalls.Load(), stderr)
			}
			if strings.Contains(string(stderr), "capability escape") || strings.Contains(string(stderr), "fixture blocked") {
				t.Fatal("source native capability blocked", string(stderr))
			}
		})
	}
}
