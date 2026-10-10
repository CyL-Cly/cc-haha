package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"path/filepath"
)

func (m *NativeMCPToolContext) ordinaryWorktreeBindings() (*builtintools.WorktreeBindings, error) {
	n := m.command.Native
	hasCreate, e := m.hooks.OutsideEventConfigured("WorktreeCreate", m.state.Snapshot().SessionID)
	if e != nil {
		return nil, e
	}
	var create func(context.Context, string) (string, error)
	if hasCreate {
		create = m.ordinaryCreateHook
	}
	service, err := worktree.New(worktree.Options{CreateHook: create, RemoveHook: m.ordinaryRemoveHook, Files: nativeWorktreeFiles{n}, Environment: n.Environment, GitExecutable: worktree.NativeGitExecutable(n.Environment), Run: worktree.NativeRun, Now: n.Now, IncludeMatcher: func(content, candidate string) (bool, error) {
		matcher, e := ignorepattern.New(m.platform)
		if e != nil {
			return false, e
		}
		if e = matcher.Add(content); e != nil {
			return false, e
		}
		result, e := matcher.Test(candidate)
		return result.Ignored, e
	}, Log: func(level, message string) {
		if level == "error" {
			_, _ = m.command.Error.Write([]byte(message + "\n"))
		}
	}})
	if err != nil {
		return nil, err
	}
	m.worktreeService = service
	var current *worktree.Session
	setCWD := func(scope *cooperative.Scope, cwd string) error {
		if err := m.checkOrdinary(scope); err != nil {
			return err
		}
		if err := m.setOrdinaryCWD(scope, cwd); err != nil {
			return err
		}
		cwd = m.state.Snapshot().CWD
		if err := m.state.SetOriginalCWD(scope, cwd); err != nil {
			return err
		}
		if err := m.plans.SetWorkingDirectory(scope, cwd); err != nil {
			return err
		}
		return m.tools.SetWorkingDirectory(scope, cwd)
	}
	save := func(scope *cooperative.Scope, session *worktree.Session) error {
		if err := m.checkOrdinary(scope); err != nil {
			return err
		}
		cwd := m.state.Snapshot().CWD
		if session != nil {
			cwd = session.OriginalCWD
		}
		key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, cwd)
		if key == "" {
			key = cwd
		}
		return m.command.Global.SaveProject(key, func(old *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := old.Clone()
			if session == nil {
				next.Delete("activeWorktreeSession")
			} else {
				data, err := json.Marshal(session)
				if err != nil {
					return nil, err
				}
				next.Set("activeWorktreeSession", data)
			}
			return &next, nil
		})
	}
	return &builtintools.WorktreeBindings{Service: service, Current: func(scope *cooperative.Scope) (*worktree.Session, error) {
		if err := m.checkOrdinary(scope); err != nil {
			return nil, err
		}
		if current == nil {
			return nil, nil
		}
		copy := *current
		return &copy, nil
	}, PrepareEnter: func(scope *cooperative.Scope, in protocol.JSONObject) (worktree.Request, error) {
		if err := m.checkOrdinary(scope); err != nil {
			return worktree.Request{}, err
		}
		cwd := m.state.Snapshot().CWD
		if root := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, cwd); root != "" && root != cwd {
			if err := setCWD(scope, root); err != nil {
				return worktree.Request{}, err
			}
			cwd = root
		}
		slug := in.String("name")
		if in.Get("name") == nil {
			var err error
			slug, err = m.plans.Slug(scope)
			if err != nil {
				return worktree.Request{}, err
			}
		}
		settings, err := m.command.Startup.GetInitialSettings()
		if err != nil {
			return worktree.Request{}, err
		}
		cfg := worktree.Settings{}
		if settings != nil {
			var value struct{ SparsePaths, SymlinkDirectories []string }
			_ = json.Unmarshal(settings.Get("worktree"), &value)
			cfg.SparsePaths = value.SparsePaths
			cfg.SymlinkDirectories = value.SymlinkDirectories
		}
		return worktree.Request{CWD: cwd, SessionID: m.state.Snapshot().SessionID, Slug: slug, Settings: cfg}, nil
	}, ApplyEnter: func(scope *cooperative.Scope, session worktree.Session) error {
		if err := setCWD(scope, session.Path); err != nil {
			return err
		}
		current = &session
		return save(scope, current)
	}, ApplyExit: func(scope *cooperative.Scope, session worktree.Session, clear bool) error {
		if err := setCWD(scope, session.OriginalCWD); err != nil {
			return err
		}
		if clear {
			current = nil
			return save(scope, nil)
		}
		return nil
	}}, nil
}

func (m *NativeMCPToolContext) setOrdinaryCWD(scope *cooperative.Scope, cwd string) error {
	if e := m.checkOrdinary(scope); e != nil {
		return e
	}
	n := m.command.Native
	info, e := n.stat(cwd)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: cwd, Err: fmt.Errorf("not a directory")}
	}
	physical, e := (nativeWorktreeFiles{n}).Realpath(cwd)
	if e != nil {
		return e
	}
	if e = m.state.SetCWD(scope, physical); e != nil {
		return e
	}
	return m.tools.SetWorkingDirectory(scope, physical)
}
