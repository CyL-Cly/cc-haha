package cliauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceCLIListenerCloseAndInvalidCallbacks(t *testing.T) {
	for _, mode := range []string{"pending-close", "no-code", "bad-state", "wrongpath"} {
		t.Run(mode, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			_ = os.Mkdir(filepath.Join(home, ".claude"), 0700)
			fixture, _ := filepath.Abs("../../testdata/native-cli-auth-login.fixture.ts")
			profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER=fixture", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE=1", "CC_HAHA_CLI_LOGIN_PEER=https://127.0.0.1:1", "CC_HAHA_CLI_LOGIN_LISTENER_ONLY=1", "CC_HAHA_CLI_LOGIN_LISTENER_MODE=" + mode, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "USER_TYPE=external", "NODE_ENV=production"}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			raw, e := cmd.Output()
			if e != nil {
				t.Fatal("actual Source listener", e, stderr.String())
			}
			var original map[string]any
			if e = json.Unmarshal(raw, &original); e != nil {
				t.Fatal(e, string(raw))
			}
			listener := cliauth.NewOAuthListener("/callback")
			port, e := listener.Start(0)
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			actual := map[string]any{"wrongPath": nil}
			responseDone := make(chan map[string]any, 1)
			browser := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			code, e := listener.Wait(ctx, "fixture-state", func() error {
				if mode == "wrongpath" {
					r, e := browser.Get(fmt.Sprintf("http://localhost:%d/other", port))
					if e != nil {
						return e
					}
					data, _ := io.ReadAll(r.Body)
					r.Body.Close()
					actual["wrongPath"] = map[string]any{"status": float64(r.StatusCode), "body": string(data)}
				}
				address := fmt.Sprintf("http://localhost:%d/callback?state=fixture-state", port)
				if mode != "no-code" {
					address += "&code=fixture-code"
				}
				if mode == "bad-state" {
					address += "&state=wrong"
					address = fmt.Sprintf("http://localhost:%d/callback?state=wrong&code=fixture-code", port)
				}
				r, e := browser.Get(address)
				if e != nil {
					responseDone <- map[string]any{"transportError": e.Error()}
					return e
				}
				data, _ := io.ReadAll(r.Body)
				r.Body.Close()
				var location any
				if v := r.Header.Get("Location"); v != "" {
					location = v
				}
				responseDone <- map[string]any{"status": float64(r.StatusCode), "location": location, "body": string(data)}
				return nil
			})
			if e != nil {
				actual["error"] = e.Error()
			} else {
				actual["code"] = code
			}
			listener.Close()
			actual["response"] = <-responseDone
			if !reflect.DeepEqual(original, actual) {
				t.Fatalf("actual Source listener %s Go %v", raw, actual)
			}
		})
	}
}
