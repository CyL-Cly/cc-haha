package builtinwrite_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

type borrowedWriterFiles struct {
	builtinwrite.OSFiles
	entered chan struct{}
	release chan struct{}
	writes  atomic.Int32
}

func (files *borrowedWriterFiles) Mkdir(path string) error {
	close(files.entered)
	<-files.release
	return files.OSFiles.Mkdir(path)
}

func (files *borrowedWriterFiles) WriteFlush(path string, content []byte) error {
	files.writes.Add(1)
	return files.OSFiles.WriteFlush(path, content)
}

func TestWriteBorrowsActualPendingWriterBindingAndScope(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	foreign := cooperative.New()
	defer foreign.Close()
	files := &borrowedWriterFiles{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(files.release) }) }
	defer owner.Close()
	defer unblock()
	settings, err := clisettings.NewState(func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	empty := protocol.NewJSONObject()
	settings.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &empty})
	writer, err := sessioncontroller.NewWriter(sessioncontroller.WriterOptions{ConfigDir: filepath.Join(home, "config"), CWD: home, SessionID: "own-finite", Version: "test", Environment: map[string]string{}, SessionPersistenceDisabled: true, SettingsState: settings, Files: sessioncontroller.OSFiles{}, Branch: func() (string, error) { return "", nil }, OnError: func(err error) { t.Error(err) }, Schedule: sessioncontroller.RealSchedule, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sessioncontroller.NewPendingWriter(writer, owner)
	if err != nil {
		t.Fatal(err)
	}
	binding := pending.NativeCopyBinding()
	if !binding.Owns(owner) || binding.Owns(foreign) || !binding.Same(pending.NativeCopyBinding()) {
		t.Fatal("actual Writer did not retain one owner-bound native ledger")
	}
	cache, err := builtinwrite.NewFileStateCache(owner, 100, 25*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	permission := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: json.RawMessage(`"bypassPermissions"`)}, protocol.JSONField{Name: "alwaysDenyRules", Value: json.RawMessage(`{}`)})
	options := builtinwrite.Options{Owner: owner, NativeBinding: binding, Cache: cache, Home: home, CWD: home, PID: os.Getpid(), NowMillis: func() float64 { return 1000 }, Environment: func(string) string { return "" }, PermissionContext: func(*cooperative.Scope) (protocol.JSONObject, error) { return permission.Clone(), nil }, Host: builtinwrite.Host{Platform: "darwin"}, HostState: func(*cooperative.Scope) (builtinwrite.Host, error) {
		return builtinwrite.Host{NonInteractive: true}, nil
	}, Files: files}
	actual, err := builtinwrite.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Owns(owner) || actual.Owns(foreign) {
		t.Fatal("Write did not retain the pending Writer owner")
	}
	content, _ := json.Marshal("shared")
	path, _ := json.Marshal("actual-writer-binding")
	input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: path}, protocol.JSONField{Name: "content", Value: content})
	for _, call := range []func(*cooperative.Scope) error{
		func(scope *cooperative.Scope) error { return actual.Validate(context.Background(), input, scope) },
		func(scope *cooperative.Scope) error {
			_, err := actual.Call(context.Background(), input, scope)
			return err
		},
	} {
		if err := foreign.Run(context.Background(), call); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatalf("foreign Owner/scope reached native Write: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "actual-writer-binding")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected scope changed the target: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			if err := actual.Validate(ctx, input, scope); err != nil {
				return err
			}
			_, err := actual.Call(ctx, input, scope)
			return err
		})
	}()
	select {
	case <-files.entered:
	case err := <-done:
		t.Fatalf("actual Write exited before its native directory IO: %v", err)
	case <-ctx.Done():
		t.Fatal("native Write directory IO never opened", ctx.Err())
	}
	// Prove Owner delivery remains live while this IO is held. The following
	// timeout therefore measures the native ledger, not a blocked Owner queue.
	barrier, err := owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	barrierDone := make(chan error, 1)
	go func() { barrierDone <- barrier.Wait() }()
	select {
	case err := <-barrierDone:
		if err != nil {
			t.Fatal("Owner stopped accepting work while native Write was pending", err)
		}
	case <-ctx.Done():
		t.Fatal("held native directory IO blocked the Owner queue", ctx.Err())
	}
	// This public liveness boundary must observe the actual Write's held IO.
	// A second private ledger would return nil here and fail the regression.
	held, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	if err := pending.WaitRuntimeNative(held); !errors.Is(err, context.DeadlineExceeded) {
		stop()
		t.Fatalf("pending Writer failed to observe Write's borrowed native reference: %v", err)
	}
	stop()
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native Write did not finish after IO release", ctx.Err())
	}
	if err := pending.WaitRuntimeNative(ctx); err != nil {
		t.Fatal("borrowed native reference not released after Owner delivery", err)
	}
	if files.writes.Load() != 1 {
		t.Fatal("actual shared Write did not flush exactly one native replacement", files.writes.Load())
	}
	bytes, err := os.ReadFile(filepath.Join(home, "actual-writer-binding"))
	if err != nil || string(bytes) != "shared" {
		t.Fatalf("borrowed actual file %q %v", bytes, err)
	}
}
