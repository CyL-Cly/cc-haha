package clibootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type nativeErrorLogFiles struct{ native *NativeBoundary }

func (f nativeErrorLogFiles) MkdirAll(path string) error {
	if e := f.native.check("error-log-mkdir", path); e != nil {
		return e
	}
	return os.MkdirAll(path, 0700)
}
func (f nativeErrorLogFiles) AppendFile(path string, data []byte) error {
	if e := f.native.check("error-log-append", path); e != nil {
		return e
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = file.Write(data)
	closeErr := file.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func (f nativeErrorLogFiles) Remove(path string) error {
	if e := f.native.check("error-log-remove", path); e != nil {
		return e
	}
	// Source fs.unlink never removes a directory. os.Remove would remove
	// an empty user-owned latest directory before symlink creation.
	if info, e := os.Lstat(path); e == nil && info.IsDir() {
		return &os.PathError{Op: "unlink", Path: path, Err: syscall.EISDIR}
	}
	return os.Remove(path)
}
func (f nativeErrorLogFiles) Symlink(path, link string) error {
	if e := f.native.check("error-log-link", link); e != nil {
		return e
	}
	return os.Symlink(path, link)
}

// Error reporting borrows the same MainPrint process environment and clock.
// The Source sink never means "print the error to the ordinary terminal".
func (m *MainPrint) nativeLogError(ctx context.Context, err error) error {
	scope, _ := ctx.Value(nativeOpenAIAuthScopeKey{}).(*cooperative.Scope)
	if m == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if parent := m.nativeHelperParent(); parent != nil {
		return m.deliverNativeErrorLog(scope, parent, errorlog.Details(err))
	}

	if e := m.initializeNativeErrorLog(); e != nil {
		return e
	}
	m.errorLog.Log(err)
	return nil
}

// A diagnostic value needs no tool-context native-copy ledger. In particular,
// the original auth fallback is reachable before the child's Controller exists.
// Only the scalar error snapshot crosses owners; parent metadata/sink remain
// on its own Read callback, and child lifetime is owned by its real Future.
func (m *MainPrint) deliverNativeErrorLog(scope *cooperative.Scope, parent *MainPrint, snapshot errorlog.Error) error {
	if !scope.BelongsTo(m.Owner) || parent == nil || parent == m || m.nativeHelperParent() != parent || parent.Owner == nil || parent.Owner == m.Owner {
		return cooperative.ErrRestricted
	}
	future, e := ownerasync.NewOwnerFuture[bool](m.Owner)
	if e != nil {
		return e
	}
	delivered := false
	receipt, failure := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
		e := parent.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, next), snapshot)
		delivered = e == nil
		return e
	})
	go func() {
		if failure == nil {
			failure = receipt.Wait()
		}
		// Source logError catches a failed backend. A delivery failure is observed
		// as false, not promoted into an authentication/config-fallback error.
		delivery, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { return future.Resolve(next, failure == nil && delivered) })
		if e == nil {
			e = delivery.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
		}
	}()
	_, e = future.Await(scope)
	return e
}

func (m *MainPrint) initializeNativeErrorLog() error {
	if m.nativeHelperParent() != nil {
		return nil
	}
	if m.errorLog != nil {
		return nil
	}
	{
		service, e := errorlog.New(errorlog.Options{Environment: m.nativeOpenAIEnvironment, Now: func() time.Time { return m.options.Native.Now() }})
		if e != nil {
			return e
		}
		config := m.options.Native.Environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(m.options.Native.Home, ".claude")
		}
		sink, e := errorlog.NewNativeSink(errorlog.NativeSinkOptions{Arguments: append([]string{}, m.options.Arguments.DebugArguments...), Formatted: m.options.Arguments.DebugFormatted, Stderr: func(text string) {
			if m.options.Error != nil {
				_, _ = io.WriteString(m.options.Error, text)
			}
		}, Files: nativeErrorLogFiles{m.options.Native}, Now: func() time.Time { return m.options.Native.Now() }, Metadata: func() errorlog.Metadata {
			cwd, sid := m.options.Native.CWD, m.options.Arguments.SessionID
			if m.state != nil {
				snapshot := m.state.Snapshot()
				cwd = snapshot.CWD
				sid = snapshot.SessionID
			}
			if m.Controller != nil {
				sid = m.Controller.SessionID()
			}
			return errorlog.Metadata{Environment: m.nativeOpenAIEnvironment(), CWD: cwd, Home: m.options.Native.Home, ConfigDir: config, Platform: m.options.Platform, SessionID: sid, Version: m.options.Version}
		}})
		if e != nil {
			return e
		}
		m.errorLog, m.errorLogSink = service, sink
		service.Attach(sink.Write)
	}
	return nil
}

func (m *MainPrint) closeNativeErrorLog() {
	if m.errorLogSink != nil {
		m.errorLogSink.Close()
	}
}
