package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

type nativeTeamWorkerState struct {
	shutdownPending bool
	pane            *teamspawn.PaneIdentity
	permissions     map[string]*ownerasync.OwnerFuture[protocol.JSONObject]
	preset          *teamworker.Preset
	context         *sessioncontroller.TeamContext
	mailbox         *teamstore.Mailbox
	config          *teamstore.Config
}

func (m *MainPrint) configureNativeTeamWorker(initial *protocol.JSONObject) error {
	if m.options.PaneTeammate != nil {
		return m.configureNativePaneTeammate(initial)
	}
	start := m.options.TeamWorker
	if start == nil {
		return nil
	}
	resume := m.options.Arguments.Resume
	if !m.options.Native.NativeProcess || m.options.DetachedAgent != nil || m.options.AgentID != "" || resume != "" && resume != start.ResumeSessionID || start.ParentSessionID == "" || start.MemberID == "" {
		return mainCap("approved standalone Team worker startup identity")
	}
	preset, err := teamworker.Build(*start, m.options.Arguments.Model, m.options.Native.Environment["USER_TYPE"], initial != nil && systemprompt.AutoMemoryEnabled(m.environment, *initial))
	if err != nil {
		return err
	}
	context, err := sessioncontroller.NewTeamContext(m.Owner, start.TeamName, start.Name, preset.AgentID)
	if err != nil {
		return err
	}
	m.teamWorker = &nativeTeamWorkerState{preset: preset, context: context}
	m.options.AgentID = preset.AgentID
	m.options.Arguments.SystemPrompt = preset.Definition.SystemPrompt
	if preset.CLIBaseTools != nil {
		m.options.Arguments.Tools = append([]string{}, (*preset.CLIBaseTools)...)
	}
	if preset.MaxTurns != nil {
		var turns int
		if json.Unmarshal(preset.MaxTurns, &turns) != nil || turns < 1 {
			return errors.New("Approved team worker preset has invalid maxTurns")
		}
		m.options.Arguments.MaxTurns = turns
	}
	return nil
}
func (m *MainPrint) nativeTeamContext() *sessioncontroller.TeamContext {
	if m.teamWorker != nil {
		return m.teamWorker.context
	}
	return nil
}
func (m *MainPrint) nativeTeamOmitClaudeMd() bool {
	return m.teamWorker != nil && m.teamWorker.preset.OmitClaudeMd
}

