package securestorage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func prefetchStoreFixture(t *testing.T, run func(context.Context, Command) ([]byte, error)) *Store {
	t.Helper()
	home := t.TempDir()
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), ConfigDirExplicit: true, Platform: "darwin", Environment: map[string]string{"USER": "owned-prefetch-account"}, RunSecurity: run})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestKeychainPrefetchDefaultPrimitiveKeepsLaunchContextAndMaxBufferErrorShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX execFile maxBuffer primitive")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// An owned executable, never the machine security(1). exec replaces the
	// shell so the actual capped-output kill cannot leave a child pipe writer.
	program := "#!/bin/sh\ncase \"$6\" in *-credentials*) exec /usr/bin/head -c 4194304 /dev/zero;; *) exit 44;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USER": "owned-max-buffer", "PATH": bin + ":/usr/bin:/bin"}
	current := env
	store, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin", Environment: env, NativeSecurityContext: func() (string, map[string]string) { return home, current }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefetch, err := store.NewKeychainPrefetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer prefetch.Close()
	// Capture happened when execFile was invoked, not when a worker later
	// executes. This intentionally invalid live PATH must not replace it.
	current = map[string]string{"PATH": filepath.Join(home, "absent-current-path")}
	if err := prefetch.RunOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	if context.Cause(ctx) != nil {
		t.Fatal("owned maxBuffer primitive waited for the timeout", context.Cause(ctx))
	}
	if prefetch.arms[0].timedOut || prefetch.arms[1].timedOut {
		t.Fatal("maxBuffer/exit44 incorrectly acquired killed field", prefetch.arms)
	}
	if err := prefetch.Publish(); err != nil {
		t.Fatal(err)
	}
	if store.cachedAt.IsZero() || store.cachedJSON != nil {
		t.Fatal("Source maxBuffer error did not publish completed empty cache")
	}
	if _, available := prefetch.LegacyResult(); !available {
		t.Fatal("ordinary absent arm was confused with killed")
	}
}

func TestKeychainPrefetchActualBothArmsMustCompleteAndPriorReadWins(t *testing.T) {
	for _, priorRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "untouched", true: "already-read"}[priorRead], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan int, 2)
			release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
			s := prefetchStoreFixture(t, func(ctx context.Context, command Command) ([]byte, error) {
				if command.ShellScript != "" {
					return []byte(`{"source":"sync-before-publication"}`), nil
				}
				if len(command.Args) != 6 || command.Args[2] != "owned-prefetch-account" {
					return nil, ErrKeychainPrefetchState
				}
				index := 1
				if strings.Contains(command.Args[5], "-credentials") {
					index = 0
				}
				entered <- index
				select {
				case <-release[index]:
				case <-ctx.Done():
					return nil, context.Cause(ctx)
				}
				if index == 0 {
					return []byte(`{"source":"actual-prefetched-read"}`), nil
				}
				return []byte("  owned-prefetched-key\n"), nil
			})
			p, err := s.NewKeychainPrefetch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			done := [2]chan error{make(chan error, 1), make(chan error, 1)}
			go func() { done[0] <- p.RunOAuth() }()
			go func() { done[1] <- p.RunLegacy() }()
			seen := map[int]bool{}
			for len(seen) < 2 {
				select {
				case index := <-entered:
					seen[index] = true
				case <-time.After(time.Second):
					t.Fatal("actual primitive arms did not overlap")
				}
			}
			if !errors.Is(p.Publish(), ErrKeychainPrefetchState) {
				t.Fatal("published before either primitive completed")
			}
			close(release[0])
			if err := <-done[0]; err != nil {
				t.Fatal(err)
			}
			if !errors.Is(p.Publish(), ErrKeychainPrefetchState) {
				t.Fatal("published before legacy arm completed")
			}
			if priorRead {
				if _, err := s.ReadSourceJSON(ctx); err != nil {
					t.Fatal(err)
				}
			}
			close(release[1])
			if err := <-done[1]; err != nil {
				t.Fatal(err)
			}
			if err := p.Publish(); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadSourceJSON(ctx)
			want := `{"source":"actual-prefetched-read"}`
			if priorRead {
				want = `{"source":"sync-before-publication"}`
			}
			if err != nil || string(got) != want {
				t.Fatal("wrong shared-cache authority", string(got), want, err)
			}
			if key, completed := p.LegacyResult(); !completed || key != "owned-prefetched-key" {
				t.Fatal("actual legacy completion missing", key, completed)
			}
			p.ClearLegacy()
			if _, completed := p.LegacyResult(); completed {
				t.Fatal("cleared legacy result remained available")
			}
			if err := p.Publish(); err != nil {
				t.Fatal(err)
			}
			if _, completed := p.LegacyResult(); completed {
				t.Fatal("shared Promise publication repeated after clear")
			}
			if !errors.Is(p.RunLegacy(), ErrKeychainPrefetchState) {
				t.Fatal("same process prefetch started twice")
			}
		})
	}
}

