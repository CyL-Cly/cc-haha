package clibootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeDoctorLocalContextProducersWithoutStartingASKAndGate(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`[{"tag_name":"v3.0.1","assets":[{"name":"claude-haha-cli-manifest.json"}]}]`))
			}))
			defer releases.Close()
			var model atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				model.Add(1)
				t.Error("Doctor without MCP must not open model", r.URL.Path)
				w.WriteHeader(500)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				agentsWrite(t, filepath.Join(n.CWD, ".mcp.json"), `{actual-invalid-json}`)
				n.Environment["CC_HAHA_CLI_RELEASE_API"] = releases.URL
				n.Environment["ENABLE_PID_BASED_VERSION_LOCKING"] = "true"
				n.Environment["BASH_MAX_OUTPUT_LENGTH"] = "170000"
				if disabled {
					n.Environment["DISABLE_DOCTOR_COMMAND"] = "true"
				}
			}, nil)
			before := m.Controller.SessionID()
			memory := filepath.Join(m.options.Native.CWD, "CLAUDE.md")
			agentsWrite(t, memory, strings.Repeat("physical large instruction ", 2000))
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			text, e := m.NativeDoctorCommand(ctx)
			if disabled {
				if e == nil || !strings.Contains(e.Error(), "disabled native /doctor") {
					t.Fatal("disabled Source gate admitted", e, text)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				for _, expected := range []string{"Diagnostics", "Go native", "MCP Configuration Issues", "Latest version: 3.0.1", "Stable version: 3.0.1", "BASH_MAX_OUTPUT_LENGTH: Capped from 170000 to 150000", "Context diagnostics", "Large CLAUDE.md file detected", "Version Locks", "No active version locks", "Plugins", "Agents"} {
					if !strings.Contains(text, expected) {
						t.Fatal("actual native local Doctor omits", expected, text)
					}
				}
			}
			if model.Load() != 0 || m.Controller.SessionID() != before {
				t.Fatal("Doctor changed inference/session", model.Load(), before, m.Controller.SessionID())
			}
		})
	}
}
