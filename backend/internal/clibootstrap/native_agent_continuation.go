package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeAgentRecovery struct {
	history  []protocol.JSONObject
	metadata protocol.JSONObject
	worktree string
}

func (m *MainPrint) nativeSendMessageEnabled(scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	env := m.options.Native.Environment
	if value, present := env["CC_HAHA_AGENT_TEAMS_ENABLED"]; present && !envTruthyNative(value) {
		return false, nil
	}
	if env["USER_TYPE"] == "ant" {
		return true, nil
	}
	optIn, present := env["CC_HAHA_AGENT_TEAMS_ENABLED"]
	if !present {
		optIn, present = env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"]
		if !present {
			optIn = env["CC_HAHA_AGENT_TEAMS_DEFAULT"]
		}
	}
	// Native external runs use the original absent-cache killswitch default.
	return envTruthyNative(optIn), nil
}
func (m *MainPrint) resumeNativeAgentForTool(_ context.Context, scope *cooperative.Scope, id, prompt string) (string, error) {
	if e := m.resumeNativeAgent(scope, id, prompt); e != nil {
		return "", e
	}
	directory, e := m.nativeTaskOutputDirectory(scope)
	return filepath.Join(directory, id+".output"), e
}

func (m *MainPrint) resumeNativeAgent(scope *cooperative.Scope, id, prompt string) error {
	if !scope.BelongsTo(m.Owner) || m.nativeTools == nil {
		return cooperative.ErrRestricted
	}
	path, loader, reducer, e := m.Controller.AgentTranscriptSourceOnOwner(scope, id)
	if e != nil {
		return e
	}
	n := m.options.Native
	load, e := nativeSkillWork(m, scope, func() (nativeAgentRecovery, error) {
		transcript := loader.LoadAgentTranscript(path, id)
		if transcript == nil {
			return nativeAgentRecovery{}, fmt.Errorf("No transcript found for agent ID: %s", id)
		}
		// This external native profile has no active replacement state. Source
		// reconstructForSubagentResume returns undefined in that profile even
		// when a historical sidechain contains replacement records.
		history, e := reducer.FilterSubagent(transcript.Messages)
		if e != nil {
			return nativeAgentRecovery{}, e
		}
		metadata := protocol.NewJSONObject()
		data, e := n.readFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
		if e == nil {
			if e = metadata.UnmarshalJSON(data); e != nil {
				return nativeAgentRecovery{}, e
			}
		} else if !os.IsNotExist(e) && !os.IsPermission(e) {
			return nativeAgentRecovery{}, e
		}
		worktree := metadata.String("worktreePath")
		if worktree != "" {
			info, e := n.stat(worktree)
			if e != nil || !info.IsDir() {
				worktree = ""
			} else {
				now := time.Now()
				if e = n.check("agent-resume-worktree", worktree); e != nil {
					return nativeAgentRecovery{}, e
				}
				if e = os.Chtimes(worktree, now, now); e != nil {
					return nativeAgentRecovery{}, e
				}
			}
		}
		return nativeAgentRecovery{history, metadata, worktree}, nil
	})
	if e != nil {
		return e
	}
	recovered, e := load.Await(scope)
	if e != nil {
		return e
	}
	request, e := m.nativeTools.PrepareAgentResume(scope, prompt, recovered.metadata)
	if e != nil {
		return e
	}
	request.ResumeAgentID = id
	request.ResumeHistory = recovered.history
	request.ResumedWorktreePath = recovered.worktree
	request.ToolUseID = recovered.metadata.Get("toolUseId")
	request.OwnerAgentID = m.options.AgentID
	existing, e := m.nativeBackgroundBindings().Lookup(scope, id)
	if e != nil {
		return e
	}
	if existing != nil {
		if request.ToolUseID == nil || string(request.ToolUseID) == "null" {
			request.ToolUseID = existing.Data.Get("toolUseId")
		}
		if request.OwnerAgentID == "" {
			request.OwnerAgentID = existing.Data.String("ownerAgentId")
		}
	}
	if request.OwnerAgentID == "" {
		request.OwnerAgentID = recovered.metadata.String("ownerAgentId")
	}
	future, e := m.runBackgroundAgent(context.Background(), scope, request)
	if e != nil {
		return e
	}
	_, e = future.Await(scope)
	return e
}

func (m *MainPrint) sendAgentMessageOnOwner(scope *cooperative.Scope, id, content string) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.closed {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	id = protocol.TrimJavaScriptString(id)
	content = protocol.TrimJavaScriptString(content)
	if id == "" || content == "" {
		return protocol.JSONObject{}, errors.New("Agent id and message content are required")
	}
	task, e := m.nativeBackgroundBindings().Lookup(scope, id)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	delivery := "resumed"
	if task != nil && task.Data.String("type") == "local_agent" && task.Data.String("agentType") != "main-session" && task.Data.String("status") == "running" {
		e = builtintools.QueueAgentPendingMessage(m.nativeBackgroundBindings(), scope, id, content)
		delivery = "queued"
	} else {
		e = m.resumeNativeAgent(scope, id, content)
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "agent_id", Value: quoteMain(id)}, protocol.JSONField{Name: "delivery", Value: quoteMain(delivery)}), nil
}

// SendAgentMessage is the shared real desktop/CLI control boundary. It never
// submits these prompts through the unrelated root session-message inbox.
func (m *MainPrint) SendAgentMessage(ctx context.Context, id, content string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		var e error
		result, e = m.sendAgentMessageOnOwner(scope, id, content)
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return result, e
}

// ViewAgentTranscript is the original coordinator retain + one-shot disk
// bootstrap. Live stream suffixes stay in the single parent task entry.
func (m *MainPrint) ViewAgentTranscript(ctx context.Context, id string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if e := m.Controller.EnterAgentViewOnOwner(scope, id, m.options.Native.NowMillis()); e != nil {
			return e
		}
		path, loader, _, e := m.Controller.AgentTranscriptSourceOnOwner(scope, id)
		if e != nil {
			return e
		}
		future, e := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
			rows := []protocol.JSONObject{}
			if transcript := loader.LoadAgentTranscript(path, id); transcript != nil {
				for _, raw := range transcript.Messages {
					var row protocol.JSONObject
					if e := row.UnmarshalJSON(raw); e != nil {
						return nil, e
					}
					rows = append(rows, row)
				}
			}
			return rows, nil
		})
		if e != nil {
			return e
		}
		rows, e := future.Await(scope)
		if e != nil {
			return e
		}
		if e = m.Controller.BootstrapRetainedAgentTranscriptOnOwner(scope, id, rows); e != nil {
			return e
		}
		task, e := m.nativeBackgroundBindings().Lookup(scope, id)
		if task != nil {
			result = task.Data
		}
		return e
	})
	if e == nil {
		e = receipt.Wait()
	}
	return result, e
}
func (m *MainPrint) ExitAgentTranscript(ctx context.Context) error {
	receipt, e := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		return m.Controller.ExitAgentViewOnOwner(scope, m.options.Native.NowMillis())
	})
	if e == nil {
		e = receipt.Wait()
	}
	return e
}
