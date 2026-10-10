package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type nativeAgentWorktree struct {
	CWD     string
	Skipped string
	Info    *worktree.AgentWorktree
	service *worktree.Service
}

// Preparation changes only the child cwd. It does not create a session worktree,
// write activeWorktreeSession or retarget the parent's project/transcript.
func (m *MainPrint) prepareAgentWorktree(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest, agentID string) (*nativeAgentWorktree, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	run := &nativeAgentWorktree{CWD: m.workingDirectory()}
	if request.Isolation == "" {
		return run, nil
	}
	if request.Isolation != "worktree" || !m.options.Native.NativeProcess || len(agentID) < 8 {
		return nil, mainCap("Agent actual worktree isolation identity/producer")
	}
	if hooked, present, e := m.prepareAgentHookWorktree(ctx, scope, agentID); e != nil {
		return nil, e
	} else if present {
		return hooked, nil
	}
	service, e := m.nativeWorktreeService()
	if e != nil || service == nil {
		if e == nil {
			e = mainCap("Agent actual Worktree service")
		}
		return nil, e
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return nil, e
	}
	config := worktree.Settings{}
	if settings != nil && settings.Get("worktree") != nil {
		var fields protocol.JSONObject
		if e = fields.UnmarshalJSON(settings.Get("worktree")); e != nil {
			return nil, e
		}
		if fields.Get("sparsePaths") != nil {
			if e = json.Unmarshal(fields.Get("sparsePaths"), &config.SparsePaths); e != nil {
				return nil, e
			}
		}
		if fields.Get("symlinkDirectories") != nil {
			if e = json.Unmarshal(fields.Get("symlinkDirectories"), &config.SymlinkDirectories); e != nil {
				return nil, e
			}
		}
	}
	cwd := run.CWD
	future, e := nativeSkillWork(m, scope, func() (worktree.AgentAttempt, error) {
		return service.CreateAgent(context.Background(), cwd, "agent-"+agentID[:8], config)
	})
	if e != nil {
		return nil, e
	}
	attempt, e := future.Await(scope)
	if e != nil {
		return nil, e
	}
	run.service, run.Info, run.Skipped = service, attempt.Worktree, attempt.UnavailableReason
	if run.Info != nil {
		run.CWD = run.Info.Path
	}
	return run, nil
}

// Source performs cleanup in finally, independently of the agent abort signal.
// Claiming cleanup twice must not remove a subsequently recreated worktree.
func (m *MainPrint) finishAgentWorktree(scope *cooperative.Scope, run *nativeAgentWorktree) (*worktree.AgentWorktree, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if run == nil || run.Info == nil {
		return nil, nil
	}
	info := *run.Info
	run.Info = nil
	if info.HookBased || info.HeadCommit == "" {
		return &info, nil
	}
	service := run.service
	future, e := nativeSkillWork(m, scope, func() (*worktree.AgentWorktree, error) {
		changed, e := service.HasAgentChanges(context.Background(), info)
		if e != nil {
			return nil, e
		}
		if changed {
			return &info, nil
		}
		// The original clears metadata even when git returns a non-zero remove code.
		// Only a rejected process promise propagates from this finally branch.
		_, e = service.RemoveAgent(context.Background(), info)
		return nil, e
	})
	if e != nil {
		return nil, e
	}
	return future.Await(scope)
}

func (m *MainPrint) writeAgentWorktreeMetadata(path string, metadata protocol.JSONObject) {
	if mainSettingsFilesMkdir(m.options.Native, filepath.Dir(path)) != nil {
		return
	}
	data, e := metadata.MarshalJSON()
	if e == nil {
		_ = (mainResumeFiles{n: m.options.Native}).WriteFile(strings.TrimSuffix(path, ".jsonl")+".meta.json", data)
	}
}
