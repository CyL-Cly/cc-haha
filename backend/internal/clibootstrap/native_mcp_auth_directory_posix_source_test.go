//go:build !windows

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeMCPDirectorySource struct {
	Initial, Next, Third string
	Now                  float64
	BeforeFiles          map[string]string
	AfterFiles           map[string]*string
	Outcome              json.RawMessage
	Events               []struct{ Operation, Path, Directory string }
	Caps                 struct{ Blocked, SecureReads []string }
}

type nativeMCPDirectoryNoHTTP struct{ calls *atomic.Int32 }

func (transport nativeMCPDirectoryNoHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("current-directory cached refresh must not request provider HTTP")
}

func TestActualNativeMCPRefreshCurrentDirectoryAndAdmittedLeaseAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"refresh-lock", "held-refresh-directory"} {
		t.Run(profile, func(t *testing.T) {
			var source nativeMCPDirectorySource
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
				source = actualMCPDirectorySource(t, n.Home, profile)
				restoreNativeMCPCacheFiles(t, source)
				n.Now = func() time.Time { return time.UnixMilli(int64(source.Now)) }
				n.NowMillis = func() float64 { return source.Now }
			}, nil)
			options := m.nativeMCPAuthOptions(m.options.Platform)
			if options.NativeConfigDirectory == nil {
				t.Fatal("actual native factory did not bind its current-directory producer")
			}
			var httpCalls atomic.Int32
			options.HTTPClient = &http.Client{Transport: nativeMCPDirectoryNoHTTP{&httpCalls}}
			var mu sync.Mutex
			mkdirs, leases := []string{}, map[string]bool{}
			clock := options.Now
			options.Now = func() time.Time {
				for _, directory := range []string{source.Initial, source.Next, source.Third} {
					paths, err := filepath.Glob(filepath.Join(directory, "mcp-refresh-*.lock.lock"))
					if err != nil {
						t.Error(err)
					}
					for _, path := range paths {
						if stat, err := os.Stat(path); err == nil && stat.IsDir() {
							mu.Lock()
							leases[path] = true
							mu.Unlock()
						}
					}
				}
				return clock()
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			options.MkdirAll = func(path string, mode os.FileMode) error {
				if err := os.MkdirAll(path, mode); err != nil {
					return err
				}
				mu.Lock()
				mkdirs = append(mkdirs, path)
				mu.Unlock()
				if profile == "held-refresh-directory" {
					close(started)
					<-release
				}
				return nil
			}
			auth, err := mcpauth.New(options)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := auth.Provider(context.Background(), "owned-current-lock", mainObj([]byte(`{"type":"http","url":"http://127.0.0.1:1/never-request","scope":"user"}`)))
			if err != nil {
				t.Fatal(err)
			}
			set := func(directory string) {
				if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(directory)); err != nil {
					t.Fatal(err)
				}
			}
			defer set(source.Initial)
			set(source.Next)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type result struct {
				value *protocol.JSONObject
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := provider.RefreshAuthorization(ctx, "fake-current-cache-refresh")
				done <- result{value, err}
			}()
			if profile == "held-refresh-directory" {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("actual refresh mkdir did not settle before held completion")
				}
				set(source.Third)
				unblock()
			}
			var reply result
			select {
			case reply = <-done:
			case <-ctx.Done():
				t.Fatal("actual cached refresh did not join", ctx.Err())
			}
			if reply.err != nil || reply.value == nil {
				t.Fatal("actual cached MCP refresh", reply.err)
			}
			actual, _ := reply.value.MarshalJSON()
			want, _ := protocol.ParseAndStringifyJavaScriptJSON(source.Outcome)
			if !bytes.Equal(actual, want) || httpCalls.Load() != 0 {
				t.Error("cached original token consumer or unexpected provider refresh differs", string(actual), string(want), httpCalls.Load())
			}
			expectedLeases := map[string]bool{}
			for _, event := range source.Events {
				if event.Operation == "lock" {
					expectedLeases[event.Path+".lock"] = true
				}
			}
			mu.Lock()
			if !reflect.DeepEqual(leases, expectedLeases) || !reflect.DeepEqual(mkdirs, []string{source.Next}) {
				t.Error("actual current/captured refresh directory differs from original Source", leases, expectedLeases, mkdirs)
			}
			mu.Unlock()
			for path := range leases {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Error("actual refresh lease not released", path, err)
				}
			}
			if _, err := m.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
				t.Error("ordinary configuration-directory guard changed", err)
			}
			t.Logf("Source native refresh=%s real mkdir=%v physical lease=%v; no provider HTTP", profile, mkdirs, leases)
		})
	}
}

func actualMCPDirectorySource(t *testing.T, home, profile string) nativeMCPDirectorySource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual current-directory MCP Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-mcp-auth-cache.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_DIR_MCP_AUTH_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"home": home, "profile": profile, "holdStage": "read"}))
	output, err := command.CombinedOutput()
	var source nativeMCPDirectorySource
	if err != nil || json.Unmarshal(output, &source) != nil || source.BeforeFiles == nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole current-directory MCP Source %v %.5000s", err, output)
	}
	return source
}