func (m *MainPrint) nativeTeamWorkerBinding(scope *cooperative.Scope) (*sdkhost.NativeTeamWorker, error) {
	if m.teamWorker == nil {
		return nil, nil
	}
	return sdkhost.NewNativeTeamWorker(scope, m.Owner, m.Controller, m.prepareNativeTeamWorkerPreset)
}
func (m *MainPrint) prepareNativeTeamWorkerPreset(ctx context.Context, scope *cooperative.Scope, names []string) (sdkhost.NativeTeamPreset, error) {
	if m.teamWorker == nil || !scope.BelongsTo(m.Owner) {
		return sdkhost.NativeTeamPreset{}, cooperative.ErrRestricted
	}
	if m.teamWorker.pane != nil {
		return sdkhost.NativeTeamPreset{Tools: append([]string{}, names...), Messages: []protocol.JSONObject{}}, nil
	}
	definition := m.teamWorker.preset.Definition
	definition.Source = m.teamWorker.preset.Source
	definition.Fields.Set("source", quoteMain(definition.Source))
	definition.Fields.Set("agentType", quoteMain(m.teamWorker.preset.AgentType))
	var refs []json.RawMessage
	_ = json.Unmarshal(definition.Fields.Get("mcpServers"), &refs)
	for _, ref := range refs {
		if _, err := protocol.DecodeJavaScriptJSONString(ref); err != nil {
			return sdkhost.NativeTeamPreset{}, errors.New("Team worker MCP requires named configured servers")
		}
	}
	if len(refs) > 0 {
		if m.mcpTools == nil {
			return sdkhost.NativeTeamPreset{}, mainCap("Team worker actual named MCP producer")
		}
		preset, err := m.nativeAgentPreset(scope, definition)
		if err != nil {
			return sdkhost.NativeTeamPreset{}, err
		}
		additional, _, err := preset.connectMCP(ctx, m, scope)
		if err != nil {
			return sdkhost.NativeTeamPreset{}, err
		}
		peers := append(append(preset.inherited[:0:0], preset.inherited...), additional...)
		for _, ref := range refs {
			var name string
			_ = json.Unmarshal(ref, &name)
			connected := false
			for _, peer := range peers {
				connected = connected || peer.Name == name && peer.Status == "connected"
			}
			if !connected {
				return sdkhost.NativeTeamPreset{}, fmt.Errorf("Team worker MCP server unavailable: %s", name)
			}
		}
		if err := m.mcpTools.InstallTeamConnections(scope, preset.inherited, additional); err != nil {
			return sdkhost.NativeTeamPreset{}, err
		}
		for _, peer := range additional {
			if peer.Status == "connected" {
				for _, tool := range peer.Tools {
					names = append(names, mcpclient.ToolName(peer.Name, tool.String("name")))
				}
			}
		}
	}
	// This is the main-thread variant. Question/Agent/Workflow are never
	// removed by ordinary subagent disallow lists.
	resolved := agentcatalog.ResolveTools(definition, names, false, true)
	output := sdkhost.NativeTeamPreset{Tools: resolved.ResolvedTools, Messages: []protocol.JSONObject{}}
	var skills []string
	_ = json.Unmarshal(definition.Fields.Get("skills"), &skills)
	if len(skills) > 0 {
		commands, err := m.nativeSkillCommands(scope)
		if err != nil {
			return output, err
		}
		// activateTeamWorkerPreset resolves through getSkillToolCommands,
		// including its model-invocation/source/listing filters.
		available := commands[:0]
		for _, command := range commands {
			if skillcommands.Listed(command.Metadata) {
				available = append(available, command)
			}
		}
		commands = available
		inv, err := m.nativeSkillInvocation(scope)
		if err != nil {
			return output, err
		}
		for _, name := range skills {
			index := -1
			for _, candidate := range []string{name, strings.Split(m.teamWorker.preset.AgentType, ":")[0] + ":" + name} {
				for i, command := range commands {
					matches := command.Metadata.String("name") == candidate || command.Metadata.String("userFacingName") == candidate
					var aliases []string
					_ = json.Unmarshal(command.Metadata.Get("aliases"), &aliases)
					for _, alias := range aliases {
						matches = matches || alias == candidate
					}
					if matches {
						index = i
						break
					}
				}
				if index >= 0 {
					break
				}
			}
			if index < 0 {
				for i, command := range commands {
					if strings.HasSuffix(command.Metadata.String("name"), ":"+name) {
						index = i
						break
					}
				}
			}
			if index < 0 {
				return output, fmt.Errorf("Team worker skill unavailable: %s", name)
			}
			command := commands[index]
			if command.Definition == nil {
				return output, fmt.Errorf("Team worker skill is not prompt-based: %s", name)
			}
			args := ""
			text, err := command.Definition.Prompt(ctx, &args, inv)
			if err != nil {
				return output, err
			}
			// Only content is preloaded; the skill model/effort/permissions do
			// not replace the human-approved routing.
			blocks := rawMain([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("text")}, protocol.JSONField{Name: "text", Value: quoteMain(text)})})
			output.Messages = append(output.Messages, messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": blocks, "isMeta": json.RawMessage("true")})))
		}
	}
	if hooks := definition.Fields.Get("hooks"); hooks != nil {
		policy, err := m.options.Native.ResolvePolicy()
		if err != nil {
			return output, err
		}
		restricted := false
		if policy != nil {
			restricted = string(policy.Get("strictPluginOnlyCustomization")) == "true"
			var surfaces []string
			_ = json.Unmarshal(policy.Get("strictPluginOnlyCustomization"), &surfaces)
			for _, surface := range surfaces {
				restricted = restricted || surface == "hooks"
			}
		}
		trusted := definition.Source == "plugin" || definition.Source == "policySettings" || definition.Source == "built-in" || definition.Source == "builtin" || definition.Source == "bundled"
		if restricted && !trusted {
			return output, errors.New("Team worker hooks are blocked by customization policy")
		}
		var events protocol.JSONObject
		if err := events.UnmarshalJSON(hooks); err != nil {
			return output, err
		}
		if err := m.hook.RegisterNativeMainHooks(m.Controller.SessionID(), events); err != nil {
			return output, err
		}
	}
	return output, nil
}

