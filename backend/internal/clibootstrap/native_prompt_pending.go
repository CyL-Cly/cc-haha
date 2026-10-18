package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

type nativePromptRuntime struct {
	mu          sync.Mutex
	life        context.Context
	cancel      context.CancelFunc
	closed      bool
	lanes       *cooperative.NativePromptContinuations
	user        *usercontext.Service
	settings    *clisettings.Service
	state       *clisettings.State
	environment *clistartupenv.Environment
	startup     *clistartupenv.Service
}

func (m *MainPrint) nativePromptProducer(scope *cooperative.Scope) (*cooperative.NativePromptContinuations, context.Context, error) {
	if m == nil || m.options.Native == nil || !m.options.Native.NativeProcess || !scope.BelongsTo(m.Owner) || m.userContext == nil || !m.userContext.SharesSettings(m.settings, m.settingState, m.environment) || !m.settings.UsesNativeUserSettingsPath(m.settingState) {
		return nil, nil, cooperative.ErrRestricted
	}
	r := &m.nativePromptLife
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, cooperative.ErrClosed
	}
	if r.lanes != nil && (r.user != m.userContext || r.settings != m.settings || r.state != m.settingState || r.environment != m.environment || r.startup != m.Startup) {
		return nil, nil, cooperative.ErrRestricted
	}
	if r.lanes == nil {
		if _, err := m.nativeSDKInitialSettings(); err != nil {
			return nil, nil, err
		}
		lanes, err := cooperative.NewNativePromptContinuations(scope, m.Owner)
		if err != nil {
			return nil, nil, err
		}
		r.lanes = lanes
		r.user, r.settings, r.state, r.environment, r.startup = m.userContext, m.settings, m.settingState, m.environment, m.Startup
		r.life, r.cancel = context.WithCancel(context.Background())
	}
	if err := r.lanes.CheckCaller(scope); err != nil {
		return nil, nil, err
	}
	return r.lanes, r.life, nil
}

func (m *MainPrint) BeginNativePromptClose() {
	r := &m.nativePromptLife
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	if r.lanes != nil {
		r.lanes.BeginClose()
	}
	r.mu.Unlock()
}
func (m *MainPrint) joinNativePrompt(ctx context.Context) error {
	m.BeginNativePromptClose()
	r := &m.nativePromptLife
	r.mu.Lock()
	lanes := r.lanes
	r.mu.Unlock()
	return lanes.Join(ctx)
}

// A continuation retains its exclusive Owner token while doing business. Each
// method dispatches only its original OS call and awaits immutable native data.
type nativePromptFiles struct {
	m     *MainPrint
	scope *cooperative.Scope
	life  context.Context
}

