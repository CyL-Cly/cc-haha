package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func privateSmokePrefetchEnvironment(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"CC_HAHA_ELECTRON_SMOKE_STATE_ROOT": root, "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG": filepath.Join(root, "window.ndjson"), "HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "home", ".claude"), "SystemRoot": os.Getenv("SystemRoot"), "PATH": filepath.Join(root, "never-search-security")}
}

func TestPrivateSmokePrefetchPublishesActualOwnedAbsentProcesses(t *testing.T) {
	environment := privateSmokePrefetchEnvironment(t)
	start, err := SmokeSecurityPrefetch(environment)
	if err != nil || start == nil {
		t.Fatal("validated smoke has no actual prefetch admission", err)
	}
	read, err := SmokeSecurity(environment)
	if err != nil || read == nil {
		t.Fatal(err)
	}
	reads := 0
	var processes []*securestorage.KeychainPrefetchProcess
	store, err := securestorage.New(securestorage.Options{Home: environment["HOME"], ConfigDir: environment["CLAUDE_CONFIG_DIR"], Platform: "darwin", Environment: map[string]string{"USER": "owned-smoke"}, RunSecurity: func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		reads++
		return read(ctx, call)
	}, StartSecurity: func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
		process, err := start(ctx, call)
		if err == nil {
			processes = append(processes, process)
		}
		return process, err
	}})
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
	if err := prefetch.StartOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.StartLegacy(); err != nil {
		t.Fatal(err)
	}
	if len(processes) != 2 || processes[0].PID() <= 0 || processes[1].PID() <= 0 || processes[0].PID() == processes[1].PID() {
		t.Fatal("prefetch did not admit two distinct actual processes", processes)
	}
	if err := prefetch.RunOAuth(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.RunLegacy(); err != nil {
		t.Fatal(err)
	}
	if err := prefetch.Publish(); err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		select {
		case <-process.Done():
		default:
			t.Fatal("absent result preceded physical Wait", process.PID())
		}
	}
	if value, available := prefetch.LegacyResult(); value != "" || !available {
		t.Fatal("empty completed arm was confused with a killed read", value, available)
	}
	if value, err := store.ReadSourceJSON(ctx); err != nil || string(value) != "{}" || reads != 0 {
		t.Fatal("actual empty prefetch did not prime the existing Store cache", string(value), reads, err)
	}
	if value, err := read(ctx, securestorage.Command{Args: []string{"delete-generic-password"}}); len(value) != 0 || !errors.Is(err, os.ErrPermission) {
		t.Fatal("sync smoke credential permissions changed", string(value), err)
	}
}

func TestPrivateSmokePrefetchRejectsUnvalidatedOrNonReadAdmission(t *testing.T) {
	if callback, err := SmokeSecurityPrefetch(map[string]string{}); callback != nil || err != nil {
		t.Fatal("ordinary production Start primitive changed", err)
	}
	environment := privateSmokePrefetchEnvironment(t)
	outside := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG"} {
		if key == "USERPROFILE" && os.PathSeparator != '\\' || key == "HOME" && os.PathSeparator == '\\' {
			continue
		}
		copy := map[string]string{}
		for name, value := range environment {
			copy[name] = value
		}
		copy[key] = filepath.Join(outside, "escape")
		if callback, err := SmokeSecurityPrefetch(copy); callback != nil || err == nil {
			t.Fatal("unvalidated smoke path admitted a subprocess", key, err)
		}
	}
	link := filepath.Join(environment["CC_HAHA_ELECTRON_SMOKE_STATE_ROOT"], "escaped-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	linked := map[string]string{}
	for name, value := range environment {
		linked[name] = value
	}
	linked["CLAUDE_CONFIG_DIR"] = filepath.Join(link, ".claude")
	if callback, err := SmokeSecurityPrefetch(linked); callback != nil || err == nil {
		t.Fatal("symlink-escaped smoke path admitted a subprocess", err)
	}
	start, err := SmokeSecurityPrefetch(environment)
	if err != nil {
		t.Fatal(err)
	}
	valid := securestorage.Command{Args: []string{"find-generic-password", "-a", "owned-smoke", "-w", "-s", "owned-service"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if process, err := start(ctx, valid); process != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled caller admitted a process", process, err)
	}
	for _, call := range []securestorage.Command{{Args: []string{"add-generic-password"}}, {Args: valid.Args, Input: "secret"}, {Args: valid.Args, ShellScript: "security find-generic-password"}, {Args: []string{"find-generic-password", "-s", "owned-service", "-w", "-a", "owned-smoke"}}} {
		if process, err := start(context.Background(), call); process != nil || err == nil {
			t.Fatal("non-prefetch operation acquired actual admission", call.Args, process, err)
		}
	}
	if process, err := start(nil, valid); process != nil || err == nil {
		t.Fatal("missing lifetime admitted a process", process, err)
	}
}

func TestPrivateWindowsSmokePrefetchValidatesRuntimeOnlyAtActualLaunch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows absolute system runtime boundary")
	}
	for _, systemRoot := range []string{"", "relative-runtime"} {
		environment := privateSmokePrefetchEnvironment(t)
		delete(environment, "SystemRoot")
		delete(environment, "SYSTEMROOT")
		if systemRoot != "" {
			environment["SystemRoot"] = systemRoot
		}
		if read, err := SmokeSecurity(environment); read == nil || err != nil {
			t.Fatal("required private Smoke paths were not valid", systemRoot, err)
		}
		start, err := SmokeSecurityPrefetch(environment)
		if start == nil || err != nil {
			t.Fatal("unused prefetch runtime rejected valid Smoke construction", systemRoot, err)
		}
		call := securestorage.Command{Args: []string{"find-generic-password", "-a", "owned-smoke", "-w", "-s", "owned-service"}}
		if process, err := start(context.Background(), call); process != nil || err == nil || !strings.Contains(err.Error(), "absolute Windows system runtime") {
			t.Fatal("missing or relative runtime admitted an actual process", systemRoot, process, err)
		}
	}
}
