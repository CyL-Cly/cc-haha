package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// setupNativeWorktree runs before command, agent, memory and MCP session
// discovery. It reuses the one physical Worktree service used by both tools.
func (m *MainPrint) setupNativeWorktree(ctx context.Context) error {
	options := m.options.Arguments.Worktree
	if options == nil {
		return nil
	}
	if !m.options.Native.NativeProcess || m.nativeSettings == nil {
		return mainCap("native CLI worktree startup bindings")
	}
	if options.Tmux {
		return mainCap("CLI worktree tmux startup producer")
	}
	if e := m.newNativePlans(); e != nil {
		return e
	}
	service, e := m.nativeWorktreeService()
	if e != nil {
		return e
	}
	cwd := m.workingDirectory()
	gitRoot := service.GitRoot(cwd)
	if e = m.hook.EmptyEvent("WorktreeCreate", m.state.SessionID()); e != nil {
		return e
	}
	if gitRoot == "" {
		return errors.New("Error: Can only use --worktree in a git repository, but " + cwd + " is not a git repository. Configure a WorktreeCreate hook in settings.json to use --worktree with other VCS systems.")
	}
	root := usercontext.CanonicalGitRoot(mainSettingsFiles{n: m.options.Native}, filepath.EvalSymlinks, cwd)
	if root == "" {
		return errors.New("Error: Could not determine the main git repository root.")
	}
	// setup preserves a main-repository subdirectory as originalCwd. It only
	// lifts a linked worktree to the main root; mid-session Enter lifts both.
	if root != gitRoot {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.setWorkingDirectory(scope, root) }); e != nil {
			return e
		}
		cwd = m.workingDirectory()
	}
	var slug string
	if options.Name != nil {
		slug = *options.Name
	}
	pr, e := worktree.ParsePRReference(slug)
	if e != nil {
		return e
	}
	if pr != nil && *pr != 0 {
		slug = "pr-" + worktree.PRNumberString(*pr)
	} else if options.Name == nil || pr != nil {
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { var e error; slug, e = m.plans.Slug(scope); return e }); e != nil {
			return e
		}
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return e
	}
	configuration := worktree.Settings{}
	if settings != nil && settings.Get("worktree") != nil {
		var parsed struct {
			SparsePaths        []string `json:"sparsePaths"`
			SymlinkDirectories []string `json:"symlinkDirectories"`
		}
		if e = json.Unmarshal(settings.Get("worktree"), &parsed); e != nil {
			return e
		}
		configuration.SparsePaths = parsed.SparsePaths
		configuration.SymlinkDirectories = parsed.SymlinkDirectories
	}
	// This is pre-activation construction: no ASK, terminal or background Agent
	// has been started. All native child processes use an explicit cwd/env.
	session, e := service.Create(ctx, worktree.Request{CWD: cwd, SessionID: m.state.SessionID(), Slug: slug, PRNumber: pr, BaseRef: options.BaseRef, Settings: configuration})
	if e != nil {
		return fmt.Errorf("Error creating worktree: %w", e)
	}
	m.worktreeState.current = &session
	if e = m.saveActiveWorktree(&session, cwd); e != nil {
		return e
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := m.setWorkingDirectory(scope, session.Path); e != nil {
			return e
		}
		if e := m.state.SetOriginalCWD(scope, m.workingDirectory()); e != nil {
			return e
		}
		return m.state.SetProjectRoot(scope, m.workingDirectory())
	}); e != nil {
		return e
	}
	m.worktreeState.startupMetadata, e = json.Marshal(session)
	if e != nil {
		return e
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return m.plans.SetWorkingDirectory(scope, m.workingDirectory()) }); e != nil {
		return e
	}
	return m.rebindWorktreeStartupSettings(ctx, m.workingDirectory())
}

func (m *MainPrint) rebindWorktreeStartupSettings(ctx context.Context, cwd string) error {
	old := m.nativeSettings
	boundary := m.options.Native.agentScopedBoundary(cwd)
	next, e := old.ScopedSettings(boundary)
	if e != nil {
		return e
	}
	next.environment = m.environment
	if e = next.ensureSettings(); e != nil {
		_ = next.Close(ctx)
		return e
	}
	if _, e = next.LoadPlugins(ctx, "", false); e != nil {
		_ = next.Close(ctx)
		return e
	}
	// Transfer the existing global writer's lifetime, rather than creating a
	// second config writer or leaving the scoped child as its only borrower.
	old.borrowedGlobal = true
	next.borrowedGlobal = false
	m.nativeSettings = next
	m.options.Native = boundary
	m.Startup = next.startup
	m.settings = next.settings
	m.settingState = next.settingsState
	if e = old.Close(ctx); e != nil {
		return e
	}
	m.pluginOptions, e = next.PluginOptions(m.secure)
	return e
}
