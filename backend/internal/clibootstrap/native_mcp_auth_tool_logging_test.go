package clibootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceAuthToolSuccessAndLateFailureReachOwnedMCPFiles(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "late-callback-failure"}[failed], func(t *testing.T) {
			addresses := make(chan string, 1)
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{headless: true, tools: []string{"mcp__auth__authenticate"}, model: func(x *terminalMCPAuthFixture, w http.ResponseWriter, r *http.Request, body []byte) {
				n := x.models.Add(1)
				if n == 1 {
					emitPlanFixture(w, int(n), "mcp__auth__authenticate", "")
				} else if n == 2 {
					parts := strings.Split(authenticationResultMessage(body), "\n\n")
					if len(parts) != 3 {
						t.Error("actual model did not receive authorization URL")
					} else {
						addresses <- parts[1]
					}
					emitPlanFixture(w, int(n), "", "Authorization link received")
				} else {
					t.Error("diagnostic continuation started another model", n)
				}
			}})
			mode, field, prefix := "auth-tool", "debug", "OAuth complete, reconnected with "
			if failed {
				mode, field, prefix = "auth-tool-late-error", "error", "OAuth flow failed after tool-triggered start:"
			}
			output := runActualMCPAuthenticationSourceWithInput(t, x, mode, map[string]any{"observeMCPLog": true})
			var source struct{ Diagnostics []map[string]any }
			if err := json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			expected := ""
			for _, row := range source.Diagnostics {
				if message, _ := row[field].(string); strings.HasPrefix(message, prefix) {
					expected = message
				}
			}
			if expected == "" {
				t.Fatal("actual Source omitted target MCP diagnostic", string(output))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err := x.m.RunTurn(ctx, x.user("Use the actual authentication tool")); err != nil {
				t.Fatal(err)
			}
			var address string
			select {
			case address = <-addresses:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			callback := terminalMCPAuthCallback(t, address)
			if failed {
				u, err := url.Parse(callback)
				if err != nil {
					t.Fatal(err)
				}
				query := u.Query()
				query.Del("code")
				query.Set("error", "access_denied")
				query.Set("error_description", "private owned denied")
				u.RawQuery = query.Encode()
				callback = u.String()
			}
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal("actual private OAuth callback", err)
			}
			_ = response.Body.Close()
			for {
				found := false
				paths, err := filepath.Glob(filepath.Join(x.f.config, "Cache", "*", "mcp-logs-auth", "*.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range paths {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
						var row map[string]any
						if json.Unmarshal([]byte(line), &row) == nil && row[field] == expected {
							found = row["sessionId"] == x.m.Controller.SessionID() && row["cwd"] == x.f.n.CWD && row["timestamp"] != ""
						}
					}
				}
				if found {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("actual AuthTool did not write Source diagnostic", expected, paths)
				case <-time.After(time.Millisecond):
				}
			}
			x.m.Close()
			if x.models.Load() != 2 {
				t.Fatal("background diagnostic changed actual model count", x.models.Load())
			}
			t.Logf("actual Source and native Harness MCP %s=%q, next-model-posts=%d", field, expected, x.models.Load())
		})
	}
}
