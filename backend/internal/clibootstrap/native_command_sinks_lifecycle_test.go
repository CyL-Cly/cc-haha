package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

func TestNativeCommandSinkFailureKeepsAttachedHandleForRetryAndClose(t *testing.T) {
	f := newNativePluginFixture(t)
	debugPath := filepath.Join(f.config, "debug-append-destination")
	if err := os.MkdirAll(debugPath, 0700); err != nil {
		t.Fatal(err)
	}
	f.n.Environment["DEBUG"] = "1"
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"] = debugPath
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.BeginNativeCommandSinks(context.Background()); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("sinks acquired before actual preAction init", err)
	}
	if err := m.BeginNativeAPIPreconnect(context.Background()); !errors.Is(err, syscall.EISDIR) || m.commandErrorSink == nil || m.commandAnalytics != nil {
		t.Fatal("actual debug append failure lost attached resource or initialized later sink", err, m.commandErrorSink, m.commandAnalytics)
	}
	retained := m.commandErrorSink
	if err := os.Remove(debugPath); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginNativeAPIPreconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.commandErrorSink != retained {
		t.Fatal("next actual init replaced first attached error sink")
	}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		result, err := m.PluginCommand(context.Background(), scope, plugincli.Command{Action: "marketplace-list", JSON: true})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 || result.Stderr != "" || !strings.Contains(result.Stdout, `"name": "mkt"`) {
			return fmt.Errorf("handler after real initialization retry changed: %+v", result)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	before, err := os.ReadFile(debugPath)
	if err != nil || strings.Count(string(before), "Error log sink initialized") != 1 {
		t.Fatal("normal close lost or duplicated successful retry debug", err, string(before))
	}
	if err := retained.Debug("late-after-close", "DEBUG"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(debugPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("retained sink wrote after normal Close", err)
	}
}

func TestNativeCommandSinkNamespaceRejectsForeignExpiredCanceledAndClosedConsumers(t *testing.T) {
	f := newNativePluginFixture(t)
	f.n.Environment["NODE_ENV"] = "production"
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.BeginNativeAPIPreconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	var expired *cooperative.Scope
	namespace := &nativeCommandSinksNamespace{command: m, binding: m.nativeCommandInit}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		return namespace.check(scope)
	}); err != nil {
		t.Fatal(err)
	}
	if err := namespace.initialize(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil namespace consumer", err)
	}
	if err := namespace.initialize(expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained expired namespace consumer", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := namespace.initialize(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign namespace consumer: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.BeginNativeCommandSinks(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preAction acquired sinks", err)
	}
	m.Close()
	if err := m.BeginNativeCommandSinks(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed command acquired sinks", err)
	}
}

func TestNativeCommandSinksRepeatedInitKeepsServicesAndActualDebugSideEffect(t *testing.T) {
	f := newNativePluginFixture(t)
	debugPath := filepath.Join(f.config, "repeat-debug.txt")
	f.n.Environment["DEBUG"] = "1"
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"] = debugPath
	m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.BeginNativeAPIPreconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	errors, logger, sink := m.commandErrorSink, m.commandAnalytics, m.commandAnalyticsSink
	if err := m.BeginNativeAPIPreconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if errors != m.commandErrorSink || logger != m.commandAnalytics || sink != m.commandAnalyticsSink {
		t.Fatal("repeated Source init replaced first process sinks")
	}
	m.Close()
	raw, err := os.ReadFile(debugPath)
	if err != nil || strings.Count(string(raw), "Error log sink initialized") != 2 {
		t.Fatal("idempotent service attachment erased Source initialization side effect", err, string(raw))
	}
}
