package clibootstrap

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type nativeWorktreeState struct {
	current         *worktree.Session
	service         *worktree.Service
	startupMetadata json.RawMessage
}

func (m *MainPrint) workingDirectory() string {
	if m.state != nil {
		return m.state.Snapshot().CWD
	}
	return m.options.Native.CWD
}

// The native server hosts several sessions in one process. Explicit child cwd
// and the original per-session Bootstrap replace Source process.chdir, without
// changing another desktop session's process directory.
func (m *MainPrint) setWorkingDirectory(scope *cooperative.Scope, cwd string) error {
	info, e := m.options.Native.stat(cwd)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: cwd, Err: fmt.Errorf("not a directory")}
	}
	physical, e := (nativeWorktreeFiles{n: m.options.Native}).Realpath(cwd)
	if e != nil {
		return e
	}
	cwd = physical
	if e = m.state.SetCWD(scope, cwd); e != nil {
		return e
	}
	if m.nativeTools != nil {
		return m.nativeTools.SetWorkingDirectory(scope, cwd)
	}
	return nil
}

func (m *MainPrint) clearWorktreeCaches(scope *cooperative.Scope) error {
	cwd := m.workingDirectory()
	if m.prompt != nil {
		m.prompt.Clear()
	}
	if m.userContext != nil {
		if e := m.userContext.SetWorkingDirectory(cwd); e != nil {
			return e
		}
	}
	if m.plans != nil {
		return m.plans.SetWorkingDirectory(scope, cwd)
	}
	return nil
}

func (m *MainPrint) saveActiveWorktree(session *worktree.Session, cwd string) error {
	key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: m.options.Native}, filepath.EvalSymlinks, cwd)
	if key == "" {
		key = cwd
	}
	return m.Global.SaveProject(key, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		if session == nil {
			next.Delete("activeWorktreeSession")
		} else {
			value, e := json.Marshal(session)
			if e != nil {
				return nil, e
			}
			next.Set("activeWorktreeSession", value)
		}
		return &next, nil
	})
}

func (m *MainPrint) saveTranscriptWorktree(scope *cooperative.Scope, session *worktree.Session) error {
	value, e := json.Marshal(session)
	if e != nil {
		return e
	}
	_, e = m.Controller.SaveWorktreeStateOnOwner(scope, value)
	return e
}

func (m *MainPrint) nativeWorktreeService() (*worktree.Service, error) {
	if !m.options.Native.NativeProcess {
		return nil, nil
	}
	if m.worktreeState != nil && m.worktreeState.service != nil {
		return m.worktreeState.service, nil
	}
	n := m.options.Native
	var env map[string]string
	value, _ := m.environment.Snapshot().MarshalJSON()
	if e := json.Unmarshal(value, &env); e != nil {
		return nil, e
	}
	files := nativeWorktreeFiles{n: n}
	var matcherMu sync.Mutex
	matchers := map[string]*ignorepattern.Matcher{}
	service, e := worktree.New(worktree.Options{HasCreateHook: m.nativeWorktreeHasCreateHook, CreateHook: m.nativeWorktreeCreateHook, RemoveHook: m.nativeWorktreeRemoveHook, Files: files, Environment: env, GitExecutable: worktree.NativeGitExecutable(env), Run: worktree.NativeRun, Now: n.Now, TouchAgentWorktree: func(path string, now time.Time) error {
		if e := n.check("Agent worktree timestamp", path); e != nil {
			return e
		}
		return os.Chtimes(path, now, now)
	}, Log: func(level, message string) {
		if level == "error" {
			_, _ = fmt.Fprintln(m.options.Error, message)
		}
	}, IncludeMatcher: func(content, candidate string) (bool, error) {
		matcherMu.Lock()
		matcher := matchers[content]
		if matcher == nil {
			var e error
			matcher, e = ignorepattern.New(m.options.Platform)
			if e == nil {
				e = matcher.Add(content)
			}
			if e != nil {
				matcherMu.Unlock()
				return false, e
			}
			matchers[content] = matcher
		}
		matcherMu.Unlock()
		result, e := matcher.Test(candidate)
		return result.Ignored, e
	}})
	if e != nil {
		return nil, e
	}
	if m.worktreeState == nil {
		m.worktreeState = &nativeWorktreeState{}
	}
	m.worktreeState.service = service
	return service, nil
}

