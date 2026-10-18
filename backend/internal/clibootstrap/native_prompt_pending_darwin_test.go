//go:build darwin

package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func promptPendingMain(t *testing.T) (*MainPrint, string) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-prompt-pending"
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, MaxTurns: 1, Tools: []string{""}, Sources: State{AllowedSettingSources: []string{"userSettings"}, EnabledSettingSources: []string{"userSettings"}}}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	return m, filepath.Join(f.config, "CLAUDE.md")
}

func TestNativePromptMemoSingleWaiterCancelKeepsActualFileProducerAndWarmIdentity(t *testing.T) {
	m, path := promptPendingMain(t)
	runPromptMemoFIFO(t, m, path, false)
}
func TestNativePromptGlobalCancelJoinsActualFileProducerWithoutLateMemoryCache(t *testing.T) {
	m, path := promptPendingMain(t)
	runPromptMemoFIFO(t, m, path, true)
}

func TestNativePromptFactoryBindingRejectsForeignManualReadRetainedAndClosedCallers(t *testing.T) {
	m, path := promptPendingMain(t)
	other, _ := promptPendingMain(t)
	var retained *cooperative.Scope
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		lanes, life, err := m.nativePromptProducer(scope)
		if err != nil {
			return err
		}
		original := m.userContext
		m.userContext = other.userContext
		if _, _, err := m.nativePromptProducer(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("foreign original user graph: %v", err)
		}
		m.userContext = original
		startup := m.Startup
		m.Startup = other.Startup
		if _, _, err := m.nativePromptProducer(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("foreign startup graph: %v", err)
		}
		m.Startup = startup
		if err := scope.Inline(cooperative.Read, func(callback *cooperative.Scope) error {
			if _, err := m.beginNativeUserContext(callback); !errors.Is(err, cooperative.ErrRestricted) {
				t.Errorf("generic callback began original user producer: %v", err)
			}
			if _, err := m.beginNativeDefaultPrompt(callback, nil, "fixture"); !errors.Is(err, cooperative.ErrRestricted) {
				t.Errorf("generic callback began original default producer: %v", err)
			}
			return nil
		}); err != nil {
			return err
		}
		// Correct service/context values do not let an ordinary Root stack or a
		// manually constructed life masquerade as the currently executing lane.
		files := nativePromptFiles{m: m, scope: scope, life: life}
		if _, err := files.ReadFile(path); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("manual Root file worker: %v", err)
		}
		return lanes.Start(scope, life, func(part *cooperative.Scope) error {
			files := nativePromptFiles{m: m, scope: part, life: context.Background()}
			if _, err := files.ReadFile(path); !errors.Is(err, cooperative.ErrRestricted) {
				t.Errorf("manual life file worker: %v", err)
			}
			return nil
		}, func(*cooperative.Scope, error) {})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.beginNativeUserContext(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("retained caller started user", err)
	}
	if err := other.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.beginNativeUserContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign Owner started user", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.BeginNativePromptClose()
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.beginNativeUserContext(scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed factory started user", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func runPromptMemoFIFO(t *testing.T, m *MainPrint, path string, globalClose bool) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	var writer *os.File
	var openError error
	go func() { writer, openError = os.OpenFile(path, os.O_WRONLY, 0600); close(opened) }()
	t.Cleanup(func() {
		select {
		case <-opened:
		default:
			rescue, _ := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0600)
			<-opened
			if rescue != nil {
				_ = rescue.Close()
			}
		}
		if writer != nil {
			_ = writer.Close()
		}
		m.BeginNativePromptClose()
		_ = m.joinNativePrompt(context.Background())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		first, err := m.beginNativeUserContext(scope)
		if err != nil {
			return err
		}
		second, err := m.beginNativeUserContext(scope)
		if err != nil {
			return err
		}
		if first != second {
			t.Error("pending user memo identity changed")
		}
		if err := scope.AwaitPromise(opened); err != nil {
			return err
		}
		if openError != nil {
			return openError
		}
		// Actual O_WRONLY open proves this native producer owns a read fd. The
		// writer stays connected and empty, so the read has no EOF/body result.
		select {
		case <-first.Done():
			t.Error("empty connected FIFO settled user memo")
		default:
		}
		if globalClose {
			m.BeginNativePromptClose()
			if _, err := first.Await(scope); !errors.Is(err, context.Canceled) {
				t.Errorf("global producer close: %v", err)
			}
			if string(m.userContext.CachedClaudeMd()) != "null" {
				t.Error("canceled producer committed memory")
			}
			if _, err := m.beginNativeUserContext(scope); !errors.Is(err, cooperative.ErrClosed) {
				t.Errorf("closed admission: %v", err)
			}
			return nil
		}
		waiter, stop := context.WithCancel(context.Background())
		stop()
		if _, err := first.Wait(waiter); !errors.Is(err, context.Canceled) {
			t.Errorf("single waiter cancellation: %v", err)
		}
		third, err := m.beginNativeUserContext(scope)
		if err != nil {
			return err
		}
		if third != first {
			t.Error("waiter canceled the shared memo")
		}
		if _, err := writer.WriteString("ACTUAL_PROMPT_PENDING_BODY\n"); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		value, err := first.Await(scope)
		if err != nil {
			return err
		}
		if value == nil || !strings.Contains(value.String("claudeMd"), "ACTUAL_PROMPT_PENDING_BODY") {
			t.Errorf("actual native memory result: %+v", value)
		}
		warm, err := m.beginNativeUserContext(scope)
		if err != nil {
			return err
		}
		if warm != first {
			t.Error("settled native memo changed identity")
		}
		if _, err := warm.Await(scope); err != nil {
			return err
		}
		m.BeginNativePromptClose()
		settled, err := first.Await(scope)
		if err != nil || settled != value {
			t.Errorf("close replaced the already-settled original memo: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if globalClose {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.joinNativePrompt(ctx); err != nil {
			t.Fatal("actual prompt stacks did not join", err)
		}
	}
}
