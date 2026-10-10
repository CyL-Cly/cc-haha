package clibootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"sync"
)

// NativeTeamResourceCleanup shares the physical Worktree service used by CLI
// Team cleanup. The API caller supplies its owned native boundary/environment.
func NativeTeamResourceCleanup(n *NativeBoundary, log func(string, string)) (func(context.Context, string, []protocol.JSONObject, bool) error, error) {
	if n == nil || !n.NativeProcess || n.Now == nil || log == nil {
		return nil, errors.New("Team resource cleanup requires actual native process/files/clock/log producers")
	}
	env := map[string]string{}
	for k, v := range n.Environment {
		env[k] = v
	}
	service, err := worktree.New(worktree.Options{Files: nativeWorktreeFiles{n: n}, Environment: env, GitExecutable: worktree.NativeGitExecutable(env), Run: worktree.NativeRun, Now: n.Now, Log: log})
	if err != nil {
		return nil, err
	}
	panes, err := teamspawn.NewPaneRuntime(n.CWD, os.Getpid(), env)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, _ string, members []protocol.JSONObject, kill bool) error {
		if kill {
			var jobs sync.WaitGroup
			for _, member := range members {
				if member.String("name") == "team-lead" || member.String("tmuxPaneId") == "" || (member.String("backendType") != "tmux" && member.String("backendType") != "iterm2") {
					continue
				}
				member := member.Clone()
				jobs.Add(1)
				go func() { defer jobs.Done(); panes.KillPane(member.String("backendType"), member.String("tmuxPaneId")) }()
			}
			jobs.Wait()
		}
		for _, member := range members {
			path := member.String("worktreePath")
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(n.CWD, path)
			}
			if err := service.DestroyTeamWorktree(ctx, path, func(path string) error {
				if err := n.check("Team worktree cleanup", path); err != nil {
					return err
				}
				return os.RemoveAll(path)
			}); err != nil {
				return err
			}
		}
		return nil
	}, nil
}
