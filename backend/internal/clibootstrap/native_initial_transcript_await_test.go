//go:build darwin || linux

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNonbareInitialTranscriptRejectsHeldCanonicalIOBeforeModelAndRecovers(t *testing.T) {
	for _, profile := range []struct {
		implementation string
		cancelled      bool
	}{{"source", false}, {"native", false}, {"source", true}, {"native", true}} {
		implementation, name := profile.implementation, profile.implementation
		if profile.cancelled {
			name += "-model-cancel"
		}
		t.Run(name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
			var models atomic.Int32
			var mu sync.Mutex
			var bodies [][]byte
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/__fixture/swap-fifo" {
					path := r.URL.Query().Get("path")
					if !inside(f.config, path) || !strings.HasSuffix(path, ".jsonl") {
						http.Error(w, "unowned FIFO", 403)
						return
					}
					if err := os.Remove(path); err != nil {
						t.Error(err)
						http.Error(w, "remove", 500)
						return
					}
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Error(err)
						http.Error(w, "mkfifo", 500)
						return
					}
					_, _ = io.WriteString(w, "owned FIFO")
					return
				}
				if r.Method != "POST" || r.URL.Path != "/v1/messages" {
					t.Error("unowned HTTP", r.Method, r.URL.Path)
					http.Error(w, "unowned", 403)
					return
				}
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				emitPlanFixture(w, int(models.Add(1)), "", "INITIAL_RECORD_MODEL_DONE")
			}))
			defer peer.Close()
			if implementation == "source" {
				bun := os.Getenv("CC_HAHA_SOURCE_BUN")
				if bun == "" {
					t.Skip("pinned Source required")
				}
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-initial-record.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = f.home
				cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=" + filepath.Join(f.home, "bin"), "USE_BUILTIN_RIPGREP=false", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_INITIAL_TRANSCRIPT_FIXTURE=1", "ANTHROPIC_API_KEY=offline-owned-initial-record", "ANTHROPIC_BASE_URL=" + peer.URL, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_ATTRIBUTION_HEADER=0"}
				cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "peer": peer.URL, "cancelled": profile.cancelled}))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				output, err := cmd.Output()
				if err != nil {
					t.Fatal("actual Source initial transcript", err, string(output), stderr.String())
				}
				var result struct {
					FirstError, SecondError, ThirdError string
					Held                                struct{ Pending, FIFO, Cancelled bool }
					Caps                                struct{ Blocked []string }
					Canonical                           []protocol.JSONObject
					RestoredMessages                    []protocol.JSONObject
				}
				if err = json.Unmarshal(output, &result); err != nil || result.FirstError != "" || !result.Held.Pending || !result.Held.FIFO || result.Held.Cancelled != profile.cancelled || !strings.Contains(result.SecondError, "Refusing non-regular transcript target") || result.ThirdError != "" || len(result.Caps.Blocked) != 0 || models.Load() != 2 || !bytes.Contains(rawMain(result.RestoredMessages), []byte("INITIAL_RECORD_NEXT_HUMAN")) {
					t.Fatal("actual Source held record", err, string(output), stderr.String(), models.Load())
				}
				t.Logf("actual Source nonbare held FIFO rejected before model; cancel=%v next human and physical recovery retained; modelPosts=%d", profile.cancelled, models.Load())
			} else {
				f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
				f.n.Environment["ANTHROPIC_API_KEY"] = "offline-owned-initial-record"
				f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
				disabled := false
				options := MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual initial transcript", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}}
				m, err := NewMainPrint(options)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err = m.StartSession(ctx); err != nil {
					m.Close()
					t.Fatal(err)
				}
				defer m.Close()
				user := func(text string) protocol.JSONObject {
					return objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}))
				}
				if err = m.RunTurn(ctx, user("INITIAL_RECORD_FIRST")); err != nil {
					t.Fatal(err)
				}
				var target string
				if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					var e error
					target, e = m.Controller.Writer().ComputedTranscriptPath(scope)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err = syscall.Mkfifo(target, 0600); err != nil {
					t.Fatal(err)
				}
				f.n.mu.Lock()
				startTrace := len(f.n.Trace)
				f.n.mu.Unlock()
				second := make(chan error, 1)
				secondContext, cancelSecond := context.WithCancel(ctx)
				defer cancelSecond()
				go func() { second <- m.RunTurn(secondContext, user("INITIAL_RECORD_HELD_REJECTED")) }()
				waiting := false
				for !waiting {
					f.n.mu.Lock()
					for _, row := range f.n.Trace[startTrace:] {
						waiting = waiting || row.Operation == "openWrite" && row.Path == target
					}
					f.n.mu.Unlock()
					if waiting {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("actual FIFO open not reached", ctx.Err())
					case <-time.After(time.Millisecond):
					}
				}
				if profile.cancelled {
					cancelSecond()
					select {
					case err := <-second:
						t.Fatal("model cancellation escaped held initial record", err)
					default:
					}
				}
				reader, err := syscall.Open(target, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				secondError := <-second
				_ = syscall.Close(reader)
				if err = os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if secondError == nil || !strings.Contains(secondError.Error(), "Refusing non-regular transcript target") || models.Load() != 1 {
					t.Errorf("ordinary nonbare input reached model before canonical record rejection: error=%v modelPosts=%d", secondError, models.Load())
				}
				if err = m.RunTurn(ctx, user("INITIAL_RECORD_NEXT_HUMAN")); err != nil {
					t.Fatal("next human recovery", err)
				}
				canonical, err := os.ReadFile(target)
				if err != nil || !bytes.Contains(canonical, []byte("INITIAL_RECORD_NEXT_HUMAN")) || models.Load() != 2 {
					t.Error("actual next human canonical recovery", err, string(canonical), models.Load())
				}
				if err = m.ShutdownSession(ctx); err != nil {
					t.Fatal(err)
				}
				sid := m.Controller.SessionID()
				m.Close()
				fresh, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: f.n.Environment, CWD: f.cwd, Platform: "darwin", ManagedRoot: f.managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
				if err != nil {
					t.Fatal(err)
				}
				fresh.RunSecurity, fresh.Temp = f.n.RunSecurity, f.n.Temp
				options.Native, options.Arguments.Resume = fresh, sid
				resumed, err := NewMainPrint(options)
				if err != nil {
					t.Fatal(err)
				}
				defer resumed.Close()
				if err = resumed.StartSession(ctx); err != nil {
					t.Fatal("fresh native resume", err)
				}
				if err = resumed.RunTurn(ctx, user("INITIAL_RECORD_PHYSICAL_RESUME")); err != nil {
					t.Fatal("resumed actual human", err)
				}
				mu.Lock()
				lastBody := append([]byte(nil), bodies[len(bodies)-1]...)
				mu.Unlock()
				if models.Load() != 3 || !bytes.Contains(lastBody, []byte("INITIAL_RECORD_NEXT_HUMAN")) || !bytes.Contains(lastBody, []byte("INITIAL_RECORD_PHYSICAL_RESUME")) {
					t.Fatal("fresh MainPrint did not use own canonical file in next HTTP", models.Load())
				}
				if err = resumed.ShutdownSession(ctx); err != nil {
					t.Fatal(err)
				}
				t.Logf("actual native nonbare held record cancel=%v rejected; next human+fresh physical resume HTTP retained; modelPosts=%d", profile.cancelled, models.Load())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) < 2 || !bytes.Contains(bodies[len(bodies)-1], []byte("INITIAL_RECORD_NEXT_HUMAN")) {
				t.Error("next actual model did not consume recovered human")
			}
		})
	}
}
