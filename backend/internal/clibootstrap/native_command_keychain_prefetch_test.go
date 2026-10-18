package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func commandKeychainFixture(t *testing.T) (nativePluginFixture, MainCommandOptions) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.n.Environment["USER"] = "owned-prefetch-user"
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] = "1"
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	return f, MainCommandOptions{Arguments: []string{"mcp", "list"}, Native: f.n, Platform: "darwin", PID: os.Getpid(), NonInteractive: true, Output: io.Discard, Error: io.Discard}
}

func TestNativeCommandAdvisorAndPrefetchUseActualIndependentStartupGates(t *testing.T) {
	for _, row := range []struct {
		name     string
		env      map[string]string
		settings string
		bare     bool
		want     map[string]int
	}{
		{"bare", nil, `{}`, true, map[string]int{}},
		{"simple", map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, `{}`, false, map[string]int{}},
		{"forced-token", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "private-inference-only"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1, "sync-legacy": 1}},
		{"disabled-catalog", map[string]string{"ENABLE_CLAUDEAI_MCP_SERVERS": "false"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1, "sync-legacy": 1, "sync-oauth": 1}},
		{"advisor-disabled", map[string]string{"CLAUDE_CODE_DISABLE_ADVISOR_TOOL": "1"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
		{"first-party-url", map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com:443"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
		{"bedrock", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
		{"foundry", map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
		{"beta-disabled-after-auth", map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1, "sync-legacy": 1, "sync-oauth": 1}},
		{"helper-source-only", nil, `{"apiKeyHelper":"must-not-execute"}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
		{"external-token-still-samples-key", map[string]string{"ANTHROPIC_AUTH_TOKEN": "private-external"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1, "sync-legacy": 1}},
		{"noninteractive-explicit-key", map[string]string{"ANTHROPIC_API_KEY": "private-explicit"}, `{}`, false, map[string]int{"async-oauth": 1, "async-legacy": 1}},
	} {
		t.Run(row.name, func(t *testing.T) {
			f, options := commandKeychainFixture(t)
			for key, value := range row.env {
				f.n.Environment[key] = value
			}
			f.write(t, filepath.Join(f.config, "settings.json"), row.settings)
			if row.bare {
				options.Arguments = append([]string{"--bare"}, options.Arguments...)
			}
			var mu sync.Mutex
			got := map[string]int{}
			f.n.RunSecurity = func(ctx context.Context, call securestorage.Command) ([]byte, error) {
				if err := context.Cause(ctx); err != nil {
					return nil, err
				}
				kind := "legacy"
				if strings.Contains(strings.Join(call.Args, " "), "-credentials") {
					kind = "oauth"
				}
				if call.ShellScript == "" {
					kind = "async-" + kind
				} else {
					kind = "sync-" + kind
				}
				mu.Lock()
				got[kind]++
				mu.Unlock()
				return nil, nil
			}
			m, err := NewMainCommand(options)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			mu.Lock()
			same := reflect.DeepEqual(got, row.want)
			mu.Unlock()
			if !same {
				t.Fatalf("actual command startup producers %v, want %v", got, row.want)
			}
			if m.commandAdvisorConfigurable {
				t.Fatal("external cached gate invented an advisor option")
			}
			if runtime := m.nativeKeychainPrefetch; runtime != nil {
				if count, _ := runtime.refs.Snapshot(); count != 0 {
					t.Fatal("constructor escaped physical prefetch references", count)
				}
				if key, available := runtime.ticket.LegacyResult(); key != "" || !available {
					t.Fatal("empty completed result lost", key, available)
				}
			}
		})
	}
}

func TestNativeCommandPrefetchContextCancelsBothHeldArmsBeforeSafeAndJoinsConstruction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX held process cancellation")
	}
	f, options := commandKeychainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:2"}}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options.Context = ctx
	entered := make(chan *securestorage.KeychainPrefetchProcess, 2)
	program := filepath.Join(f.n.Temp, "held-prefetch.sh")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexec /usr/bin/tail -f /dev/null\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, nil }
	f.n.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		command := exec.CommandContext(ctx, program)
		command.Dir, command.Env = f.cwd, []string{"PATH=/usr/bin:/bin", "HOME=" + f.home}
		process, err := securestorage.StartKeychainPrefetchProcess(ctx, command)
		if err == nil {
			entered <- process
		}
		return process, err
	}
	type outcome struct {
		command *MainCommand
		err     error
	}
	done := make(chan outcome, 1)
	go func() { m, err := NewMainCommand(options); done <- outcome{m, err} }()
	processes := []*securestorage.KeychainPrefetchProcess{}
	for len(processes) < 2 {
		select {
		case process := <-entered:
			processes = append(processes, process)
		case <-time.After(5 * time.Second):
			t.Fatal("both actual security arms were not launched")
		}
	}
	select {
	case result := <-done:
		if result.command != nil {
			result.command.Close()
		}
		t.Fatal("constructor returned before actual prefetch completion", result.err)
	default:
	}
	if f.n.Environment["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1" {
		t.Fatal("Safe environment applied while prefetch remained held")
	}
	cancel()
	select {
	case result := <-done:
		if result.command != nil || !errors.Is(result.err, context.Canceled) {
			t.Fatal("actual startup cancellation was swallowed", result.command, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("constructor did not cancel/join actual held security operations")
	}
	for _, process := range processes {
		select {
		case <-process.Done():
		default:
			t.Fatal("constructor returned before actual process Wait", process.PID())
		}
	}

}

func TestNativeCommandAdvisorConstructionFailureCancelsAndJoinsEarlierPrefetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual POSIX held process cancellation")
	}
	f, options := commandKeychainFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options.Context = ctx
	entered := make(chan *securestorage.KeychainPrefetchProcess, 2)
	failure := mainCap("private actual policy-read failure")
	program := filepath.Join(f.n.Temp, "failed-construction-prefetch.sh")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexec /usr/bin/tail -f /dev/null\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, nil }
	f.n.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		command := exec.CommandContext(ctx, program)
		command.Dir, command.Env = f.cwd, []string{"PATH=/usr/bin:/bin", "HOME=" + f.home}
		process, err := securestorage.StartKeychainPrefetchProcess(ctx, command)
		if err == nil {
			entered <- process
		}
		return process, err
	}
	var once sync.Once
	var admissionError error
	processes := []*securestorage.KeychainPrefetchProcess{}
	f.n.ResolvePolicy = func() (*protocol.JSONObject, error) {
		once.Do(func() {
			for range 2 {
				select {
				case process := <-entered:
					processes = append(processes, process)
				case <-time.After(5 * time.Second):
					admissionError = errors.New("earlier actual subprocesses not admitted")
				}
			}
		})
		if admissionError != nil {
			return nil, admissionError
		}
		return nil, failure
	}
	type outcome struct {
		command *MainCommand
		err     error
	}
	done := make(chan outcome, 1)
	go func() { m, err := NewMainCommand(options); done <- outcome{m, err} }()
	select {
	case result := <-done:
		if result.command != nil || !errors.Is(result.err, failure) {
			if result.command != nil {
				result.command.Close()
			}
			t.Fatal("actual construction error changed", result.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("construction failure leaked earlier held security workers")
	}
	for _, process := range processes {
		select {
		case <-process.Done():
		default:
			t.Fatal("construction failure returned before actual process Wait", process.PID())
		}
	}

}

func TestNativeCommandPrefetchRetainsPriorSyncMemoAndInvalidatesOnlyActualAuthMutations(t *testing.T) {
	f, options := commandKeychainFixture(t)
	var mu sync.Mutex
	syncLegacy := "owned-sync-key"
	legacyReads := 0
	f.n.RunSecurity = func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if context.Cause(ctx) != nil {
			return nil, context.Cause(ctx)
		}
		if call.ShellScript == "" {
			return []byte("older-prefetched-key"), nil
		}
		if strings.Contains(strings.Join(call.Args, " "), "-credentials") {
			return []byte(`{}`), nil
		}
		if strings.HasPrefix(call.ShellScript, "security find-") {
			legacyReads++
			return []byte(syncLegacy), nil
		}
		return nil, nil
	}
	m, err := NewMainCommand(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		key, present, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
		if err != nil || !present || key != "owned-sync-key" {
			return fmt.Errorf("prior sync memo overwritten by prefetch: %q %v %w", key, present, err)
		}
		mu.Lock()
		syncLegacy = "owned-new-key"
		mu.Unlock()
		result, err := m.AuthLogout(context.Background(), scope, false)
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("actual logout failed: %+v %w", result, err)
		}
		if _, available := m.nativeKeychainPrefetch.ticket.LegacyResult(); available {
			return errors.New("actual remove failed to invalidate stale prefetch")
		}
		key, present, err = m.nativeCommandLegacyAPIKey(context.Background(), scope)
		if err != nil || !present || key != "owned-new-key" {
			return fmt.Errorf("post-remove primitive not sampled: %q %v %w", key, present, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	reads := legacyReads
	mu.Unlock()
	if reads != 2 {
		t.Fatal("legacy actual getter was not memoized/invalidation-bound", reads)
	}
}

func TestNativeCommandPrefetchAndAdvisorRejectForeignRetainedSubstitutedAndClosedBindings(t *testing.T) {
	_, options := commandKeychainFixture(t)
	m, err := NewMainCommand(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var retained *cooperative.Scope
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error { retained = scope; return m.nativeKeychainPrefetch.check(scope) }); err != nil {
		t.Fatal(err)
	}
	if err := m.nativeKeychainPrefetch.check(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained scope admitted publication", err)
	}
	if err := m.initializeNativeCommandAdvisor(context.Background(), nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil scope admitted advisor", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.nativeKeychainPrefetch.check(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign prefetch: %v", err)
		}
		if err := m.initializeNativeCommandAdvisor(context.Background(), scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign advisor: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store := m.Secure
	m.Secure = nil
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.nativeKeychainPrefetch.check(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign store admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Secure = store
	m.Close()
	if count, _ := m.nativeKeychainPrefetch.refs.Snapshot(); count != 0 {
		t.Fatal("normal Close lost actual work refs", count)
	}
	if err := m.nativeKeychainPrefetch.ticket.Publish(); !errors.Is(err, securestorage.ErrKeychainPrefetchState) {
		t.Fatal("closed producer published", err)
	}
}
