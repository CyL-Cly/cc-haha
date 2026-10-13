package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func TestActualSourceAndPublicDesktopPlanApprovalCrossProviderRestart(t *testing.T) {
	runActualPlanProviderRestart(t, "normal")
}
func TestActualSourceAndPublicDesktopPlanApprovalSameProviderEffortRestart(t *testing.T) {
	runActualPlanProviderRestart(t, "effort")
}

func TestActualSourceAndPublicDesktopPlanApprovalRestartClientLifecycle(t *testing.T) {
	for _, scenario := range []string{"disconnect", "disconnect-queued", "fallback", "abort", "close", "close-restart"} {
		t.Run(scenario, func(t *testing.T) { runActualPlanProviderRestart(t, scenario) })
	}
}

func TestActualSourceAndPublicDesktopPlanApprovalDuplicateAndQueuedInput(t *testing.T) {
	for _, scenario := range []string{"duplicate", "queued"} {
		t.Run(scenario, func(t *testing.T) { runActualPlanProviderRestart(t, scenario) })
	}
}

func runActualPlanProviderRestart(t *testing.T, scenario string) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	observations := map[string]any{}
	for _, kind := range []string{"source", "go"} {
		t.Run(kind, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			work := filepath.Join(home, "work")
			config := filepath.Join(home, ".claude")
			tmp := filepath.Join(home, "tmp")
			for _, p := range []string{work, config, tmp, filepath.Join(work, ".git")} {
				if e = os.MkdirAll(p, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.WriteFile(filepath.Join(work, "input.txt"), []byte("PLAN_RUNTIME_PRIVATE_INPUT: 37 41\n"), 0600); e != nil {
				t.Fatal(e)
			}
			rg, e := exec.LookPath("rg")
			if e != nil {
				t.Fatal(e)
			}
			rgBody, e := os.ReadFile(rg)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(home, "private-rg"), rgBody, 0700); e != nil {
				t.Fatal(e)
			}
			var mu sync.Mutex
			requestCanceled := make(chan struct{})
			requestStarted := make(chan struct{})
			newResult := make(chan struct{})
			var newResultOnce sync.Once
			planning, execution, auxiliary := []json.RawMessage{}, []json.RawMessage{}, []json.RawMessage{}
			planPath := ""
			initialModel, executionModel, nextProvider, expectedCredential := "fixture-planning", "fixture-execution", "execution", "private-execution-key"
			if scenario == "effort" {
				initialModel = "claude-sonnet-4-6"
				executionModel = initialModel
				nextProvider = "planning"
				expectedCredential = "private-planning-key"
			}
			override := map[string]any{"providerId": nextProvider, "modelId": executionModel}
			if scenario == "effort" {
				override["effortLevel"] = "high"
			}
			var executeHandler http.HandlerFunc
			left := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if scenario == "effort" && bytes.Contains(body, []byte("The plan is approved. Continue with the implementation.")) {
					r.Body = io.NopCloser(bytes.NewReader(body))
					executeHandler(w, r)
					return
				}
				if r.URL.Path != "/v1/messages" {
					http.Error(w, "unowned planning endpoint", 404)
					return
				}
				mu.Lock()
				planning = append(planning, append(json.RawMessage(nil), body...))
				n := len(planning)
				mu.Unlock()
				switch n {
				case 1:
					response(w, "actual-plan-enter", "EnterPlanMode", map[string]any{}, "")
				case 2:
					match := regexp.MustCompile(`No plan file exists yet\. You should create your plan at ([^ ]+) using the Write tool\.`).FindSubmatch(body)
					if len(match) != 2 {
						t.Errorf("actual EnterPlanMode reminder missing %.1800s", body)
						http.Error(w, "no initial plan path", 500)
						return
					}
					mu.Lock()
					planPath = string(match[1])
					mu.Unlock()
					response(w, "actual-plan-read", "Read", map[string]any{"file_path": filepath.Join(work, "input.txt")}, "")
				case 3:
					if !bytes.Contains(body, []byte("PLAN_RUNTIME_PRIVATE_INPUT")) {
						t.Error("actual planning Read result not in next POST")
					}
					mu.Lock()
					actualPlanPath := planPath
					mu.Unlock()
					response(w, "actual-plan-write", "Write", map[string]any{"file_path": actualPlanPath, "content": "# Actual preserved plan\nWrite execution.txt and read the physical marker."}, "")
				case 4:
					response(w, "actual-plan-exit", "ExitPlanMode", map[string]any{}, "")
				default:
					response(w, "actual-plan-interrupted", "", nil, "PLAN_RUNTIME_OLD_CAPTURED_MODEL")
				}
			}))
			defer left.Close()
			executeHandler = func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path == "/fixture-held-ready" && (scenario == "abort" || scenario == "close") {
					select {
					case <-requestStarted:
					case <-r.Context().Done():
						return
					}
					io.WriteString(w, "physical model POST arrived")
					return
				}
				if r.URL.Path != "/v1/messages" {
					http.Error(w, "unowned execution endpoint", 404)
					return
				}
				if bytes.Contains(body, []byte("Generate a concise, sentence-case title")) {
					mu.Lock()
					auxiliary = append(auxiliary, append(json.RawMessage(nil), body...))
					mu.Unlock()
					w.Header().Set("content-type", "application/json")
					_, _ = io.WriteString(w, `{"id":"actual-private-title","type":"message","role":"assistant","model":"fixture-execution","content":[{"type":"text","text":"{\"title\":\"Private plan execution\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
					return
				}
				mu.Lock()
				execution = append(execution, append(json.RawMessage(nil), body...))
				n := len(execution)
				mu.Unlock()
				if r.Header.Get("X-Api-Key") != expectedCredential {
					t.Error("new physical POST retained planning credential")
				}
				if !bytes.Contains(body, []byte("PLAN_RUNTIME_PRIVATE_INPUT")) || (scenario != "disconnect-queued" && !bytes.Contains(body, []byte("The plan is approved. Continue with the implementation."))) {
					t.Errorf("new provider lost original physical context/continue %.1800s", body)
				}
				if scenario == "disconnect-queued" && (!bytes.Contains(body, []byte("PLAN_RUNTIME_PRIVATE_QUEUED")) || bytes.Contains(body, []byte("The plan is approved. Continue with the implementation."))) {
					t.Errorf("no client lost explicit input or synthesized automatic continuation %.1200s", body)
				}
				switch n {
				case 1:
					if scenario == "abort" || scenario == "close" {
						close(requestStarted)
						w.Header().Set("content-type", "text/event-stream")
						io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"actual-held\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-execution\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"PLAN_RUNTIME_EXECUTION_HELD\"}}\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						close(requestCanceled)
						return
					}
					response(w, "actual-execute-write", "Write", map[string]any{"file_path": filepath.Join(work, "execution.txt"), "content": "PLAN_RUNTIME_PRIVATE_EXECUTION_OK"}, "")
				case 2:
					response(w, "actual-execute-read", "Read", map[string]any{"file_path": filepath.Join(work, "execution.txt")}, "")
				case 3:
					response(w, "actual-execute-done", "", nil, "PLAN_RUNTIME_PRIVATE_EXECUTION_OK")
				case 4:
					if !bytes.Contains(body, []byte("PLAN_RUNTIME_PRIVATE_NEXT:")) {
						t.Errorf("actual next ASK not consumed; request %.1200s", body)
					}
					response(w, "actual-next-read", "Read", map[string]any{"file_path": filepath.Join(work, "execution.txt")}, "")
				default:
					response(w, "actual-next-done", "", nil, "PLAN_RUNTIME_PRIVATE_NEXT_DONE")
				}
			}
			right := httptest.NewServer(executeHandler)
			defer right.Close()
			index := map[string]any{"schemaVersion": 6, "activeId": "planning", "providers": []any{map[string]any{"id": "planning", "presetId": "deepseek", "name": "Private planning", "baseUrl": left.URL, "apiKey": "private-planning-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": initialModel, "haiku": "", "sonnet": "", "opus": ""}}, map[string]any{"id": "execution", "presetId": "deepseek", "name": "Private execution", "baseUrl": right.URL, "apiKey": "private-execution-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": executionModel, "haiku": "", "sonnet": "", "opus": ""}}}}
			if e = os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), raw(index), 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"futurePlanFixture":{"retain":true},"autoQuestion":{"enabled":false}}`), 0600); e != nil {
				t.Fatal(e)
			}
			var frames []protocol.JSONObject
			var sid, failure, stderr string
			if kind == "source" {
				cli, _ := filepath.Abs("testdata/plan_runtime_source_cli.fixture.ts")
				fixture, _ := filepath.Abs("testdata/plan_runtime_source_server.fixture.ts")
				command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				command.Dir = home
				input := raw(map[string]any{"home": home, "work": work, "planningURL": left.URL, "executionURL": right.URL, "cliPath": cli, "scenario": scenario, "override": override})
				command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CLAUDE_CODE_TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SOURCE_PLAN_RESTART_FIXTURE=1", "CC_HAHA_SOURCE_PLAN_RESTART_INPUT=" + string(input), "PATH=" + filepath.Dir(bun) + ":" + os.Getenv("PATH"), "CC_HAHA_RIPGREP_PATH=" + filepath.Join(home, "private-rg"), "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "BUN_OPTIONS=--no-env-file"}
				var out, errOut bytes.Buffer
				command.Stdout = &out
				command.Stderr = &errOut
				runError := command.Run()
				stderr = errOut.String()
				var receipt protocol.JSONObject
				_ = receipt.UnmarshalJSON(out.Bytes())
				evidence, _ := filepath.Abs("../../../evidence")
				if st, e := os.Stat(evidence); e == nil && st.IsDir() {
					_ = os.WriteFile(filepath.Join(evidence, "actual-plan-source-"+scenario+"-receipt.json"), out.Bytes(), 0600)
				}
				sid = receipt.String("sessionId")
				_ = json.Unmarshal(receipt.Get("frames"), &frames)
				if runError == nil {
					var processReceipts []protocol.JSONObject
					_ = json.Unmarshal(receipt.Get("processReceipts"), &processReceipts)
					var beforeAuth, afterAuth []bool
					_ = json.Unmarshal(receipt.Get("identityBeforeClose"), &beforeAuth)
					_ = json.Unmarshal(receipt.Get("identityAfterClose"), &afterAuth)
					if len(processReceipts) != 2 || len(beforeAuth) != 2 || beforeAuth[0] || !beforeAuth[1] || len(afterAuth) != 2 || afterAuth[0] || afterAuth[1] {
						t.Fatal("actual Source old/new SDK process identity lifecycle missing", processReceipts, beforeAuth, afterAuth)
					}
					for _, process := range processReceipts {
						if string(process.Get("absent")) != "true" || obj(process.Get("exited")).String("status") != "fulfilled" {
							t.Fatal("actual Source owned PID did not drain", process)
						}
					}
					if len(obj(receipt.Get("capabilities")).Get("blocked")) > 2 {
						t.Fatal("actual Source fixture external escape", receipt.Get("capabilities"))
					}
				}
				if runError != nil {
					failure = fmt.Sprintf("%v receipt %.2000s stderr %.4000s", runError, out.String(), stderr)
				}
				observations[kind+"-receipt"] = json.RawMessage(out.Bytes())
			} else {
				o := nativeSessionOptions(t, left.URL)
				o.Environment = map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": tmp, "CLAUDE_CODE_TMPDIR": tmp, "ANTHROPIC_BASE_URL": left.URL, "ANTHROPIC_API_KEY": "private-planning-key", "CC_HAHA_RIPGREP_PATH": filepath.Join(home, "private-rg"), "PATH": os.Getenv("PATH"), "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
				o.WorkDir = work
				o.Native = nil
				o.Arguments.Model = initialModel
				o.Arguments.SystemPrompt = "Use the actual tools for the private plan restart fixture."
				o.Arguments.PermissionMode = "default"
				o.Arguments.Tools = []string{"EnterPlanMode,ExitPlanMode,Read,Write"}
				var nativeCount int
				secondNative := make(chan struct{})
				allowNative := make(chan struct{})
				var d *DesktopSession
				desktopReady := make(chan *DesktopSession, 1)
				o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
					nativeCount++
					if (scenario == "disconnect" || scenario == "disconnect-queued" || scenario == "queued" || scenario == "close-restart") && nativeCount == 2 {
						close(secondNative)
						select {
						case <-allowNative:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
					if e == nil {
						n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
							return nil, fmt.Errorf("private fixture external credential store absent")
						}
					}
					return n, e
				}
				var s *server.Server
				if scenario == "disconnect" || scenario == "disconnect-queued" || scenario == "close" || scenario == "queued" || scenario == "close-restart" {
					original := DesktopFactory(o)
					s, e = server.New(server.Options{HomeDir: home, ConfigDir: config, WorkDir: work, Factory: func(ctx context.Context, options server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
						live, err := original(ctx, options, func(data json.RawMessage) {
							if scenario == "disconnect-queued" && obj(data).String("type") == "message_complete" {
								mu.Lock()
								count := len(execution)
								mu.Unlock()
								if count >= 3 {
									newResultOnce.Do(func() { close(newResult) })
								}
							}
							emit(data)
						})
						if err == nil {
							desktopReady <- live.(*DesktopSession)
						}
						return live, err
					}})
				} else {
					s, e = NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: work, Version: o.Version, Arguments: o.Arguments, NativeFactory: o.NativeFactory})
				}
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close(context.Background())
				host := httptest.NewServer(s)
				defer host.Close()
				created, e := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": work, "permissionMode": "default"})))
				if e != nil {
					t.Fatal(e)
				}
				data, _ := io.ReadAll(created.Body)
				created.Body.Close()
				if created.StatusCode != 201 {
					t.Fatal(created.StatusCode, string(data))
				}
				sid = obj(data).String("sessionId")
				ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+sid, "", host.URL)
				if e != nil {
					t.Fatal(e)
				}
				defer ws.Close()
				ws.SetDeadline(time.Now().Add(35 * time.Second))
				applied, continued, nextSent := false, false, false
				var approval map[string]any
				approvalSent := false
				duplicateSent := false
				initial := ws
				var old *Session
				lifecycleDone := false
				for i := 0; i < 500; i++ {
					var data string
					if e = websocket.Message.Receive(ws, &data); e != nil {
						if scenario == "disconnect" || scenario == "disconnect-queued" {
							for {
								d.mu.Lock()
								phase, closed := d.planRestart, d.closed
								d.mu.Unlock()
								if phase == nil || closed {
									break
								}
								select {
								case <-ctx.Done():
									t.Fatal(ctx.Err())
								default:
									runtime.Gosched()
								}
							}
							lifecycleDone = true
							if scenario == "disconnect-queued" {
								select {
								case <-newResult:
								case <-ctx.Done():
									t.Fatal("explicit queued turn did not complete", ctx.Err())
								}
							}
						} else {
							failure = e.Error()
						}
						break
					}
					f := obj([]byte(data))
					frames = append(frames, f)
					switch f.String("type") {
					case "connected":
						websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "PLAN_RUNTIME_PRIVATE_FIRST: Enter plan mode, read input.txt, write the plan, and ask for approval."})))
					case "permission_request":
						if approvalSent {
							break
						}
						approvalSent = true
						if f.String("toolName") != "ExitPlanMode" {
							failure = "unexpected Go permission " + f.String("toolName")
							break
						}
						if scenario == "disconnect" || scenario == "disconnect-queued" || scenario == "close" || scenario == "queued" || scenario == "close-restart" {
							d = <-desktopReady
							old = d.currentEngine()
						}
						if scenario == "fallback" {
							fallback, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+sid, "", host.URL)
							if e != nil {
								t.Fatal(e)
							}
							var connected string
							if e = websocket.Message.Receive(fallback, &connected); e != nil {
								t.Fatal(e)
							}
							if obj([]byte(connected)).String("type") != "connected" {
								t.Fatal("fallback client missing original connected")
							}
							defer fallback.Close()
							ws = fallback
							ws.SetDeadline(time.Now().Add(35 * time.Second))
						}
						approval = map[string]any{"type": "permission_response", "requestId": f.String("requestId"), "allowed": true, "runtimeOverride": override, "permissionUpdates": []any{map[string]any{"type": "setMode", "destination": "session", "mode": "acceptEdits"}}}
						websocket.Message.Send(initial, string(raw(approval)))
						if scenario == "fallback" {
							_ = initial.Close()
						}
						if scenario == "disconnect" || scenario == "disconnect-queued" || scenario == "queued" || scenario == "close-restart" {
							select {
							case <-secondNative:
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							}
							if scenario == "close-restart" {
								closed := make(chan error, 1)
								go func() { closed <- s.Close(ctx) }()
								for d.canContinueRuntime() {
									select {
									case <-ctx.Done():
										t.Fatal(ctx.Err())
									default:
										runtime.Gosched()
									}
								}
								select {
								case e := <-closed:
									t.Fatal("Close returned before its accepted restart worker joined", e)
								default:
								}
								close(allowNative)
								select {
								case e := <-closed:
									if e != nil {
										t.Fatal(e)
									}
								case <-ctx.Done():
									t.Fatal("Close did not join actual restart worker", ctx.Err())
								}
								select {
								case <-old.process.Owner.Done():
								case <-ctx.Done():
									t.Fatal("old Owner survived Close")
								}
								lifecycleDone = true
								i = 500
								break
							}
							if scenario == "queued" || scenario == "disconnect-queued" {
								websocket.Message.Send(initial, string(raw(map[string]any{"type": "user_message", "content": "PLAN_RUNTIME_PRIVATE_QUEUED: Read the existing execution marker."})))
							}
							if scenario == "disconnect" || scenario == "disconnect-queued" {
								_ = initial.Close()
							}
							for (scenario == "disconnect" || scenario == "disconnect-queued") && d.canContinueRuntime() {
								select {
								case <-ctx.Done():
									t.Fatal(ctx.Err())
								default:
									runtime.Gosched()
								}
							}
							close(allowNative)
						}
					case "permission_resolved":
						if scenario == "duplicate" && !duplicateSent && approval != nil {
							duplicateSent = true
							websocket.Message.Send(initial, string(raw(approval)))
						}
					case "runtime_config_applied":
						if f.String("providerId") == nextProvider {
							applied = true
						}
					case "user_message_replay":
						if f.String("content") == "The plan is approved. Continue with the implementation." {
							continued = true
						}
					case "content_delta":
						if f.String("text") == "PLAN_RUNTIME_EXECUTION_HELD" {
							if scenario == "abort" {
								websocket.Message.Send(ws, string(raw(map[string]any{"type": "stop_generation"})))
							}
							if scenario == "close" {
								if e = s.Close(ctx); e != nil {
									t.Fatal(e)
								}
								lifecycleDone = true
								i = 500
							}
						}
					case "message_complete":
						if scenario == "abort" && applied {
							lifecycleDone = true
							i = 500
							break
						}
						if applied && continued && !nextSent {
							nextSent = true
							websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "PLAN_RUNTIME_PRIVATE_NEXT: Read the existing execution marker and report it."})))
						} else if nextSent {
							i = 500
						}
					case "error":
						failure = f.String("code") + ":" + f.String("message")
					}
					if failure != "" {
						break
					}
				}
				if scenario == "disconnect" || scenario == "disconnect-queued" {
					if !lifecycleDone || nativeCount != 2 || old == nil || old == d.currentEngine() {
						t.Fatal("disconnect did not reach actual new engine")
					}
					select {
					case <-old.process.Owner.Done():
					case <-ctx.Done():
						t.Fatal("old Owner did not close")
					}
					if d.canContinueRuntime() {
						t.Fatal("actual client EOF treated as runtime context alive")
					}
					d.mu.Lock()
					running := d.running
					d.mu.Unlock()
					if running && scenario == "disconnect" {
						t.Fatal("client EOF auto continued")
					}
				}
				if scenario == "close" && old != nil {
					select {
					case <-d.currentEngine().process.Owner.Done():
					case <-ctx.Done():
						t.Fatal("new Owner did not drain on Close")
					}
				}
			}
			if failure == "" && (scenario == "abort" || scenario == "close") {
				select {
				case <-requestCanceled:
				case <-ctx.Done():
					t.Fatal("physical new provider request not canceled")
				}
			}
			mu.Lock()
			p := append([]json.RawMessage(nil), planning...)
			x := append([]json.RawMessage(nil), execution...)
			diskPlan := planPath
			mu.Unlock()
			settingsBytes, e := os.ReadFile(filepath.Join(config, "settings.json"))
			if e != nil || !bytes.Contains(settingsBytes, []byte("futurePlanFixture")) {
				t.Fatal("original unknown settings were not preserved", e)
			}
			observations[kind] = map[string]any{"sessionId": sid, "frames": frames, "planningPOSTs": p, "executionPOSTs": x, "auxiliaryPOSTs": auxiliary, "failure": failure, "stderr": stderr, "planPath": diskPlan}
			evidence, _ := filepath.Abs("../../../evidence")
			if st, e := os.Stat(evidence); e == nil && st.IsDir() {
				data, _ := json.MarshalIndent(observations, "", "  ")
				os.WriteFile(filepath.Join(evidence, "actual-plan-provider-restart-"+scenario+"-observations.json"), data, 0600)
			}
			if failure != "" {
				if kind == "go" {
					stack := make([]byte, 2<<20)
					n := runtime.Stack(stack, true)
					_ = os.WriteFile(filepath.Join(evidence, "plan-native-failure-stacks.txt"), stack[:n], 0600)
				}
				t.Fatal("actual public", kind, "plan restart", failure, "native stderr",stderr)
			}
			if scenario == "close-restart" {
				if len(x) != 0 {
					t.Fatal("Close during restart admitted execution", len(x))
				}
				return
			}
			if scenario == "disconnect-queued" {
				if len(x) != 3 {
					t.Fatal("explicit no-client input request count", len(x))
				}
				return
			}
			if scenario == "disconnect" {
				if len(x) != 0 {
					t.Fatal("no clients still sent execution", len(x))
				}
				return
			}
			if scenario == "abort" || scenario == "close" {
				if len(x) != 1 {
					t.Fatal("abort/Close extra physical requests", len(x))
				}
				return
			}
			marker, e := os.ReadFile(filepath.Join(work, "execution.txt"))
			if e != nil || string(marker) != "PLAN_RUNTIME_PRIVATE_EXECUTION_OK" {
				t.Fatal("actual new provider Write/Read absent", e, string(marker))
			}
			if len(p) < 4 || len(x) != 5 {
				t.Fatal("actual planning/execution/next POST count", len(p), len(x))
			}
		})
	}
}
