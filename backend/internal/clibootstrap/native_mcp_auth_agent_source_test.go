package clibootstrap

import (
	"encoding/json"
	"testing"
)

// This deliberately audits original Agent admission rather than claiming the
// Go child flow is complete. The parent/source result is an input contract for
// the separate ordinary Agent implementation; it never prepares a Go catalog.
func TestActualSourceAgentInheritsAuthenticationToolAndScopesAppStateWrite(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "async"}[background], func(t *testing.T) {
			x := newTerminalMCPAuthFixture(t, false)
			output := runActualMCPAuthenticationSourceWithInput(t, x, "auth-tool-agent", map[string]any{"observeMCPLog": true, "background": background})
			var source struct {
				AgentAdmission struct {
					Background, SameTool, SharesSetAppState bool
					Tools, CapturedToolsAfterAuth           []string
					Clients                                 []struct{ Name, Type string }
				}
				Connected struct{ Tools []string }
			}
			if err := json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			a := source.AgentAdmission
			if !a.SameTool || a.Background != background || a.SharesSetAppState == background || len(a.Tools) != 1 || a.Tools[0] != "mcp__auth__authenticate" || len(a.CapturedToolsAfterAuth) != 1 || a.CapturedToolsAfterAuth[0] != a.Tools[0] || len(a.Clients) != 1 || a.Clients[0].Type != "needs-auth" {
				t.Fatal("actual runAgent inherited tool/context changed", string(output))
			}
			want := "mcp__auth__inspect"
			if background {
				want = "mcp__auth__authenticate"
			}
			if len(source.Connected.Tools) != 1 || source.Connected.Tools[0] != want || x.tokens.Load() != 1 || x.connects.Load() != 1 || x.models.Load() != 0 {
				t.Fatal("actual Agent OAuth parent state differs", want, string(output), x.tokens.Load(), x.connects.Load(), x.models.Load())
			}
			t.Log(string(output))
		})
	}
}
