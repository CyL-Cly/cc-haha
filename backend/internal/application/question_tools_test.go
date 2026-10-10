package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"golang.org/x/net/websocket"
)

func TestQuestionActualHarnessWaitsForPermissionAndUsesUserAnswer(t *testing.T) {
	for _, mode := range []string{"answer", "decline", "clarify", "image", "reject-image"} {
		allowed := mode == "answer" || mode == "image"
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var calls atomic.Int32
			question := map[string]any{"question": "Choose runtime?", "header": "Runtime", "options": []any{map[string]any{"label": "Go", "description": "Native backend"}, map[string]any{"label": "Other runtime", "description": "Alternative"}}}
			modelInput := map[string]any{"questions": []any{question}, "answers": map[string]string{"Choose runtime?": "Other runtime"}, "metadata": map[string]any{"autoAnswered": true}}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				ordinal := calls.Add(1)
				if ordinal == 1 {
					if !bytes.Contains(body, []byte(`"name":"AskUserQuestion"`)) {
						t.Errorf("question missing native schema %s", body)
					}
					response(w, "question-native", "AskUserQuestion", modelInput, "")
					return
				}
				var request struct {
					Messages []struct {
						Role    string
						Content []protocol.JSONObject
					}
				}
				if json.Unmarshal(body, &request) != nil {
					t.Errorf("native request JSON")
				}
				found := false
				images, feedbackTexts := 0, 0
				for _, message := range request.Messages {
					for _, block := range message.Content {
						if block.String("type") == "image" {
							images++
							if block.Get("tool_use_id") != nil {
								t.Error("permission image nested in tool result")
							}
						}
						if block.String("type") == "text" && block.String("text") == "Image approval feedback" {
							feedbackTexts++
						}
						if block.String("type") == "tool_result" && block.String("tool_use_id") == "call-question-native" {
							found = true
							content := block.String("content")
							if allowed {
								if !strings.Contains(content, `"Choose runtime?"="Go"`) || strings.Contains(content, "automatic") {
									t.Errorf("model answered without user authority: %s", content)
								}
								if !strings.Contains(content, "You can now continue with the user's answers in mind.") {
									t.Errorf("Source mapped guidance: %s", content)
								}
							} else if mode == "clarify" {
								if !bytes.Equal(block.Get("is_error"), []byte("true")) || !strings.Contains(content, "Start by asking them what they would like to clarify.") {
									t.Errorf("custom clarification feedback lost: %s", content)
								}
							} else if !bytes.Equal(block.Get("is_error"), []byte("true")) || !strings.Contains(content, "The user doesn't want to proceed") {
								t.Errorf("decline failed %s", raw(block))
							}
						}
					}
				}
				if !found {
					t.Errorf("native permission result did not reach provider")
				}
				if (mode == "image" || mode == "reject-image") && images != 1 {
					t.Errorf("permission image did not reach actual provider request: %d %s", images, body)
				}
				if mode == "image" && feedbackTexts != 1 {
					t.Errorf("approve feedback lost: %d %s", feedbackTexts, body)
				}
				response(w, "question-final", "", nil, "QUESTION COMPLETE")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Tools = []string{"AskUserQuestion"}
			o.Arguments.PermissionMode = "bypassPermissions"
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			wantPermissionMode := "bypassPermissions"
			if mode == "clarify" {
				if err = s.process.SetSessionPermissionMode(ctx, "plan"); err != nil {
					t.Fatal(err)
				}
				wantPermissionMode = "plan"
			}

			pending := make(chan tui.PermissionRequest, 1)
			finished := make(chan error, 1)
			go func() {
				finished <- s.Submit(ctx, tui.Input{Text: "Ask me to choose."}, func(event tui.Event) error {
					if event.Kind == tui.PermissionAsked && event.Permission != nil {
						pending <- *event.Permission
					}
					return nil
				})
			}()
			var asked tui.PermissionRequest
			select {
			case asked = <-pending:
			case err = <-finished:
				t.Fatalf("did not ask user: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if calls.Load() != 1 || asked.ToolName != "AskUserQuestion" {
				t.Fatal("provider advanced before real user input")
			}
			if asked.PermissionMode != wantPermissionMode {
				t.Fatalf("missing live permission mode %q", asked.PermissionMode)
			}
			input := obj(asked.Input)
			metadata := obj(input.Get("metadata"))
			if metadata.Get("autoAnswered") != nil {
				t.Fatal("model-supplied provenance entered permission UI")
			}
			input.Set("answers", raw(map[string]string{"Choose runtime?": "Go"}))
			decision := tui.PermissionDecision{Allowed: allowed, UpdatedInput: raw(input)}
			if mode == "clarify" {
				decision.Message = raw("The user wants to clarify these questions.\n    Start by asking them what they would like to clarify.")
			}
			if mode == "image" || mode == "reject-image" {
				decision.ContentBlocks = []json.RawMessage{[]byte(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jR1MAAAAASUVORK5CYII="}}`)}
				if mode == "image" {
					decision.AcceptFeedback = raw("Image approval feedback")
				} else {
					decision.Behavior = "ask"
				}
			}
			if err = s.RespondPermission(ctx, asked.ID, decision); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if calls.Load() != 2 || len(s.PendingPermissions()) != 0 {
				t.Fatalf("question lifecycle retained pending or failed rounds: %d %v", calls.Load(), s.PendingPermissions())
			}
		})
	}
}

func TestQuestionDesktopPermissionImagesReachActualWebSocketHarness(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "clarify", true: "approve"}[allowed], func(t *testing.T) {
			question := map[string]any{"question": "Choose runtime?", "header": "Runtime", "options": []any{map[string]any{"label": "Go", "description": "Native"}, map[string]any{"label": "Other", "description": "Alternative"}}}
			input := map[string]any{"questions": []any{question}, "answers": map[string]string{"Choose runtime?": "Go"}, "metadata": map[string]any{"autoAnswered": true}}
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if calls.Add(1) == 1 {
					response(w, "desktop-question", "AskUserQuestion", input, "")
					return
				}
				var physical struct {
					Messages []struct{ Content []protocol.JSONObject }
				}
				_ = json.Unmarshal(body, &physical)
				images := 0
				for _, message := range physical.Messages {
					for _, block := range message.Content {
						if block.String("type") == "image" && obj(block.Get("source")).String("data") == "cHJvY2Vzc2Vk" {
							images++
						}
					}
				}
				if images != 1 {
					t.Errorf("desktop image not at user level in actual POST: count%d", images)
				}
				if allowed {
					if !bytes.Contains(body, []byte("desktop approved feedback")) || bytes.Contains(body, []byte("automatic response")) {
						t.Error("desktop permission feedback/provenance differs")
					}
				} else if !bytes.Contains(body, []byte("The user wants to clarify these questions.")) || !bytes.Contains(body, []byte("desktop clarifying feedback")) {
					t.Error("desktop denyMessage lost original Question guidance")
				}
				response(w, "desktop-question-final", "", nil, "DESKTOP QUESTION COMPLETE")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Tools = []string{"AskUserQuestion"}
			o.Arguments.PermissionMode = "bypassPermissions"
			s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			host := httptest.NewServer(s)
			defer host.Close()
			request, _ := http.NewRequest("POST", host.URL+"/api/sessions", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
			request.Header.Set("Content-Type", "application/json")
			created, err := host.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(created.Body)
			_ = created.Body.Close()
			if created.StatusCode != 201 {
				t.Fatalf("desktop session %d %s", created.StatusCode, body)
			}
			id := obj(body).String("sessionId")
			ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
			send := func(value any) {
				t.Helper()
				if err := websocket.Message.Send(ws, string(raw(value))); err != nil {
					t.Fatal(err)
				}
			}
			receive := func(kind string) protocol.JSONObject {
				t.Helper()
				for count := 0; count < 100; count++ {
					var source string
					if err := websocket.Message.Receive(ws, &source); err != nil {
						t.Fatal(err)
					}
					message := obj([]byte(source))
					if message.String("type") == "error" {
						t.Fatalf("desktop Question error %s", source)
					}
					if message.String("type") == kind {
						return message
					}
				}
				t.Fatalf("missing desktop %s", kind)
				return protocol.JSONObject{}
			}
			receive("connected")
			send(map[string]any{"type": "user_message", "content": "Ask a Question."})
			permission := receive("permission_request")
			if permission.String("toolName") != "AskUserQuestion" || calls.Load() != 1 {
				t.Fatal("Question permission did not pause actual harness")
			}
			decision := map[string]any{"type": "permission_response", "requestId": permission.String("requestId"), "allowed": allowed, "updatedInput": input, "contentBlocks": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "cHJvY2Vzc2Vk"}}}}
			if allowed {
				decision["acceptFeedback"] = "desktop approved feedback"
			} else {
				decision["behavior"], decision["denyMessage"] = "ask", "desktop clarifying feedback"
			}
			send(decision)
			receive("message_complete")
			for state := receive("session_state"); state.String("turnState") != "idle"; state = receive("session_state") {
			}
			if calls.Load() != 2 {
				t.Fatal("desktop Question did not settle")
			}
		})
	}
}

func TestQuestionActualHarnessCancelClosesPendingPermission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "pending-question", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Continue?", "header": "Choice", "options": []any{map[string]any{"label": "A", "description": "a"}, map[string]any{"label": "B", "description": "b"}}}}}, "")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"AskUserQuestion"}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	pending := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- s.Submit(ctx, tui.Input{Text: "Ask and wait."}, func(event tui.Event) error {
			if event.Kind == tui.PermissionAsked {
				pending <- struct{}{}
			}
			return nil
		})
	}()
	select {
	case <-pending:
	case err = <-finished:
		t.Fatalf("missing question %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	s.Cancel()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("cancelled question stayed blocked")
	}
	if calls.Load() != 1 || len(s.PendingPermissions()) != 0 {
		t.Fatal("cancel called model again or leaked request", calls.Load(), s.PendingPermissions())
	}
}