func (f nativePromptFiles) current() error {
	if f.m == nil || f.life == nil || !f.scope.IsNativePromptContinuation(f.m.Owner) {
		return cooperative.ErrRestricted
	}
	r := &f.m.nativePromptLife
	r.mu.Lock()
	same := r.life == f.life && r.lanes.Current(f.scope)
	r.mu.Unlock()
	if !same {
		return cooperative.ErrRestricted
	}
	return context.Cause(f.life)
}
func (f nativePromptFiles) MemoryError() error { return f.current() }
func (f nativePromptFiles) WaitMemory(done <-chan struct{}) error {
	if err := f.current(); err != nil {
		return err
	}
	select {
	case <-done:
		return f.scope.AwaitPromise(done)
	default:
	}
	ready := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-f.life.Done():
		}
		close(ready)
	}()
	if err := f.scope.AwaitPromise(ready); err != nil {
		return err
	}
	return f.current()
}
func nativePromptIO[T any](f nativePromptFiles, work func() (T, error)) (T, error) {
	var zero T
	if !f.scope.IsNativePromptContinuation(f.m.Owner) {
		return zero, cooperative.ErrRestricted
	}
	if err := f.MemoryError(); err != nil {
		return zero, err
	}
	future, err := nativeSkillWork(f.m, f.scope, work)
	if err != nil {
		return zero, err
	}
	value, err := future.Await(f.scope)
	if err == nil {
		err = f.MemoryError()
	}
	return value, err
}
func (f nativePromptFiles) ReadFile(path string) ([]byte, error) {
	if err := f.current(); err != nil {
		return nil, err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if err := f.m.options.Native.check("readFile", physical); err != nil {
		return nil, err
	}
	return nativePromptIO(f, func() ([]byte, error) { return nativePromptReadFile(f.life, physical) })
}
func (f nativePromptFiles) Stat(path string) (fs.FileInfo, error) {
	if err := f.current(); err != nil {
		return nil, err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if f.m.options.Native.absentGit[physical] {
		return f.m.options.Native.stat(physical)
	}
	if err := f.m.options.Native.check("stat", physical); err != nil {
		return nil, err
	}
	return nativePromptIO(f, func() (fs.FileInfo, error) { return os.Stat(physical) })
}
func (f nativePromptFiles) Lstat(path string) (fs.FileInfo, error) {
	if err := f.current(); err != nil {
		return nil, err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if err := f.m.options.Native.check("lstat", physical); err != nil {
		return nil, err
	}
	return nativePromptIO(f, func() (fs.FileInfo, error) { return os.Lstat(physical) })
}
func (f nativePromptFiles) ReadDir(path string) ([]fs.DirEntry, error) {
	if err := f.current(); err != nil {
		return nil, err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if err := f.m.options.Native.check("readdir", physical); err != nil {
		return nil, err
	}
	return nativePromptIO(f, func() ([]fs.DirEntry, error) { return usercontext.NativeReadDir(physical) })
}
func (f nativePromptFiles) Realpath(path string) (string, error) {
	if err := f.current(); err != nil {
		return "", err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if err := f.m.options.Native.check("realpath", physical); err != nil {
		return "", err
	}
	return nativePromptIO(f, func() (string, error) { return filepath.EvalSymlinks(physical) })
}
func (f nativePromptFiles) MkdirAll(path string) error {
	if err := f.current(); err != nil {
		return err
	}
	physical := f.m.options.Native.nativePhysicalPath(path)
	if err := f.m.options.Native.check("mkdir", physical); err != nil {
		return err
	}
	_, err := nativePromptIO(f, func() (struct{}, error) { return struct{}{}, os.MkdirAll(physical, 0777) })
	return err
}
func (f nativePromptFiles) IsGit(cwd string) (bool, error) {
	root := usercontext.GitRoot(f, cwd)
	return root != "", f.MemoryError()
}
func (f nativePromptFiles) OSInfo() (systemprompt.OSInfo, error) {
	return nativePromptIO(f, nativeOSInfo)
}
func (f nativePromptFiles) MemoryDirectory() (string, error) {
	if err := f.current(); err != nil {
		return "", err
	}
	// getAutoMemPath and findCanonicalGitRoot are synchronous memoized Source
	// functions. Their path/settings sampling cannot acquire an async checkpoint.
	return f.m.memoryPaths.Directory()
}
func (f nativePromptFiles) SyncMemoryFiles() usercontext.MemoryFiles {
	return nativeMemoryFiles{n: f.m.options.Native}
}
func (f nativePromptFiles) SyncMemoryRealpath(path string) (string, error) {
	if err := f.current(); err != nil {
		return "", err
	}
	return (nativeMemoryFiles{n: f.m.options.Native}).Realpath(path)
}

func (m *MainPrint) beginNativeUserContext(scope *cooperative.Scope) (*ownerasync.OwnerFuture[*protocol.JSONObject], error) {
	lanes, life, err := m.nativePromptProducer(scope)
	if err != nil {
		return nil, err
	}
	return m.userContext.BeginUserContextOnOwner(scope, m.Owner, lanes, life, func(next *cooperative.Scope) usercontext.NativeMemoryPrimitives {
		return nativePromptFiles{m: m, scope: next, life: life}
	})
}
func (m *MainPrint) beginNativeDefaultPrompt(scope *cooperative.Scope, tools []protocol.JSONObject, model string) (*ownerasync.OwnerFuture[[]json.RawMessage], error) {
	lanes, life, err := m.nativePromptProducer(scope)
	if err != nil {
		return nil, err
	}
	if m.options.Arguments.SystemPrompt != "" || m.options.Arguments.HasCustomSystemPrompt {
		future, err := ownerasync.NewOwnerFuture[[]json.RawMessage](m.Owner)
		if err != nil {
			return nil, err
		}
		return future, future.Resolve(scope, []json.RawMessage{})
	}
	return m.prompt.BeginResolveOnOwner(scope, m.Owner, lanes, life, tools, model, m.styles, func(next *cooperative.Scope) systemprompt.NativePromptPrimitives {
		return nativePromptFiles{m: m, scope: next, life: life}
	})
}