func (m *MainPrint) nativeTeamMessageBindings() func(context.Context, *cooperative.Scope, protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if m.options.Native == nil || !m.options.Native.NativeProcess {
		return nil
	}
	return m.sendNativeTeamMessage
}
func (m *MainPrint) sendNativeTeamMessage(ctx context.Context, scope *cooperative.Scope, in protocol.JSONObject) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if _, structured := in.Object("message"); structured {
		return m.sendNativeStructuredTeamMessage(ctx, scope, in)
	}
	future, handled, resumeErr := m.tryResumeNativeInProcessTarget(scope, ctx, in)
	if handled || resumeErr != nil {
		if resumeErr != nil {
			out, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](m.Owner)
			if err != nil {
				return nil, err
			}
			response := nativeInProcessObject(rawMain(map[string]any{"success": false, "message": fmt.Sprintf("Failed to resume teammate %q — nothing was sent: %s", in.String("to"), resumeErr.Error())}))
			return out, out.Resolve(scope, queryloop.ToolCallResult{Data: rawMain(response)})
		}
		return future, nil
	}

	var err error
	team := m.teamWorker
	name, sender := m.nativeLeaderTeamName(), "team-lead"
	var senderColor string
	var mailbox *teamstore.Mailbox
	var config *teamstore.Config
	var targetColor json.RawMessage
	if team != nil {
		name, sender = team.preset.Start.TeamName, team.preset.Start.Name
	} else if teammate := m.nativeInProcessTeammateIdentity(); teammate != nil {
		identity, err := m.Controller.TeammateIdentityOnOwner(scope)
		if err != nil || identity != *teammate {
			return nil, cooperative.ErrRestricted
		}
		name, sender, senderColor = identity.TeamName, identity.AgentName, identity.Color
	} else if m.teamState != nil {
		var teammates protocol.JSONObject
		_ = teammates.UnmarshalJSON(m.teamState.snapshot.Get("teammates"))
		for _, field := range teammates.OwnProperties() {
			var teammate protocol.JSONObject
			_ = teammate.UnmarshalJSON(field.Value)
			if teammate.String("name") == in.String("to") {
				targetColor = teammate.Get("color")
				break
			}
		}
	}
	if team != nil && team.mailbox == nil {
		team.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
		if err != nil {
			return nil, err
		}
		team.config, err = teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, PID: m.options.PID, Platform: m.options.Platform})
		if err != nil {
			return nil, err
		}
	}
	if team != nil {
		mailbox, config = team.mailbox, team.config
	} else {
		if m.teamLeader == nil {
			return nil, mainCap("actual Team leader mailbox lifecycle")
		}
		if m.teamLeader.mailbox == nil {
			m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore, Log: func(err error) { _, _ = fmt.Fprintln(m.options.Error, err) }})
			if err != nil {
				return nil, err
			}
		}
		mailbox, config = m.teamLeader.mailbox, m.teamLeader.config
	}
	return nativeSkillWork(m, scope, func() (queryloop.ToolCallResult, error) {
		if err := context.Cause(ctx); err != nil {
			return queryloop.ToolCallResult{}, err
		}
		to, content := in.String("to"), in.String("message")
		target := "@" + to
		message := fmt.Sprintf("Message sent to %s's inbox", to)
		routing := protocol.NewJSONObject(protocol.JSONField{Name: "sender", Value: quoteMain(sender)}, protocol.JSONField{Name: "target", Value: quoteMain(target)}, protocol.JSONField{Name: "summary", Value: in.Get("summary")}, protocol.JSONField{Name: "content", Value: in.Get("message")})
		if targetColor != nil {
			routing.Set("targetColor", targetColor)
		}
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoteMain(sender)}, protocol.JSONField{Name: "text", Value: quoteMain(content)}, protocol.JSONField{Name: "summary", Value: in.Get("summary")}, protocol.JSONField{Name: "timestamp", Value: quoteMain(m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z"))})
		if senderColor != "" {
			routing.Set("senderColor", quoteMain(senderColor))
			envelope.Set("color", quoteMain(senderColor))
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: json.RawMessage("true")}, protocol.JSONField{Name: "message", Value: quoteMain(message)})
		if to == "*" {
			if name == "" {
				return queryloop.ToolCallResult{}, errors.New("Not in a team context. Create a team with Teammate spawnTeam first, or set CLAUDE_CODE_TEAM_NAME.")
			}
			file := config.Read(name)
			if file == nil {
				return queryloop.ToolCallResult{}, fmt.Errorf("Team %q does not exist", name)
			}
			var members []protocol.JSONObject
			_ = json.Unmarshal(file.Get("members"), &members)
			recipients := []string{}
			id := "mailbox-" + uuidMain()
			envelope.Set("id", quoteMain(id))
			for _, member := range members {
				if strings.ToLower(member.String("name")) != strings.ToLower(sender) {
					recipients = append(recipients, member.String("name"))
				}
			}
			result.Set("recipients", rawMain(recipients))
			if len(recipients) == 0 {
				result.Set("message", quoteMain("No teammates to broadcast to (you are the only team member)"))
				return queryloop.ToolCallResult{Data: rawMain(result)}, nil
			}
			failed, delivered := []string{}, []string{}
			for _, recipient := range recipients {
				if err := mailbox.Write(recipient, name, envelope); err != nil {
					failed = append(failed, recipient)
				} else {
					delivered = append(delivered, recipient)
				}
			}
			if len(failed) > 0 {
				result.Set("success", rawMain(false))
				result.Set("recipients", rawMain(delivered))
				text := fmt.Sprintf("Failed to write to the inbox of %s — nothing was sent. Try again.", strings.Join(failed, ", "))
				if len(delivered) > 0 {
					text = fmt.Sprintf("Message broadcast to %d of %d teammate(s): %s. Failed to write to the inbox of %s — they did not receive it. Message them directly instead of broadcasting again.", len(delivered), len(recipients), strings.Join(delivered, ", "), strings.Join(failed, ", "))
				}
				result.Set("message", quoteMain(text))
				return queryloop.ToolCallResult{Data: rawMain(result)}, nil
			}
			result.Set("message", quoteMain(fmt.Sprintf("Message broadcast to %d teammate(s): %s", len(recipients), strings.Join(recipients, ", "))))
			routing.Set("target", quoteMain("@team"))
		} else if err := mailbox.Write(to, name, envelope); err != nil {
			result.Set("success", rawMain(false))
			result.Set("message", quoteMain(fmt.Sprintf("Failed to write to %s's inbox — nothing was sent. Try again.", to)))
			return queryloop.ToolCallResult{Data: rawMain(result)}, nil
		}
		result.Set("routing", rawMain(routing))
		return queryloop.ToolCallResult{Data: rawMain(result)}, nil
	})
}
