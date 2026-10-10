package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"golang.org/x/net/websocket"
)

func TestActualDesktopTimedQuestionUsesSameNativePermissionAndModelHistory(t *testing.T) {
	question := map[string]any{"question": "Which format?", "header": "Format", "options": []any{map[string]string{"label": "JSON", "description": "machine"}, map[string]string{"label": "Markdown", "description": "human"}}}
	input := map[string]any{"questions": []any{question}, "metadata": map[string]any{"autoAnswered": true, "keep": "source"}}
	var mainCalls, smallCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if obj(body).String("model") == "small-question-model" {
			smallCalls.Add(1)
			if !bytes.Contains(body, []byte("Prefer JSON.")) || !bytes.Contains(body, []byte("Which format?")) {
				t.Errorf("actual small model lost original transcript context %s", body)
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"answers\":[{\"questionIndex\":0,\"optionLabels\":[\"JSON\"]}]}"}]}`))
			return
		}
		if mainCalls.Add(1) == 1 {
			response(w, "ask-auto", "AskUserQuestion", input, "")
			return
		}
		if !bytes.Contains(body, []byte("Answers were selected automatically after the timeout")) || !bytes.Contains(body, []byte("not direct user input")) {
			t.Errorf("automatic choice provenance missing in main request %s", body)
		}
		response(w, "auto-final", "", nil, "AUTOMATIC QUESTION COMPLETE")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"AskUserQuestion"}
	o.Arguments.PermissionMode = "bypassPermissions"
	id := "source-provider"
	o.RuntimeSelection = &providerconfig.Selection{ProviderID: &id}
	timers := make(chan func(), 4)
	service, e := autoquestion.New(autoquestion.Options{Log: func(reason string, e error) { t.Log("auto", reason, e) }, ReadSettings: func(context.Context) (json.RawMessage, error) {
		return []byte(`{"enabled":true,"timeoutMinutes":1}`), nil
	}, AfterFunc: func(d time.Duration, f func()) autoquestion.Timer {
		if d < 59*time.Second || d > time.Minute {
			t.Errorf("Source timeout differs %s", d)
		}
		timer := time.AfterFunc(time.Hour, f)
		timers <- f
		return timer
	}, Decider: autoquestion.Decider{Resolve: func(context.Context, *string, string) (*autoquestion.Target, error) {
		return &autoquestion.Target{Model: "small-question-model", URL: provider.URL + "/v1/messages", Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
	}}})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close(context.Background())
	o.AutoQuestion = service
	o.AutoQuestionProvider = func(context.Context, string) (json.RawMessage, error) { return []byte(`{"fixture":"same"}`), nil }
	var s *server.Server
	o.AutoQuestionContext = func(ctx context.Context, id, agent string) (json.RawMessage, error) {
		value, e := s.ReadAutoQuestionContext(ctx, id, agent)
		t.Log("physical context", string(value), e)
		return value, e
	}
	s, e = server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o), AutoQuestion: service})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	req, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	req.Header.Set("Content-Type", "application/json")
	res, e := host.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(string(body))
	}
	sid := obj(body).String("sessionId")
	ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+sid, "", host.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
	send := func(v any) {
		t.Helper()
		if e := websocket.Message.Send(ws, string(raw(v))); e != nil {
			t.Fatal(e)
		}
	}
	receive := func(kind string) protocol.JSONObject {
		t.Helper()
		for i := 0; i < 150; i++ {
			var data string
			if e := websocket.Message.Receive(ws, &data); e != nil {
				t.Fatal(e)
			}
			v := obj([]byte(data))
			if v.String("type") == "error" {
				t.Fatal(data)
			}
			if v.String("type") == kind {
				return v
			}
		}
		t.Fatal(kind)
		return protocol.JSONObject{}
	}
	send(map[string]any{"type": "user_message", "content": "Prefer JSON."})
	asked := receive("permission_request")
	if bytes.Contains(asked.Get("input"), []byte(`"autoAnswered":true`)) {
		t.Fatal("untrusted model marker survived")
	}
	select {
	case fire := <-timers:
		for deadline := time.Now().Add(5 * time.Second); ; {
			contextValue, _ := s.ReadAutoQuestionContext(context.Background(), sid, "")
			if obj(raw(map[string]any{"v": contextValue})).String("v") != "" {
				break
			}
			if time.Now().After(deadline) {
				filepath.Walk(o.Environment["CLAUDE_CONFIG_DIR"], func(path string, info os.FileInfo, e error) error {
					if e == nil && !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
						data, _ := os.ReadFile(path)
						t.Log("physical persisted", path, string(data))
					}
					return nil
				})
				t.Fatal("original persisted context still empty")
			}
			time.Sleep(10 * time.Millisecond)
		}
		fire()
	case <-time.After(5 * time.Second):
		t.Fatal("timer producer not reached")
	}
	resolved := receive("permission_resolved")
	if resolved.String("requestId") != asked.String("requestId") || string(resolved.Get("allowed")) != "true" {
		t.Fatal(string(raw(resolved)))
	}
	send(map[string]any{"type": "permission_response", "requestId": asked.String("requestId"), "allowed": false})
	receive("message_complete")
	if mainCalls.Load() != 2 || smallCalls.Load() != 1 {
		t.Fatal(mainCalls.Load(), smallCalls.Load())
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	found := false
	_ = filepath.Walk(o.Environment["CLAUDE_CONFIG_DIR"], func(path string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() && strings.HasSuffix(path, sid+".jsonl") {
			data, _ := os.ReadFile(path)
			found = bytes.Contains(data, []byte("Answers were selected automatically"))
		}
		return nil
	})
	if !found {
		t.Fatal("automatic result not in actual session transcript")
	}
}