func (m *MainPrint) nativeWorktreeBindings() (*builtintools.WorktreeBindings, error) {
	service, e := m.nativeWorktreeService()
	if e != nil || service == nil {
		return nil, e
	}
	n := m.options.Native
	current := func(scope *cooperative.Scope) (*worktree.Session, error) {
		if !scope.BelongsTo(m.Owner) {
			return nil, cooperative.ErrRestricted
		}
		if m.worktreeState.current == nil {
			return nil, nil
		}
		copy := *m.worktreeState.current
		return &copy, nil
	}
	return &builtintools.WorktreeBindings{Service: service, Current: current, PrepareEnter: func(scope *cooperative.Scope, input protocol.JSONObject) (worktree.Request, error) {
		if !scope.BelongsTo(m.Owner) {
			return worktree.Request{}, cooperative.ErrRestricted
		}
		cwd := m.workingDirectory()
		root := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, cwd)
		if root != "" && root != cwd {
			if e := m.setWorkingDirectory(scope, root); e != nil {
				return worktree.Request{}, e
			}
			cwd = m.workingDirectory()
		}
		slug := input.String("name")
		if input.Get("name") == nil {
			var e error
			slug, e = m.plans.Slug(scope)
			if e != nil {
				return worktree.Request{}, e
			}
		}
		settings, e := m.Startup.GetInitialSettings()
		if e != nil {
			return worktree.Request{}, e
		}
		configuration := worktree.Settings{}
		if settings != nil && settings.Get("worktree") != nil {
			var object protocol.JSONObject
			if e = object.UnmarshalJSON(settings.Get("worktree")); e != nil {
				return worktree.Request{}, e
			}
			if object.Get("sparsePaths") != nil {
				if e = json.Unmarshal(object.Get("sparsePaths"), &configuration.SparsePaths); e != nil {
					return worktree.Request{}, e
				}
			}
			if object.Get("symlinkDirectories") != nil {
				if e = json.Unmarshal(object.Get("symlinkDirectories"), &configuration.SymlinkDirectories); e != nil {
					return worktree.Request{}, e
				}
			}
		}
		branch, e := m.Git.Branch(scope)
		if e != nil {
			return worktree.Request{}, e
		}
		originalBranch, e := branch.Await(scope)
		if e != nil {
			return worktree.Request{}, e
		}
		return worktree.Request{CWD: cwd, SessionID: m.Controller.SessionID(), Slug: slug, OriginalBranch: originalBranch, Settings: configuration}, nil
	}, ApplyEnter: func(scope *cooperative.Scope, session worktree.Session) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		m.worktreeState.current = &session
		if e := m.saveActiveWorktree(&session, session.OriginalCWD); e != nil {
			return e
		}
		if e := m.setWorkingDirectory(scope, session.Path); e != nil {
			return e
		}
		if e := m.state.SetOriginalCWD(scope, m.workingDirectory()); e != nil {
			return e
		}
		if e := m.saveTranscriptWorktree(scope, &session); e != nil {
			return e
		}
		return m.clearWorktreeCaches(scope)
	}, ApplyExit: func(scope *cooperative.Scope, session worktree.Session, clear bool) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		before := m.state.Snapshot()
		if clear {
			m.worktreeState.current = nil
			if e := m.saveActiveWorktree(nil, session.OriginalCWD); e != nil {
				return e
			}
		}
		if e := m.setWorkingDirectory(scope, session.OriginalCWD); e != nil {
			return e
		}
		if e := m.state.SetOriginalCWD(scope, session.OriginalCWD); e != nil {
			return e
		}
		if before.ProjectRoot == before.OriginalCWD {
			if e := m.state.SetProjectRoot(scope, session.OriginalCWD); e != nil {
				return e
			}
			if e := m.hook.UpdateSnapshot(); e != nil {
				return e
			}
		}
		if e := m.saveTranscriptWorktree(scope, nil); e != nil {
			return e
		}
		return m.clearWorktreeCaches(scope)
	}}, nil
}

type nativeWorktreeFiles struct{ n *NativeBoundary }

func (f nativeWorktreeFiles) ReadFile(p string) ([]byte, error) { return f.n.readFile(p) }
func (f nativeWorktreeFiles) WriteFile(p string, b []byte, m fs.FileMode) error {
	if e := f.n.check("write", p); e != nil {
		return e
	}
	return os.WriteFile(p, b, m)
}
func (f nativeWorktreeFiles) MkdirAll(p string, m fs.FileMode) error {
	if e := f.n.check("mkdir", p); e != nil {
		return e
	}
	return os.MkdirAll(p, m)
}
func (f nativeWorktreeFiles) Stat(p string) (fs.FileInfo, error)  { return f.n.stat(p) }
func (f nativeWorktreeFiles) Lstat(p string) (fs.FileInfo, error) { return f.n.lstat(p) }
func (f nativeWorktreeFiles) Realpath(p string) (string, error) {
	if e := f.n.check("realpath", p); e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func (f nativeWorktreeFiles) CopyFile(src, dst string) error {
	if e := f.n.check("read", src); e != nil {
		return e
	}
	if e := f.n.check("write", dst); e != nil {
		return e
	}
	return (worktree.OSFiles{}).CopyFile(src, dst)
}
func (f nativeWorktreeFiles) Symlink(src, dst string) error {
	if e := f.n.check("symlink", dst); e != nil {
		return e
	}
	return os.Symlink(src, dst)
}
