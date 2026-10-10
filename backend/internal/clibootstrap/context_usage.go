package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

func (m *MainPrint) ReadContextUsage(ctx context.Context, estimateOnly bool) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.Host == nil || !m.initialized || m.closed {
			return cooperative.ErrClosed
		}
		var e error
		result, e = m.nativeContextUsageWithContext(ctx, scope, estimateOnly)
		return e
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) nativeContextUsage(scope *cooperative.Scope, estimateOnly bool) (protocol.JSONObject, error) {
	return m.nativeContextUsageWithContext(context.Background(), scope, estimateOnly)
}

func (m *MainPrint) nativeContextUsageWithContext(ctx context.Context, scope *cooperative.Scope, estimateOnly bool) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.prompt == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	actual, err := m.Host.SnapshotContextInspection(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	projection, err := preflightcore.AfterCompactBoundary(actual.History)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	history := []protocol.JSONObject{}
	for _, entry := range projection.Records {
		history = append(history, entry.Record)
	}
	dependencies := requestcontext.Dependencies{UUID: uuidMain, Now: m.options.Native.Now}
	normalized, err := requestcontext.NormalizeForInspection(history, dependencies)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	tools := []protocol.JSONObject{}
	for _, definition := range actual.Definitions {
		tools = append(tools, protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: quoteMain(definition.Kind)}, protocol.JSONField{Name: "name", Value: quoteMain(definition.Name)}))
	}
	// Source always evaluates its default producer during analysis, even when
	// the effective custom prompt subsequently replaces those sections.
	prompt, err := m.prompt.ResolveForModel(context.Background(), tools, actual.Model)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if m.options.Arguments.SystemPrompt != "" {
		prompt = []json.RawMessage{quoteMain(m.options.Arguments.SystemPrompt)}
	}
	if m.options.Arguments.AppendSystemPrompt != "" {
		prompt = append(prompt, quoteMain(m.options.Arguments.AppendSystemPrompt))
	}
	system, err := m.nativeInspectionSystemContext(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	env := m.options.Native.Environment
	resolver := modelaccounting.RestoredModelResolver{Environment: env, Settings: m.Startup.GetInitialSettings, Global: m.Global.Get}
	window, err := resolver.ContextWindow(actual.Model)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	simple := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	in := contextusage.Input{Model: actual.Model, Window: window, EffectiveWindow: querypreflight.EffectiveContextWindow(actual.MainModel, env), Simple: simple, EstimateOnly: estimateOnly, History: history, Normalized: normalized, SystemPrompt: prompt, SystemContext: system, Definitions: actual.Definitions, Schemas: actual.Schemas, AutoCompact: !envTruthyNative(env["DISABLE_COMPACT"]) && !envTruthyNative(env["DISABLE_AUTO_COMPACT"]) && nativeSettingsTruthy(global.Get("autoCompactEnabled")), LowTrust: !nativeGlobalCacheScope(env) || toolsearch.Provider(env) != "firstParty", Internal: env["USER_TYPE"] == "ant"}
	if !estimateOnly {
		in.Count, err = m.nativeTokenCounter(ctx, scope, actual.MainModel)
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	if !simple {
		future, err := nativeSkillWork(m, scope, m.userContext.MemoryFilesForInspection)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		in.Memory, err = future.Await(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	if m.agents != nil {
		agents, err := m.agents.List(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		for _, agent := range agents.Active {
			in.Agents = append(in.Agents, contextusage.Agent{Name: agent.Name, Description: agent.Description, Source: agent.Source})
		}
	}
	commands, err := m.nativeSkillCommands(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err == nil {
		for _, command := range commands {
			meta := command.Metadata
			loaded := meta.String("loadedFrom")
			eligible := meta.String("type") == "prompt" && !nativeSettingsTruthy(meta.Get("disableModelInvocation")) && meta.String("source") != "builtin" && (loaded == "bundled" || loaded == "skills" || loaded == "commands_DEPRECATED" || nativeSettingsTruthy(meta.Get("hasUserSpecifiedDescription")) || nativeSettingsTruthy(meta.Get("whenToUse")))
			if eligible {
				in.SkillMetadata = append(in.SkillMetadata, meta)
			}
		}
	}
	in.CommandTotal = len(in.SkillMetadata)
	in.CommandIncluded = len(in.SkillMetadata)
	if m.toolSearch != nil {
		catalogue, err := m.Host.NativeToolSearchCatalogue(scope)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if catalogue != nil {
			plan, err := m.toolSearch.Plan(context.Background(), actual.Model, catalogue.Tools, history, len(catalogue.PendingServers) > 0, catalogue.Gate)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			in.ToolSearch = plan.Enabled
		}
	}
	in.AttachmentMessages = func(raw json.RawMessage) ([]protocol.JSONObject, error) {
		return attachments.ProjectWithOptions(raw, m.factory, nil)
	}
	in.EstimateCanonical = func(history []protocol.JSONObject) (float64, error) {
		return preflightcore.EstimateContextWithAttachments(history, &preflightcore.AttachmentOptions{Factory: m.factory})
	}
	data, err := contextusage.Analyze(in)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var result protocol.JSONObject
	err = result.UnmarshalJSON(encoded)
	return result, err
}
