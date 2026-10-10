package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualSourceAndPublicSetupTokenManualEditingCopyRetryAndCancel(t *testing.T) {
	buildRoot := t.TempDir()
	binary := filepath.Join(buildRoot, "setup-cli")
	build := exec.Command("go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	source, _ := filepath.Abs("../../testdata/native-cli-setup-token.fixture.ts")
	driver, _ := filepath.Abs("../../testdata/setup-token-pty.py")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	for _, mode := range []string{"manual-copy-edit", "exchange-retry", "exchange-201-retry", "cancel-browser", "terminate-browser", "automatic-during-invalid"} {
		t.Run(mode, func(t *testing.T) {
			type observation struct {
				Bodies    []map[string]any
				Addresses []string
				Exit      int
				Output    string
				Termios   bool
			}
			observations := map[string]observation{}
			for _, language := range []string{"source", "go"} {
				root, e := filepath.EvalSymlinks(t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				home, config, tmp := filepath.Join(root, "home"), filepath.Join(root, "home", ".claude"), filepath.Join(root, "tmp")
				for _, p := range []string{home, config, tmp} {
					if e = os.MkdirAll(p, 0700); e != nil {
						t.Fatal(e)
					}
				}
				old := []byte(`{"unknown":{"preserve":true},"forceLoginMethod":"console","forceLoginOrgUUID":"fixture-org"}`)
				if e = os.WriteFile(filepath.Join(config, "settings.json"), old, 0600); e != nil {
					t.Fatal(e)
				}
				var mu sync.Mutex
				var bodies []map[string]any
				peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/oauth/token":
						var body map[string]any
						if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
							t.Error(e)
						}
						mu.Lock()
						bodies = append(bodies, body)
						attempt := len(bodies)
						mu.Unlock()
						if (mode == "exchange-retry" || mode == "exchange-201-retry") && attempt == 1 {
							if mode == "exchange-201-retry" {
								w.WriteHeader(201)
								fmt.Fprint(w, `{"access_token":"FAKE_UNUSED_201"}`)
								return
							}
							w.WriteHeader(503)
							fmt.Fprint(w, `{"error":"owned-unavailable"}`)
							return
						}
						fmt.Fprint(w, `{"access_token":"FAKE_MATRIX_SECRET","refresh_token":"unused","scope":"user:inference","expires_in":31536000}`)
					case "/":
						fmt.Fprint(w, `{}`) // public Source prefetch is an external no-account boundary
					case "/api/oauth/profile":
						w.WriteHeader(403)
						fmt.Fprint(w, `{"error":"profile-scope"}`)
					default:
						t.Errorf("unexpected setup endpoint %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				ca := filepath.Join(root, "ca.pem")
				os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
				steps := []map[string]any{}
				extra := []string{}
				switch mode {
				case "manual-copy-edit":
					extra = []string{"CC_HAHA_SETUP_MANUAL=1", "CC_HAHA_CLI_LOGIN_HOLD_BROWSER=1"}
					steps = []map[string]any{{"wait": "Paste code here if prompted", "send": "c"}, {"wait": "Copied!", "send": "junk"}, {"wait": "junk", "send": "\x03"}, {"delay": 0.1, "send": "invalid\r"}, {"wait": "Invalid code. Please make sure the full code was copied", "send": "\r"}, {"wait": "Retrying", "delay": 1.15, "send": "\x1b[200~goodX#manual-state#extra\x1b[201~"}, {"wait": "#extra", "send": "\x01"}, {"delay": 0.05, "send": "\x1b[C"}, {"delay": 0.05, "send": "\x1b[C"}, {"delay": 0.05, "send": "\x1b[C"}, {"delay": 0.05, "send": "\x1b[C"}, {"delay": 0.05, "send": "\x1b[3~"}, {"delay": 0.1, "send": "\r"}}
				case "exchange-201-retry":
					steps = []map[string]any{{"wait": "Token exchange failed (201): Created", "send": "\r"}}
				case "exchange-retry":
					steps = []map[string]any{{"wait": "Request failed with status code 503", "send": "\r"}}
				case "terminate-browser":
					extra = []string{"CC_HAHA_SETUP_MANUAL=1", "CC_HAHA_CLI_LOGIN_HOLD_BROWSER=1", "CC_HAHA_SETUP_SOURCE_PUBLIC=1"}
					steps = []map[string]any{{"wait": "Paste code here if prompted", "signal": "SIGTERM"}}
				case "cancel-browser":
					extra = []string{"CC_HAHA_SETUP_MANUAL=1", "CC_HAHA_CLI_LOGIN_HOLD_BROWSER=1", "CC_HAHA_SETUP_SOURCE_PUBLIC=1"}
					steps = []map[string]any{{"wait": "Paste code here if prompted", "signal": "SIGINT"}}
				case "automatic-during-invalid":
					// The automatic callback is submitted by the test only after the invalid
					// input is rendered. No retry key is sent; the active promise must win.
					extra = []string{"CC_HAHA_SETUP_MANUAL=1", "CC_HAHA_CLI_LOGIN_HOLD_BROWSER=1"}
					steps = []map[string]any{{"wait": "Paste code here if prompted", "send": "invalid\r"}, {"wait": "Invalid code. Please make sure the full code was copied", "write": "browser-gate"}}
				}
				encoded, _ := json.Marshal(steps)
				receipt := filepath.Join(root, "pty-receipt.json")
				environment := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "PATH=/usr/bin:/bin", "TERM=xterm-256color", "SSH_CONNECTION=fixture-remote", "NODE_EXTRA_CA_CERTS=" + ca, "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CC_HAHA_CLI_LOGIN_PEER=" + peer.URL, "CC_HAHA_SETUP_PTY_STEPS=" + string(encoded), "CC_HAHA_SETUP_PTY_RECEIPT=" + receipt}
				environment = append(environment, extra...)
				args := []string{driver, "/usr/bin/sandbox-exec", "-f", profile, binary, "setup-token"}
				if language == "source" {
					args = []string{driver, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", source}
					environment = append(environment, "CC_HAHA_FIXTURE_TEMP_ROOT="+root, "CC_HAHA_CLI_LOGIN_FIXTURE=1", "BUN_OPTIONS=--no-env-file")
				} else {
					environment = append(environment, "CC_HAHA_NATIVE_FIXTURE_ROOT="+root)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				cmd := exec.CommandContext(ctx, "python3", args...)
				cmd.Dir = home
				cmd.Env = environment
				cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
				cmd.WaitDelay = 2 * time.Second
				var out, stderr bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &stderr
				// Observe actual external browser address. The callback transport below
				// is the only extra primitive in the automatic-invalid scenario.
				callbackDone := make(chan error, 1)
				if mode == "automatic-during-invalid" {
					go func() {
						timer := time.NewTicker(5 * time.Millisecond)
						defer timer.Stop()
						for {
							select {
							case <-ctx.Done():
								callbackDone <- ctx.Err()
								return
							case <-timer.C:
							}
							if _, e := os.Stat(filepath.Join(home, "browser-gate")); e != nil {
								continue
							}
							path := filepath.Join(home, "browser-fixture.json")
							if language == "go" {
								path = filepath.Join(root, "browser-fixture.json")
							}
							raw, e := os.ReadFile(path)
							if e != nil {
								continue
							}
							var browser map[string]string
							json.Unmarshal(raw, &browser)
							request, e := http.NewRequestWithContext(ctx, "GET", browser["callback"], nil)
							if e != nil {
								callbackDone <- e
								return
							}
							client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
							response, e := client.Do(request)
							if e == nil {
								response.Body.Close()
							}
							callbackDone <- e
							return
						}
					}()
				}
				runErr := cmd.Run()
				if mode == "automatic-during-invalid" {
					if e := <-callbackDone; e != nil {
						t.Fatal(language, e, out.String(), stderr.String())
					}
				}
				exit := cmd.ProcessState.ExitCode()
				if runErr != nil && mode != "cancel-browser" && mode != "terminate-browser" {
					t.Fatal(language, runErr, out.String(), stderr.String())
				}
				if ctx.Err() != nil {
					t.Fatal(language, "process didn't settle", out.String(), stderr.String())
				}
				cancel()
				peer.Close()
				var terminal struct {
					TermiosRestored bool `json:"termiosRestored"`
					Steps           int  `json:"steps"`
				}
				raw, _ := os.ReadFile(receipt)
				if json.Unmarshal(raw, &terminal) != nil || !terminal.TermiosRestored || terminal.Steps != len(steps) {
					t.Fatal(language, "PTY edit/termios", string(raw), out.String())
				}
				if after, _ := os.ReadFile(filepath.Join(config, "settings.json")); !bytes.Equal(after, old) {
					t.Fatal(language, "settings mutated")
				}
				if _, e := os.Stat(filepath.Join(config, "projects")); !os.IsNotExist(e) {
					t.Fatal(language, "ASK history initialized")
				}
				if strings.Contains(stderr.String(), "FAKE_MATRIX_SECRET") {
					t.Fatal(language, "secret stderr leak")
				}
				mu.Lock()
				copied := append([]map[string]any(nil), bodies...)
				mu.Unlock()
				browserPath := filepath.Join(home, "browser-fixture.json")
				if language == "go" {
					browserPath = filepath.Join(root, "browser-fixture.json")
				}
				raw, _ = os.ReadFile(browserPath)
				var browser map[string]string
				json.Unmarshal(raw, &browser)
				var addresses []string
				if language == "source" {
					rows, _ := os.ReadFile(filepath.Join(home, "browser-attempts.jsonl"))
					for _, line := range bytes.Split(bytes.TrimSpace(rows), []byte("\n")) {
						var item map[string]any
						json.Unmarshal(line, &item)
						if v, ok := item["url"].(string); ok {
							addresses = append(addresses, v)
						}
					}
				} else {
					addresses = []string{browser["url"]}
				}
				observations[language] = observation{copied, addresses, exit, out.String(), terminal.TermiosRestored}
				if mode != "cancel-browser" && mode != "terminate-browser" && !strings.Contains(out.String(), "FAKE_MATRIX_SECRET") {
					t.Fatal(language, "missing displayed setup token", out.String())
				}
				if mode == "manual-copy-edit" {
					if len(copied) != 1 || copied[0]["code"] != "good" || copied[0]["state"] == "manual-state" || copied[0]["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
						t.Fatal(language, "actual manual/edit/originalstate", copied)
					}
					if !strings.Contains(out.String(), "\x1b]52;c;") {
						t.Fatal(language, "actual copy sequence missing", out.String())
					}
				}
				if mode == "exchange-retry" || mode == "exchange-201-retry" {
					if len(copied) != 2 || copied[0]["code_verifier"] != copied[1]["code_verifier"] || copied[0]["state"] == copied[1]["state"] {
						t.Fatal(language, "retry PKCE identity", copied)
					}
				}
				if (mode == "cancel-browser" || mode == "terminate-browser") && len(copied) != 0 {
					t.Fatal(language, "cancel started exchange")
				}
				callback, _ := url.Parse(browser["callback"])
				if callback != nil && callback.Host != "" {
					connection, e := (&http.Client{Timeout: time.Second}).Get(callback.String())
					if e == nil {
						connection.Body.Close()
						t.Fatal(language, "listener survived command exit")
					}
				}
			}
			if observations["source"].Exit != observations["go"].Exit {
				t.Fatalf("actual Source/Go exit mismatch source=%d go=%d", observations["source"].Exit, observations["go"].Exit)
			}
		})
	}
}