func TestKeychainPrefetchOriginalJSONParsingAndEmptyCachePrime(t *testing.T) {
	for _, raw := range []string{"", "null", "false", "0", `[{"future":true}]`, "malformed"} {
		t.Run(raw, func(t *testing.T) {
			var syncReads atomic.Int32
			s := prefetchStoreFixture(t, func(_ context.Context, call Command) ([]byte, error) {
				if call.ShellScript != "" {
					syncReads.Add(1)
					return []byte(`{"syncRetry":true}`), nil
				}
				if strings.Contains(call.Args[5], "-credentials") {
					return []byte(raw), nil
				}
				return nil, errors.New("actual absent private primitive")
			})
			p, err := s.NewKeychainPrefetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if err := p.RunOAuth(); err != nil {
				t.Fatal(err)
			}
			if err := p.RunLegacy(); err != nil {
				t.Fatal(err)
			}
			if err := p.Publish(); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadSourceJSON(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := raw
			if raw == "" || raw == "null" {
				want = "{}"
			}
			if raw == "malformed" {
				want = `{"syncRetry":true}`
			}
			if string(got) != want || syncReads.Load() != map[bool]int32{true: 1, false: 0}[raw == "malformed"] {
				t.Fatal("prefetch parse/fallback changed", raw, string(got), syncReads.Load())
			}
			if key, complete := p.LegacyResult(); key != "" || !complete {
				t.Fatal("absence was confused with not-prefetched", key, complete)
			}
		})
	}
}

func TestKeychainPrefetchCancellationClosesActualHeldArmsWithoutPublishing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 2)
	s := prefetchStoreFixture(t, func(ctx context.Context, _ Command) ([]byte, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
	p, err := s.NewKeychainPrefetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan error, 2)
	go func() { done <- p.RunOAuth() }()
	go func() { done <- p.RunLegacy() }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("missing held primitive")
		}
	}
	cancel()
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled primitive escaped its caller")
		}
	}
	if !errors.Is(p.Publish(), context.Canceled) || !s.cachedAt.IsZero() {
		t.Fatal("cancellation published a late cache")
	}
	p.Close()
	if !errors.Is(p.Publish(), ErrKeychainPrefetchState) {
		t.Fatal("closed producer published")
	}
	other := prefetchStoreFixture(t, func(context.Context, Command) ([]byte, error) { return nil, nil })
	if p.Owns(other) || !p.Owns(s) {
		t.Fatal("opaque producer accepted a foreign store")
	}
	if _, err := s.NewKeychainPrefetch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled constructor admitted physical work", err)
	}
}

func TestKeychainPrefetchActualTenSecondTimeoutDoesNotPrimeThatArm(t *testing.T) {
	s := prefetchStoreFixture(t, func(ctx context.Context, call Command) ([]byte, error) {
		if call.ShellScript != "" {
			return []byte(`{"syncRetry":true}`), nil
		}
		if strings.Contains(call.Args[5], "-credentials") {
			<-ctx.Done()
			return nil, context.Cause(ctx)
		}
		return []byte("owned-legacy-after-other-arm-timeout"), nil
	})
	p, err := s.NewKeychainPrefetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan error, 1)
	go func() { done <- p.RunOAuth() }()
	if err := p.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("actual ten-second subprocess timeout did not settle")
	}
	if err := p.Publish(); err != nil {
		t.Fatal(err)
	}
	if !s.cachedAt.IsZero() {
		t.Fatal("timed out OAuth arm shadowed its sync retry")
	}
	if key, available := p.LegacyResult(); !available || key != "owned-legacy-after-other-arm-timeout" {
		t.Fatal("other actual arm was dropped", key, available)
	}
	if raw, err := s.ReadSourceJSON(context.Background()); err != nil || string(raw) != `{"syncRetry":true}` {
		t.Fatal(string(raw), err)
	}
}

