package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestSideChatUnopenedRuntimeSelectionMatchesActualSourceWithoutFactory(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source Bun unavailable")
	}
	parent, cliID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	id := "side-" + cliID
	content := `{"type":"session-meta","workDir":"$HOME","permissionMode":"default","runtimeProviderId":null,"runtimeModelId":"fixture-model","effortLevel":"low"}` + "\n" + `{"type":"user","uuid":"u1","sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"Reference only"}}` + "\n"
	controls := []map[string]any{{"type": "set_runtime_config", "providerId": nil, "modelId": " "}, {"type": "set_runtime_config", "providerId": nil, "modelId": "cold-next-model"}, {"type": "set_runtime_config", "providerId": nil, "modelId": "invalid-effort-model", "effortLevel": "wrong"}, {"type": "set_runtime_config", "providerId": nil, "modelId": "cold-last-model"}}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-side-chat-cold-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_CHAT_COLD_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"home": home, "parentId": parent, "cliId": cliID, "content": content, "controls": controls})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Results      []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal(source.Capabilities.Blocked)
	}
	var factories atomic.Int32
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		factories.Add(1)
		t.Error("Unopened runtime selection started a Harness")
		return nil, nil
	})
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	path := filepath.Join(o.ConfigDir, "projects", "private", parent+".jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(content, "$HOME", o.HomeDir)), 0600)
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/sessions/"+parent+"/side-chats", strings.NewReader(`{"sideChatId":"`+cliID+`"}`))
	r.RemoteAddr = "127.0.0.1:32100"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	host := httptest.NewServer(s)
	defer host.Close()
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	for i, message := range controls {
		data, _ := json.Marshal(message)
		if e = websocket.Message.Send(ws, string(data)); e != nil {
			t.Fatal(e)
		}
		var event json.RawMessage
		for {
			var text string
			if e = websocket.Message.Receive(ws, &text); e != nil {
				t.Fatal(e)
			}
			frame := historyRecord([]byte(text))
			kind := stringField(frame, "type")
			if kind == "runtime_config_applied" || kind == "error" {
				event = []byte(text)
				break
			}
		}
		s.mu.Lock()
		side := cloneSideChat(s.sideChats[id])
		s.mu.Unlock()
		observation := map[string]any{"events": []json.RawMessage{event}, "started": side.started, "active": factories.Load() > 0, "model": side.info.RuntimeModelID, "provider": side.info.RuntimeProviderID, "effort": side.info.EffortLevel}
		if side.info.EffortLevel == "" {
			delete(observation, "effort")
		}
		actual, _ := json.Marshal(observation)
		var got, want any
		_ = json.Unmarshal(actual, &got)
		_ = json.Unmarshal(source.Results[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d Source%s Go%s", i, source.Results[i], actual)
		}
	}
	if factories.Load() != 0 {
		t.Fatal(factories.Load())
	}
}
