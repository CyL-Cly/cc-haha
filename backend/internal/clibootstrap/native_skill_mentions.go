package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativeInputAttachments(ctx context.Context, scope *cooperative.Scope, text string, history []queryloop.Message) ([]queryloop.Message, error) {
	input, e := m.nativeSkillMentions(ctx, scope, text)
	if e != nil {
		return nil, e
	}
	if !skillTruthy(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) && !skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"]) {
		keyword, err := m.nativeWorkflowKeywordAttachment(scope, text)
		if err != nil {
			return nil, err
		}
		input = append(input, keyword...)
	}
	common, e := m.nativeAttachments(scope, history)
	if e != nil {
		return nil, e
	}
	return append(input, common...), nil
}

func (m *MainPrint) nativeSkillMentions(ctx context.Context, scope *cooperative.Scope, text string) ([]queryloop.Message, error) {
	out := []queryloop.Message{}
	if skillTruthy(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) || skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"]) {
		return out, nil
	}
	mentions, e := skillcommands.ExtractPromptMentions(text)
	if e != nil {
		return nil, e
	}
	files := []*ownerasync.OwnerFuture[json.RawMessage]{}
	for _, file := range mentions.Parsed {
		future, e := ownerasync.NewOwnerFuture[json.RawMessage](m.Owner)
		if e != nil {
			return nil, e
		}
		_, e = m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			if m.nativeTools == nil {
				return future.Reject(next, mainCap("original shared Read attachment tool"))
			}
			payload, failure := m.nativeTools.SkillFileAttachment(ctx, next, file)
			if failure != nil {
				return future.Reject(next, failure)
			}
			return future.Resolve(next, payload)
		})
		if e != nil {
			return nil, e
		}
		files = append(files, future)
	}
	for _, file := range files {
		payload, e := file.Await(scope)
		if e != nil {
			return nil, e
		}
		if len(payload) > 0 {
			out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(payload)})
		}
	}
	for _, mention := range mentions.Resources {
		if m.mcpTools == nil {
			return nil, mainCap("same connected MCP resource service for Skill mentions")
		}
		payload, e := m.mcpTools.SkillResourceAttachment(ctx, scope, mention)
		if e != nil {
			return nil, e
		}
		if len(payload) > 0 {
			out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(payload)})
		}
	}
	if len(mentions.Agents) > 0 {
		if m.agents == nil {
			return nil, mainCap("actual active agent definitions for Skill mentions")
		}
		agents, e := m.agents.List(scope)
		if e != nil {
			return nil, e
		}
		for _, mention := range mentions.Agents {
			name := strings.Replace(mention, "agent-", "", 1)
			for _, agent := range agents.Active {
				if agent.Fields.String("agentType") == name {
					out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("agent_mention")}, protocol.JSONField{Name: "agentType", Value: quoteMain(name)})))})
					break
				}
			}
		}
	}
	return out, nil
}
