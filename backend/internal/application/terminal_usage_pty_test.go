package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIUsageErrorRetryEscAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; Windows ConPTY requires host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"subscriber", "api-key"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "owned-usage-pty"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "testdata/terminal_usage_pty.py", binary, home, profile)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_USAGE_PTY_HELPER=1", "CC_HAHA_USAGE_PTY_PROFILE=" + profile}
			capturePublicGoCoverage(t, cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal("actual public Usage", err, string(output))
			}
			if !strings.Contains(string(output), `"terminalRestored": true`) {
				t.Fatal(string(output))
			}
		})
	}
}
func TestNativeTerminalUsagePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_USAGE_PTY_HELPER") != "1" {
		t.Skip("owned isolated Usage CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-usage-pty")); err != nil {
		t.Fatal(err)
	}
	profile := os.Getenv("CC_HAHA_USAGE_PTY_PROFILE")
	var api, models atomic.Int32
	joined := make(chan struct{})
	proxy, ca := terminalUsagePrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := api.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/oauth/usage" || r.Header.Get("Authorization") != "Bearer fake-usage-token" {
			t.Errorf("native request %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":"owned-usage-error"}`))
		case 2:
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":23.9,"resets_at":null},"seven_day":{"utilization":63,"resets_at":null},"seven_day_sonnet":{"utilization":37,"resets_at":null},"extra_usage":{"is_enabled":true,"monthly_limit":10000,"used_credits":2390,"utilization":23.9}}`))
		default:
			if err := os.WriteFile(filepath.Join(home, "usage-held-http.started"), []byte(r.URL.Path), 0600); err != nil {
				t.Error(err)
			}
			<-r.Context().Done()
			close(joined)
		}
	}))
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "/usage") {
			t.Error("local Usage reached model")
		}
		count := models.Add(1)
		response(w, "usage-next", "", nil, "PUBLIC_USAGE_NEXT_HUMAN_COMPLETE_"+strconv.Itoa(int(count)))
	}))
	defer model.Close()
	o := terminalUsageFixtureOptions(t, model.URL, proxy, ca)
	// This receipt compares the original 24-bit Source theme palette. Bind the
	// actual terminal's advertised color capability so the renderer preserves
	// that profile rather than quantizing RGB into xterm's 256-color table.
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["COLORTERM"] = "truecolor"
	}
	o.Stderr = os.Stderr
	if profile == "api-key" {
		for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
			env["ANTHROPIC_API_KEY"] = "fake-only-loopback"
		}
		_ = os.Remove(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json"))
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if profile == "subscriber" {
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("actual Usage cancellation not joined")
		}
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "api": api.Load(), "models": models.Load(), "code": code}
	encoded, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(home, "usage-receipt.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "usage-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY parent did not inspect termios")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
