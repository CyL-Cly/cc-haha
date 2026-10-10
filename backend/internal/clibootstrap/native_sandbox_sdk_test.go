package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSandboxStandaloneSDKStdinPermissionAnswerAndEOFRealBash(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("actual nonnested Seatbelt broker required")
	}
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("EOF=%v", eof), func(t *testing.T) {
			var modelCalls, targetCalls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalls.Add(1)
				io.WriteString(w, "SDK_SANDBOX_PHYSICAL_NETWORK_BYTES")
			}))
			defer target.Close()
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				count := modelCalls.Add(1)
				if count == 1 {
					emitNativeTaskTool(w, "sdk-native-sandbox-curl", "Bash", map[string]any{"command": "/usr/bin/curl --fail --silent --show-error --noproxy '' " + target.URL, "description": "real SDK owned sandbox network"})
					return
				}
				if (!eof) != strings.Contains(string(body), "SDK_SANDBOX_PHYSICAL_NETWORK_BYTES") {
					t.Errorf("actual physical result/deny did not reach next model POST")
				}
				emitPlanFixture(w, int(count), "", "SDK_SANDBOX_TURN_COMPLETE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
				n.Environment["CLAUDE_CODE_TMPDIR"] = n.Temp
				if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"sandbox":{"enabled":true,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}`), 0600); e != nil {
					t.Fatal(e)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = nil
				o.Arguments.AllowedTools = nil
				o.Arguments.PermissionMode = "default"
				o.StartSandboxNetworkPermission = nil
			})
			input, write := io.Pipe()
			m.options.Input = input
			t.Cleanup(func() { input.Close(); write.Close() })
			asked := make(chan protocol.JSONObject, 1)
			m.options.OnOutput = func(data []byte) error {
				var row protocol.JSONObject
				if e := row.UnmarshalJSON(data); e != nil {
					return e
				}
				if row.String("type") == "control_request" {
					request, _ := row.Object("request")
					if request.String("tool_name") == "SandboxNetworkAccess" {
						asked <- row.Clone()
					}
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				code, e := m.Run(ctx)
				if e == nil && code != 0 {
					e = fmt.Errorf("SDK exit %d", code)
				}
				done <- e
			}()
			if _, e := io.WriteString(write, `{"type":"user","message":{"role":"user","content":"Use real sandbox network"}}`+"\n"); e != nil {
				t.Fatal(e)
			}
			var frame protocol.JSONObject
			select {
			case frame = <-asked:
			case e := <-done:
				t.Fatal("SDK finished before synthetic permission", e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			request, _ := frame.Object("request")
			inputObject, _ := request.Object("input")
			if inputObject.String("host") != "127.0.0.1" || len(inputObject.OwnProperties()) != 1 || request.String("description") != "Allow network connection to 127.0.0.1?" || targetCalls.Load() != 0 {
				t.Fatal("source synthetic fields/order", string(rawMain(frame)), targetCalls.Load())
			}
			if !eof {
				response := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_response")}, protocol.JSONField{Name: "response", Value: rawMain(map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{"ignored": "synthetic callback reads only behavior"}}})})
				if _, e := io.WriteString(write, string(rawMain(response))+"\n"); e != nil {
					t.Fatal(e)
				}
			}
			write.Close()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("SDK EOF failed to settle/drain actual tool", ctx.Err())
			}
			expected := int32(1)
			if eof {
				expected = 0
			}
			if targetCalls.Load() != expected || modelCalls.Load() != 2 {
				t.Fatal("actual SDK counts", targetCalls.Load(), modelCalls.Load())
			}
			m.Close()
		})
	}
}
