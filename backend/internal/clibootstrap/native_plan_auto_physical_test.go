package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeActualPlanAutoPhysicalClassifier(t *testing.T) {
	for _, result := range []string{"allow", "block", "invalid", "unavailable", "retry"} {
		t.Run(result, func(t *testing.T) {
			var mu sync.Mutex
			mainRequests, classifierRequests := 0, 0
			var classBody []byte
			var retryHeaders []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var request protocol.JSONObject
				_ = request.UnmarshalJSON(body)
				if string(request.Get("stream")) != "true" {
					mu.Lock()
					classifierRequests++
					classBody = append([]byte{}, body...)
					retryHeaders = append(retryHeaders, r.Header.Get("x-stainless-retry-count"))
					attempt := classifierRequests
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					if result == "retry" && attempt < 3 {
						w.Header().Set("retry-after-ms", "1")
						if attempt == 1 {
							w.WriteHeader(500)
						} else {
							w.WriteHeader(429)
						}
						fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"original private transient"}}`)
						return
					}
					switch result {
					case "unavailable":
						w.WriteHeader(400)
						fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"private unavailable"}}`)
						return
					case "invalid":
						fmt.Fprint(w, `{"id":"classifier-message","content":[],"usage":{"input_tokens":2,"output_tokens":1}}`)
						return
					}
					block := "false"
					if result == "block" {
						block = "true"
					}
					fmt.Fprintf(w, `{"id":"classifier-message","content":[{"type":"tool_use","name":"classify_result","input":{"thinking":"actual private reasoning","shouldBlock":%s,"reason":"private action result"}}],"usage":{"input_tokens":2,"output_tokens":1}}`, block)
					return
				}
				mu.Lock()
				mainRequests++
				number := mainRequests
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				switch number {
				case 1:
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"enter-auto","name":"EnterPlanMode","input":{}}`)}))
				case 2:
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"classify-bash","name":"Bash","input":{"command":"printf native-classifier","description":"Private fixture action"}}`)}))
				case 3:
					fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"text","text":"actual classifier program completed"}`)}))
				default:
					t.Errorf("unexpected main%d", number)
				}
			}))
			defer server.Close()
			f, _, cu := computerUseMainFixture(t)
			n := f.n
			delete(n.Environment, "CLAUDE_CODE_SIMPLE")
			n.NativeProcess = true
			n.Environment["PATH"] = "/usr/bin:/bin"
			n.Environment["ANTHROPIC_BASE_URL"] = server.URL
			n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
			if result == "retry" {
				n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "2"
			}
			n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
			config := n.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true}}`), 0600); e != nil {
				t.Fatal(e)
			}
			disabled := false
			var output bytes.Buffer
			m, e := NewMainPrint(MainPrintOptions{ComputerUse: cu, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual plan classifier", Tools: []string{"EnterPlanMode,Bash"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 5, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(b []byte) error { output.Write(b); return nil }})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			if e = m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Only execute the declared private fixture command"}}`)); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			defer mu.Unlock()
			wantClasses := 1
			if result == "retry" {
				wantClasses = 3
			}
			if classifierRequests != wantClasses || mainRequests != 3 {
				t.Fatalf("classifier real requests=%d main=%d output=%s", classifierRequests, mainRequests, output.String())
			}
			if !bytes.Contains(classBody, []byte(`"max_tokens":4096`)) || !bytes.Contains(classBody, []byte(`"name":"classify_result"`)) || !bytes.Contains(classBody, []byte(`"thinking":{"type":"disabled"}`)) || bytes.Contains(classBody, []byte(`"stream":true`)) {
				t.Fatalf("original sideQuery request missing: %s", classBody)
			}
			if result == "retry" && strings.Join(retryHeaders, ",") != "0,1,2" {
				t.Fatal("SDK retry-count wire", retryHeaders)
			}
			if (result == "allow" || result == "retry") && !strings.Contains(output.String(), "native-classifier") {
				t.Fatalf("allowed command not executed:%s", output.String())
			}
			if result == "block" && !strings.Contains(output.String(), "private action result") {
				t.Fatalf("classifier denial absent:%s", output.String())
			}
		})
	}
}

// Cancellation starts from an actually accepted classifier POST. The handler
// holds the actual response; no synthetic provider fault or clock controls it.
func TestNativeActualPlanAutoPhysicalCallerCancellation(t *testing.T) {
	accepted := make(chan struct{})
	peerCancelled := make(chan struct{})
	var mu sync.Mutex
	mainCount, classCount := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(body)
		if string(request.Get("stream")) != "true" {
			mu.Lock()
			classCount++
			mu.Unlock()
			close(accepted)
			<-r.Context().Done()
			close(peerCancelled)
			return
		}
		mu.Lock()
		mainCount++
		number := mainCount
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if number == 1 {
			fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"enter-cancel","name":"EnterPlanMode","input":{}}`)}))
			return
		}
		if number == 2 {
			fmt.Fprint(w, webWire("fixture-model", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"cancel-bash","name":"Bash","input":{"command":"printf never-run"}}`)}))
			return
		}
		t.Errorf("unexpected model request after caller cancel: %d", number)
	}))
	defer server.Close()
	f, _, cu := computerUseMainFixture(t)
	n := f.n
	delete(n.Environment, "CLAUDE_CODE_SIMPLE")
	n.NativeProcess = true
	n.Environment["PATH"] = "/usr/bin:/bin"
	n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true}}`), 0600); e != nil {
		t.Fatal(e)
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: cu, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Cancel original private classifier", Tools: []string{"EnterPlanMode,Bash"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 5, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Only original private action"}}`))
	}()
	select {
	case <-accepted:
	case <-time.After(15 * time.Second):
		t.Fatal("actual classifier was not admitted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("caller cancellation stranded actual owner")
	}
	select {
	case <-peerCancelled:
	case <-time.After(15 * time.Second):
		t.Fatal("classifier native request not cancelled")
	}
	mu.Lock()
	defer mu.Unlock()
	if mainCount != 2 || classCount != 1 {
		t.Fatalf("physical requests main/classifier=%d/%d", mainCount, classCount)
	}
}
