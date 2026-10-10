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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativeSandboxDefaultMainPrintAutoAllowedBashActualToolReadPostAndCanonical(t *testing.T) {
	if os.Getenv("CC_HAHA_SANDBOX_PHYSICAL_BROKER") != "1" {
		t.Skip("dedicated nonnested actual Seatbelt broker required")
	}
	for _, foreground := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreground=%v", foreground), func(t *testing.T) {
			var calls, asks atomic.Int32
			var artifact string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				round := calls.Add(1)
				switch round {
				case 1:
					if !strings.Contains(string(body), `"name":"Bash"`) || !strings.Contains(string(body), `"name":"Read"`) {
						t.Errorf("actual default catalog absent")
					}
					emitNativeTaskTool(w, "sandbox-actual-bash", "Bash", map[string]any{"command": "printf 'same Harness sandbox bytes' > " + bashSandboxQuote(artifact) + "; cat " + bashSandboxQuote(artifact), "description": "actual native sandbox"})
				case 2:
					if !strings.Contains(string(body), "same Harness sandbox bytes") {
						t.Errorf("physical Bash result absent from next POST")
					}
					emitNativeTaskTool(w, "sandbox-actual-read", "Read", map[string]any{"file_path": artifact})
				default:
					if !strings.Contains(string(body), "same Harness sandbox bytes") {
						t.Errorf("actual Read missing")
					}
					emitPlanFixture(w, int(round), "", "SANDBOX_SHARED_HARNESS_FINISHED")
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
				path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
				data := `{"enabledPlugins":{"sample@mkt":false},"sandbox":{"enabled":true,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}`
				if e := os.WriteFile(path, []byte(data), 0600); e != nil {
					t.Fatal(e)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = nil
				o.Arguments.AllowedTools = nil
				o.Arguments.PermissionMode = "default"
				o.ForegroundShellProgress = foreground
				o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, _ queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
					asks.Add(1)
					return queryloop.ToolPermission{Behavior: "deny", Input: in}, nil
				}
			})
			artifact = filepath.Join(m.options.Native.CWD, "sandbox-real-model-result.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Use actual sandboxed Bash then Read"})})); e != nil {
				t.Fatal(e)
			}
			data, e := os.ReadFile(artifact)
			if e != nil || string(data) != "same Harness sandbox bytes" {
				t.Fatal("actual artifact", e, string(data))
			}
			if calls.Load() != 3 || asks.Load() != 0 {
				t.Fatal("Source auto-allow changed model/approval counts", calls.Load(), asks.Load())
			}
			var raw []byte
			e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { raw = rawMain(m.Controller.History()); return nil })
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(raw), "same Harness sandbox bytes") || !strings.Contains(string(raw), "sandbox-actual-bash") || !strings.Contains(string(raw), "sandbox-actual-read") {
				t.Fatal("canonical original history lacks actual tool chain")
			}
			if e = m.ShutdownSession(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
