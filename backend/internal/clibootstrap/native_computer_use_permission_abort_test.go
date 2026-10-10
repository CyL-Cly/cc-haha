package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// Reproduce desktop Stop while Bash is awaiting the actual permission Future.
// Both a never-used CU turn and an already-acquired private CU lease must reach
// the same abort cleanup consumer and leave the same Harness usable afterward.
func TestNativeComputerUsePermissionWaitStopReleasesLeaseAndContinuesSameHarness(t *testing.T) {
	for _, acquired := range []bool{false, true} {
		t.Run(fmt.Sprintf("lease=%v", acquired), func(t *testing.T) {
			f, _, cu := computerUseMainFixture(t)
			var nativeCalls atomic.Int32
			cu.NativeCall = func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
				nativeCalls.Add(1)
				switch name {
				case "check_permissions":
					return json.RawMessage(`{"accessibility":true,"screenRecording":true}`), nil
				case "resolve_app_target":
					return json.RawMessage(`{"pid":42,"bundleId":"private.app","path":"/private/App.app","executablePath":"/private/App.app/Contents/MacOS/App","launchTime":17}`), nil
				case "get_app_state":
					return json.RawMessage(`{"axText":"App=private.app\n g1:4 Button","bundleId":"private.app"}`), nil
				default:
					return nil, fmt.Errorf("unexpected native action %s", name)
				}
			}
			var requests atomic.Int32
			marker := filepath.Join(f.cwd, "must-not-run.txt")
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/messages" {
					http.NotFound(w, r)
					return
				}
				body, _ := io.ReadAll(r.Body)
				round := requests.Add(1)
				tool, args := "Bash", map[string]any{"command": "printf forbidden > " + marker, "description": "await actual approval"}
				if acquired && round == 1 {
					tool, args = "mcp__computer-use__js", map[string]any{"code": `var app=await cua.getApp("Private"); await app.getAXState(); nodeRepl.write("acquired real private lease")`}
				}
				last := int32(2)
				if acquired {
					last = 3
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"abort-permission\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
				if round == last {
					if !bytes.Contains(body, []byte(`[Request interrupted by user for tool use]`)) || !bytes.Contains(body, []byte(`continue same actual Harness`)) {
						t.Error("next turn lost original interruption/history")
					}
					fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"same Harness recovered\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					return
				}
				if round > last {
					t.Error("unexpected retry/extra model request")
				}
				input, _ := json.Marshal(args)
				fragment, _ := json.Marshal(string(input))
				fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"permission-%d\",\"name\":%q,\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", round, tool, fragment)
			}))
			defer provider.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = provider.URL
			entered := make(chan struct{})
			var m *MainPrint
			var mu sync.Mutex
			frames := []json.RawMessage{}
			resolve := func(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
				if tool.Name != "Bash" {
					return queryloop.ToolPermission{Behavior: "allow", Input: input}, nil
				}
				future, err := ownerasync.NewOwnerFuture[queryloop.ToolPermission](m.Owner)
				if err != nil {
					return queryloop.ToolPermission{}, err
				}
				close(entered)
				go func() {
					<-ctx.Done()
					_, _ = m.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return future.Reject(next, context.Cause(ctx)) })
				}()
				return future.Await(scope)
			}
			var err error
			m, err = NewMainPrint(MainPrintOptions{ComputerUse: cu, ResolvePermission: resolve, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "actual private CU permission abort", Tools: []string{"Read,Bash"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(frame []byte) error {
				mu.Lock()
				frames = append(frames, append(json.RawMessage(nil), frame...))
				mu.Unlock()
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			if err = m.StartSession(context.Background()); err != nil {
				t.Fatal(err)
			}
			caller, stop := context.WithCancel(context.Background())
			defer stop()
			done := make(chan error, 1)
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: json.RawMessage(`{"role":"user","content":"perform native action then await Bash approval"}`)})
			go func() { done <- m.RunTurn(caller, input) }()
			select {
			case <-entered:
			case err = <-done:
				t.Fatalf("permission never entered: %v", err)
			case <-time.After(15 * time.Second):
				t.Fatal("actual permission Future did not start")
			}
			lease := filepath.Join(f.config, "computer-use.lock")
			if acquired {
				data, e := os.ReadFile(lease)
				if e != nil || !bytes.Contains(data, []byte(m.Controller.SessionID())) {
					t.Fatalf("private CU lease absent %s %v", data, e)
				}
				foreign := cooperative.New()
				foreignErr := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { return m.mcpTools.EndTurnOwner(scope) })
				foreign.Close()
				if !errors.Is(foreignErr, cooperative.ErrRestricted) {
					t.Fatalf("foreign owner cleanup accepted: %v", foreignErr)
				}
				if after, e := os.ReadFile(lease); e != nil || !bytes.Equal(after, data) {
					t.Fatalf("foreign owner changed main CU lease: %v", e)
				}
			} else if nativeCalls.Load() != 0 {
				t.Fatal("unused enabled Computer Use started a native operation")
			}
			stop()
			select {
			case err = <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("Stop failed: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("Stop did not drain permission/cleanup")
			}
			if _, err = os.Stat(lease); !os.IsNotExist(err) {
				t.Fatalf("Stop retained CU lease %v", err)
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("cancelled Bash ran", err)
			}
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				return m.mcpTools.Reconnect(context.Background(), scope, "computer-use")
			}); err != nil {
				t.Fatal(err)
			}
			input.Set("message", json.RawMessage(`{"role":"user","content":"continue same actual Harness"}`))
			if err = m.RunTurn(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			joined, _ := json.Marshal(frames)
			mu.Unlock()
			if !bytes.Contains(joined, []byte("same Harness recovered")) || bytes.Contains(joined, []byte("abort computer-use overlay/badge/hidden-app/lock lifecycle")) {
				t.Fatalf("wrong SDK completion %s", joined)
			}
			if err = m.ShutdownSession(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