func TestKeychainPrefetchRejectsForeignProcessLifetimeAndAmbientEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX held-process lifetime")
	}
	home := t.TempDir()
	request := exec.Command("/bin/sh", "-c", "exit 44")
	request.Dir = home
	if process, err := StartKeychainPrefetchProcess(context.Background(), request); process != nil || !errors.Is(err, ErrKeychainPrefetchState) {
		t.Fatal("ambient process environment admitted", process, err)
	}
	zeroStore := prefetchStoreFixture(t, func(context.Context, Command) ([]byte, error) { return nil, nil })
	zeroStore.options.StartSecurity = func(context.Context, Command) (*KeychainPrefetchProcess, error) {
		return &KeychainPrefetchProcess{}, nil
	}
	zero, err := zeroStore.NewKeychainPrefetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := zero.StartOAuth(); !errors.Is(err, ErrKeychainPrefetchState) {
		t.Fatal("zero-value process admitted", err)
	}
	zero.Close()
	store := prefetchStoreFixture(t, func(context.Context, Command) ([]byte, error) { return nil, nil })
	foreign, cancel := context.WithCancel(context.Background())
	defer cancel()
	var actual *KeychainPrefetchProcess
	store.options.StartSecurity = func(context.Context, Command) (*KeychainPrefetchProcess, error) {
		command := exec.CommandContext(foreign, "/bin/sh", "-c", "exec /usr/bin/tail -f /dev/null")
		command.Dir, command.Env = home, []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		var err error
		actual, err = StartKeychainPrefetchProcess(foreign, command)
		return actual, err
	}
	prefetch, err := store.NewKeychainPrefetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prefetch.Close()
	if err := prefetch.StartOAuth(); !errors.Is(err, ErrKeychainPrefetchState) {
		t.Fatal("foreign process lifetime admitted", err)
	}
	if actual == nil || actual.PID() <= 0 {
		t.Fatal("negative did not start actual owned process")
	}
	select {
	case <-actual.Done():
	default:
		t.Fatal("rejected physical child escaped without Wait")
	}
	if !store.cachedAt.IsZero() {
		t.Fatal("foreign lifecycle primed cache")
	}
}

func TestKeychainPrefetchActualProcessTimeoutKillsReapsAndPreservesOtherArm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX security timeout primitive")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\ncase \"$6\" in *-credentials*) exec /usr/bin/tail -f /dev/null;; *) exit 44;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "darwin", Environment: map[string]string{"HOME": home, "USER": "owned-timeout", "PATH": bin + ":/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	prefetch, err := store.NewKeychainPrefetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prefetch.Close()
	if err := prefetch.StartOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.StartLegacy(); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- prefetch.RunOAuth() }()
	if err := prefetch.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("actual ten-second child timeout failed to reap")
	}
	if !prefetch.arms[0].timedOut || prefetch.arms[1].timedOut {
		t.Fatal("actual process killed receipt differs from ordinary exit44")
	}
	select {
	case <-prefetch.arms[0].process.Done():
	default:
		t.Fatal("timeout reported before actual child Wait")
	}
	if err := prefetch.Publish(); err != nil {
		t.Fatal(err)
	}
	if !store.cachedAt.IsZero() {
		t.Fatal("actual killed process published cache")
	}
	if _, available := prefetch.LegacyResult(); !available {
		t.Fatal("other normal empty callback lost")
	}
}

func TestKeychainPrefetchCancellationClosesOwnedPipesAfterSuccessfulDirectChildExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX inherited pipe")
	}
	home := t.TempDir()
	pidFile := filepath.Join(home, "owned-descendant.pid")
	program := filepath.Join(home, "owned-inherited-pipe.sh")
	body := "#!/bin/sh\n/usr/bin/tail -f /dev/null &\nprintf '%s' \"$!\" > \"$HOME/owned-descendant.pid\"\nprintf 'owned-partial-output'\nexit 0\n"
	if err := os.WriteFile(program, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	// Only this fixture's exact recorded child is cleaned up. Production does
	// not claim subtree termination and never signals a host process group.
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Error("missing owned descendant receipt", err)
			return
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil || pid <= 0 {
			t.Error("invalid owned descendant receipt", string(data), err)
			return
		}
		if child, err := os.FindProcess(pid); err == nil {
			_ = child.Kill()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, program)
	command.Dir, command.Env = home, []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	process, err := StartKeychainPrefetchProcess(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-process.Done():
		case <-time.After(5 * time.Second):
			t.Error("owned direct process/pipe cleanup did not join")
		}
	})
	type result struct {
		data    []byte
		failure error
	}
	completed := make(chan result, 1)
	go func() { data, err := process.wait(); completed <- result{data, err} }()
	select {
	case <-process.childExited:
	case <-time.After(5 * time.Second):
		t.Fatal("actual direct child did not exit")
	}
	select {
	case result := <-completed:
		t.Fatal("fixture did not retain inherited pipe", result)
	default:
	}
	process.stdout.mu.Lock()
	beforeCancel := append([]byte(nil), process.stdout.data.Bytes()...)
	process.stdout.mu.Unlock()
	cancel()
	select {
	case result := <-completed:
		if result.failure != nil || !bytes.HasPrefix(result.data, beforeCancel) || !strings.HasPrefix("owned-partial-output", string(result.data)) {
			t.Fatal("actual code0 success/consumed stdout changed after pipe destroy", string(result.data), string(beforeCancel), result.failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("own pipe close still waited for inherited writer")
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("actual child/stream callbacks not joined")
	}
}