func restoreNativeMCPCacheFiles(t *testing.T, source nativeMCPDirectorySource) {
	t.Helper()
	for directory, text := range source.BeforeFiles {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "mcp-needs-auth-cache.json"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertNativeMCPCacheFiles(t *testing.T, source nativeMCPDirectorySource) {
	t.Helper()
	for directory, expected := range source.AfterFiles {
		path := filepath.Join(directory, "mcp-needs-auth-cache.json")
		actual, err := os.ReadFile(path)
		if expected == nil {
			if !os.IsNotExist(err) {
				t.Error("Source captured clear-all path differs", path, err, string(actual))
			}
			continue
		}
		wanted, parse := protocol.ParseAndStringifyJavaScriptJSON([]byte(*expected))
		got, parseActual := protocol.ParseAndStringifyJavaScriptJSON(actual)
		if err != nil || parse != nil || parseActual != nil || !bytes.Equal(wanted, got) {
			t.Errorf("Source current/captured cache differs at %s: actual %s expected %s (%v/%v/%v)", path, actual, *expected, err, parse, parseActual)
		}
	}
}

// The unchanged native read primitive blocks in the actual FIFO fd. Replacing
// the directory entry before EOF restores the original regular file for later
// writes, while the already-open descriptor keeps its admitted old target.
func holdNativeMCPCacheRead(t *testing.T, path, text string) (<-chan struct{}, func()) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	go func() {
		defer close(done)
		file, err := os.OpenFile(path, os.O_WRONLY, 0600)
		if err != nil {
			t.Error(err)
			close(started)
			return
		}
		close(started)
		<-release
		if err = os.Remove(path); err == nil {
			err = os.WriteFile(path, []byte(text), 0600)
		}
		if err == nil {
			_, err = file.Write([]byte(text))
		}
		if err != nil {
			t.Error(err)
		}
		_ = file.Close()
	}()
	t.Cleanup(func() {
		unblock()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("actual cache FIFO producer did not join")
		}
	})
	return started, unblock
}

func TestActualNativeMCPAuthenticationCurrentDirectoryAndCapturedChainAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"warm", "held-read", "queued-mark", "queued-clear", "queued-clear-all"} {
		t.Run(profile, func(t *testing.T) {
			var source nativeMCPDirectorySource
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
				source = actualMCPDirectorySource(t, n.Home, profile)
				restoreNativeMCPCacheFiles(t, source)
				n.NowMillis = func() float64 { return source.Now }
			}, nil)
			// This is the same actual native factory used by newNativeMCP. Reuse
			// this one cache and the MainPrint's canonical environment throughout;
			// the ordinary settings/profile guard is tested separately below.
			cache, err := newNativeMCPAuthenticationCache(m.options.Native)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			t.Cleanup(func() { _ = cache.Join(context.Background()) })
			set := func(directory string) {
				if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(directory)); err != nil {
					t.Fatal(err)
				}
			}
			defer set(source.Initial)
			cached := func(name string) bool {
				value, err := cache.Cached(ctx, name)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			outcome := map[string]bool{}
			if profile == "warm" {
				outcome["before"] = cached("old")
				set(source.Next)
				outcome["warm"], outcome["warmNext"] = cached("old"), cached("next")
				if err := cache.BeginBatch(ctx); err != nil {
					t.Fatal(err)
				}
				outcome["coldNext"], outcome["coldOld"] = cached("next"), cached("old")
			} else {
				started, release := holdNativeMCPCacheRead(t, filepath.Join(source.Initial, "mcp-needs-auth-cache.json"), source.BeforeFiles[source.Initial])
				if profile == "held-read" {
					type cacheResult struct {
						value bool
						err   error
					}
					result := make(chan cacheResult, 1)
					go func() { value, err := cache.Cached(ctx, "old"); result <- cacheResult{value, err} }()
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal("actual cold cache descriptor was not opened")
					}
					set(source.Next)
					shared := make(chan cacheResult, 1)
					go func() { value, err := cache.Cached(ctx, "next"); shared <- cacheResult{value, err} }()
					// Releasing the real fd cannot create another read promise:
					// either arrival still consumes the same admitted old read.
					release()
					for key, pending := range map[string]<-chan cacheResult{"old": result, "shared": shared} {
						select {
						case response := <-pending:
							if response.err != nil {
								t.Fatal(response.err)
							}
							outcome[key] = response.value
						case <-ctx.Done():
							t.Fatal("actual shared cache read did not settle", ctx.Err())
						}
					}
					if err := cache.BeginBatch(ctx); err != nil {
						t.Fatal(err)
					}
					outcome["coldNext"] = cached("next")
				} else {
					cache.Mark("first")
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal("actual first queued cache read was not opened")
					}
					set(source.Next)
					if profile == "queued-mark" {
						cache.Mark("second")
					} else {
						var target *string
						if profile == "queued-clear" {
							name := "target"
							target = &name
						}
						// Caller cancellation stops only its wait; Source's queued
						// best-effort clear still retains the call-time path.
						wait, stop := context.WithCancel(ctx)
						stop()
						if err := cache.Clear(wait, target); !errors.Is(err, context.Canceled) {
							t.Fatal("held clear's caller wait", err)
						}
					}
					set(source.Third)
					release()
					if err := cache.Join(ctx); err != nil {
						t.Fatal(err)
					}
					outcome["afterCurrentThird"] = cached("third")
				}
			}
			var expected map[string]bool
			if err := json.Unmarshal(source.Outcome, &expected); err != nil || !reflect.DeepEqual(outcome, expected) {
				t.Error("warm/read/current cache outcome differs from original Source", outcome, expected, err)
			}
			assertNativeMCPCacheFiles(t, source)
			if _, err := m.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
				t.Error("ordinary configuration-directory guard changed", err)
			}
			t.Logf("Source current-directory profile=%s outcome=%v; same native factory/cache and canonical environment", profile, outcome)
		})
	}
}
