package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func (m *MainPrint) sendNativeStructuredTeamMessage(ctx context.Context, scope *cooperative.Scope, in protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	message, ok := in.Object("message")
	if !ok {
		return nil, cooperative.ErrRestricted
	}
	identity, err := m.Controller.TeammateIdentityOnOwner(scope)
	if err != nil {
		return nil, err
	}
	permissions, err := m.Host.NativePermissionContext(scope)
	if err != nil {
		return nil, err
	}
	actor := teamspawn.ProtocolIdentity{TeamName: m.nativeLeaderTeamName(), AgentName: identity.AgentName, AgentID: identity.AgentID, Color: identity.Color, Leader: m.teamState != nil && m.teamState.snapshot.String("leadAgentId") != "" && identity.AgentID == "", LeaderMode: permissions.String("mode"), Timestamp: m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Now: m.options.Native.NowMillis()}
	var taskID string
	if identity.AgentID != "" {
		tasks, err := m.nativeBackgroundBindings().List(scope)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			role, _ := task.Object("identity")
			if task.String("type") == "in_process_teammate" && role.String("agentId") == identity.AgentID {
				if taskID == "" || task.String("status") == "running" {
					taskID = task.String("id")
				}
				if task.String("status") == "running" {
					break
				}
			}
		}
		if taskID != "" {
			task, err := m.nativeBackgroundBindings().Lookup(scope, taskID)
			if err != nil {
				return nil, err
			}
			actor.InProcessTaskFound = task != nil && task.Cancel != nil && task.Data.String("status") == "running"
		}
	}
	if message.String("type") == "shutdown_response" && string(message.Get("approve")) == "true" && m.teamWorker != nil && m.options.RequestTeammateShutdown == nil {
		return nil, mainCap("standalone teammate actual graceful shutdown callback")
	}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform})
	if err != nil {
		return nil, err
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
	if err != nil {
		return nil, err
	}
	in = in.Clone()
	type written struct {
		result   queryloop.ToolCallResult
		shutdown bool
	}
	work, err := nativeSkillWork(m, scope, func() (written, error) {
		if message.String("type") == "shutdown_response" && string(message.Get("approve")) == "true" && actor.TeamName != "" && actor.AgentID != "" {
			file := config.Read(actor.TeamName)
			if file != nil {
				var members []protocol.JSONObject
				_ = json.Unmarshal(file.Get("members"), &members)
				for _, member := range members {
					if member.String("agentId") == actor.AgentID {
						actor.PaneID = member.Get("tmuxPaneId")
						actor.BackendType = member.Get("backendType")
						break
					}
				}
			}
		}
		out, err := teamspawn.StructuredMessage(in.String("to"), message, actor)
		if err != nil {
			return written{}, err
		}
		if out.GracefulExit && m.teamWorker == nil {
			return written{}, mainCap("ordinary process teammate actual graceful shutdown lifecycle")
		}
		if err = mailbox.Write(out.Recipient, actor.TeamName, out.Envelope); err != nil {
			result := out.Output.Clone()
			result.Set("success", rawMain(false))
			text := fmt.Sprintf("Failed to write to %s's inbox — nothing was sent. Try again.", out.Recipient)
			if message.String("type") == "shutdown_response" && string(message.Get("approve")) == "true" {
				text = fmt.Sprintf("Failed to write the shutdown approval to team-lead's inbox — nothing was sent and %s is still running. Try again.", actor.AgentName)
			} else if message.String("type") == "shutdown_request" {
				text = fmt.Sprintf("Failed to write the shutdown request to %s's inbox — nothing was sent. Try again.", out.Recipient)
			}
			result.Set("message", quoteMain(text))
			return written{result: queryloop.ToolCallResult{Data: rawMain(result)}, shutdown: false}, nil
		}
		return written{queryloop.ToolCallResult{Data: rawMain(out.Output)}, out.AbortSelf || out.GracefulExit}, nil
	})
	if err != nil {
		return nil, err
	}
	out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
	if err != nil {
		return nil, err
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, value written, failure error) error {
		if failure != nil {
			return out.Reject(next, failure)
		}
		if value.shutdown {
			if m.teamWorker != nil {
				m.teamWorker.shutdownPending = true
			} else if d := m.options.DetachedAgent; d != nil && d.teammate != nil {
				d.teammateShutdownPending = true
			} else {
				return out.Reject(next, cooperative.ErrRestricted)
			}
		}
		return out.Resolve(next, value.result)
	})
	return out, err
}

func (m *MainPrint) nativeTeammateShutdownPending() bool {
	if m.teamWorker != nil {
		return m.teamWorker.shutdownPending
	}
	return m.options.DetachedAgent != nil && m.options.DetachedAgent.teammateShutdownPending
}

func (m *MainPrint) nativeTeammateExitBinding(scope *cooperative.Scope) (*sdkhost.NativeTeammateExit, error) {
	if m.teamWorker == nil && m.nativeInProcessTeammateIdentity() == nil {
		return nil, nil
	}
	return sdkhost.NewNativeTeammateExit(scope, m.Owner, m.Controller, m.options.AgentID, func(next *cooperative.Scope) (bool, error) {
		if !m.nativeTeammateShutdownPending() {
			return false, nil
		}
		if m.teamWorker != nil {
			if m.options.RequestTeammateShutdown == nil {
				return false, mainCap("standalone teammate actual graceful shutdown callback")
			}
			if err := m.options.RequestTeammateShutdown(context.Background()); err != nil {
				return false, err
			}
			return true, nil
		}
		identity, err := m.Controller.TeammateIdentityOnOwner(next)
		if err != nil {
			return false, err
		}
		tasks, err := m.nativeBackgroundBindings().List(next)
		if err != nil {
			return false, err
		}
		for _, task := range tasks {
			role, _ := task.Object("identity")
			if task.String("type") == "in_process_teammate" && role.String("agentId") == identity.AgentID && task.String("status") == "running" {
				entry, err := m.nativeBackgroundBindings().Lookup(next, task.String("id"))
				if err != nil {
					return false, err
				}
				if entry != nil && entry.Cancel != nil {
					entry.Cancel(context.Canceled)
					return true, nil
				}
			}
		}
		return false, cooperative.ErrRestricted
	})
}
