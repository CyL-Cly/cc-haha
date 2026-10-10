package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type WorktreeBindings struct {
	Service      *worktree.Service
	Current      func(*cooperative.Scope) (*worktree.Session, error)
	PrepareEnter func(*cooperative.Scope, protocol.JSONObject) (worktree.Request, error)
	ApplyEnter   func(*cooperative.Scope, worktree.Session) error
	ApplyExit    func(*cooperative.Scope, worktree.Session, bool) error
}

func (s *Service) HasWorktreeBindings() bool {
	b := s.options.Worktree
	return b != nil && b.Service != nil && b.Current != nil && b.PrepareEnter != nil && b.ApplyEnter != nil && b.ApplyExit != nil
}
func worktreeTool(name string) bool { return name == "EnterWorktree" || name == "ExitWorktree" }
func (s *Service) validateWorktree(ctx context.Context, name string, in protocol.JSONObject, scope *cooperative.Scope) error {
	if !s.HasWorktreeBindings() {
		return capability("actual Worktree session/git/cwd/persistence/cache producers")
	}
	if name != "ExitWorktree" {
		return nil
	}
	session, e := s.options.Worktree.Current(scope)
	if e != nil {
		return e
	}
	if session == nil {
		return &builtinwrite.ValidationError{Message: "No-op: there is no active EnterWorktree session to exit. This tool only operates on worktrees created by EnterWorktree in the current session — it will not touch worktrees created manually or in a previous session. No filesystem changes were made.", ErrorCode: 1}
	}
	if in.String("action") != "remove" || boolean(in, "discard_changes", false) {
		return nil
	}
	snapshot := *session
	type validation struct {
		Code int
		Err  error
	}
	future, e := native(s, scope, func() (validation, error) {
		code, failure := s.options.Worktree.Service.ValidateRemove(ctx, &snapshot, false)
		return validation{code, failure}, nil
	})
	if e != nil {
		return e
	}
	result, e := future.Await(scope)
	if e != nil {
		return e
	}
	if result.Err != nil {
		if result.Code != 0 {
			return &builtinwrite.ValidationError{Message: result.Err.Error(), ErrorCode: result.Code}
		}
		return result.Err
	}
	return nil
}
func (s *Service) startWorktree(ctx context.Context, name string, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasWorktreeBindings() {
		return nil, capability("actual Worktree session/git/cwd/persistence/cache producers")
	}
	bindings := s.options.Worktree
	session, e := bindings.Current(scope)
	if e != nil {
		return nil, e
	}
	if name == "EnterWorktree" {
		if session != nil {
			return nil, errors.New("Already in a worktree session")
		}
		request, e := bindings.PrepareEnter(scope, in)
		if e != nil {
			return nil, e
		}
		future, e := native(s, scope, func() (worktree.Session, error) { return bindings.Service.Create(ctx, request) })
		if e != nil {
			return nil, e
		}
		return transform(s, scope, future, func(next *cooperative.Scope, session worktree.Session) (queryloop.ToolCallResult, error) {
			if e := bindings.ApplyEnter(next, session); e != nil {
				return queryloop.ToolCallResult{}, e
			}
			branch := ""
			if session.Branch != "" {
				branch = " on branch " + session.Branch
			}
			message := "Created worktree at " + session.Path + branch + ". The session is now working in the worktree. Use ExitWorktree to leave mid-session, or exit the session to be prompted."
			data := protocol.NewJSONObject(field("worktreePath", session.Path))
			if session.Branch != "" {
				data.Set("worktreeBranch", quote(session.Branch))
			}
			data.Set("message", quote(message))
			return queryloop.ToolCallResult{Data: raw(data)}, nil
		})
	}
	if session == nil {
		return nil, errors.New("Not in a worktree session")
	}
	snapshot := *session
	keep := in.String("action") == "keep"
	type exit struct {
		Changes worktree.Changes
		Clear   bool
	}
	future, e := native(s, scope, func() (exit, error) {
		changes, e := bindings.Service.CountChanges(ctx, snapshot)
		if e != nil {
			return exit{}, e
		}
		if changes == nil {
			changes = &worktree.Changes{}
		}
		if keep {
			return exit{*changes, true}, nil
		}
		if snapshot.TmuxSessionName != "" {
			if _, e = bindings.Service.KillTmux(ctx, snapshot.TmuxSessionName); e != nil {
				return exit{}, e
			}
		}
		clear, e := bindings.Service.Remove(ctx, snapshot)
		return exit{*changes, clear}, e
	})
	if e != nil {
		return nil, e
	}
	return transform(s, scope, future, func(next *cooperative.Scope, result exit) (queryloop.ToolCallResult, error) {
		if e := bindings.ApplyExit(next, snapshot, result.Clear); e != nil {
			return queryloop.ToolCallResult{}, e
		}
		data := protocol.NewJSONObject(field("action", in.String("action")), field("originalCwd", snapshot.OriginalCWD), field("worktreePath", snapshot.Path))
		if snapshot.Branch != "" {
			data.Set("worktreeBranch", quote(snapshot.Branch))
		}
		message := ""
		if keep {
			if snapshot.TmuxSessionName != "" {
				data.Set("tmuxSessionName", quote(snapshot.TmuxSessionName))
			}
			branch := ""
			if snapshot.Branch != "" {
				branch = " on branch " + snapshot.Branch
			}
			message = "Exited worktree. Your work is preserved at " + snapshot.Path + branch + ". Session is now back in " + snapshot.OriginalCWD + "."
			if snapshot.TmuxSessionName != "" {
				message += " Tmux session " + snapshot.TmuxSessionName + " is still running; reattach with: tmux attach -t " + snapshot.TmuxSessionName
			}
		}
		if !keep {
			data.Set("discardedFiles", raw(result.Changes.Files))
			data.Set("discardedCommits", raw(result.Changes.Commits))
			parts := []string{}
			if result.Changes.Commits > 0 {
				word := "commits"
				if result.Changes.Commits == 1 {
					word = "commit"
				}
				parts = append(parts, fmt.Sprintf("%d %s", result.Changes.Commits, word))
			}
			if result.Changes.Files > 0 {
				word := "files"
				if result.Changes.Files == 1 {
					word = "file"
				}
				parts = append(parts, fmt.Sprintf("%d uncommitted %s", result.Changes.Files, word))
			}
			note := ""
			if len(parts) > 0 {
				note = " Discarded " + strings.Join(parts, " and ") + "."
			}
			message = "Exited and removed worktree at " + snapshot.Path + "." + note + " Session is now back in " + snapshot.OriginalCWD + "."
		}
		data.Set("message", quote(message))
		return queryloop.ToolCallResult{Data: raw(data)}, nil
	})
}
func mapWorktreeResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	if e := result.UnmarshalJSON(data); e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "content", Value: result.Get("message")}, protocol.JSONField{Name: "tool_use_id", Value: id}), nil
}
