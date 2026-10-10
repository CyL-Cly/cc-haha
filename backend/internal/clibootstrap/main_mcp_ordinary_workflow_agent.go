package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"io"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func ordinaryWorkflowOwner[T any](m *NativeMCPToolContext, ctx context.Context, run func(*cooperative.Scope) (T, error)) (T, error) {
	var value T
	receipt, e := m.command.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error { var e error; value, e = run(scope); return e })
	if e == nil {
		e = receipt.Wait()
	}
	return value, e
}
func ordinaryWorkflowBridge[T any](m *NativeMCPToolContext, child *MainPrint, scope *cooperative.Scope, run func(*cooperative.Scope) (T, error)) (T, error) {
	future, e := nativeSkillWork(child, scope, func() (T, error) { return ordinaryWorkflowOwner(m, context.Background(), run) })
	var zero T
	if e != nil {
		return zero, e
	}
	return future.Await(scope)
}
func (m *NativeMCPToolContext) runOrdinaryWorkflowAgent(ctx context.Context, request workflow.AgentExecution) (workflow.AgentOutcome, error) {
	prepare := func(ctx context.Context, request workflow.AgentExecution) (nativeWorkflowAgentPrepared, error) {
		return ordinaryWorkflowOwner(m, ctx, func(scope *cooperative.Scope) (nativeWorkflowAgentPrepared, error) {
			return m.prepareOrdinaryWorkflowAgent(ctx, scope, request)
		})
	}
	finish := func(p nativeWorkflowAgentPrepared) {
		_, _ = ordinaryWorkflowOwner(m, context.Background(), func(scope *cooperative.Scope) (struct{}, error) {
			_, e := m.modelAuth.finishAgentWorktree(scope, p.worktree)
			return struct{}{}, e
		})
	}
	// Source emits Agent SDK events into its process queue, which entrypoints/mcp
	// does not drain onto the MCP stdout transport. Journal callbacks are real.
	emit := func(nativeWorkflowAgentPrepared, string, string, json.RawMessage, error) error { return nil }
	return runNativeWorkflowPrepared(ctx, request, prepare, finish, emit, "", m.modelAuth.writeAgentWorktreeMetadata)
}
func (m *NativeMCPToolContext) prepareOrdinaryWorkflowAgent(ctx context.Context, scope *cooperative.Scope, in workflow.AgentExecution) (nativeWorkflowAgentPrepared, error) {
	var out nativeWorkflowAgentPrepared
	if e := m.checkOrdinary(scope); e != nil {
		return out, e
	}
	// The workflow's internal definition is independent of the ordinary Agent
	// tool. Explicit named definitions still resolve against MCP's actual empty
	// agentDefinitions.activeAgents, through the shared resolver.
	selected, e := resolveNativeWorkflowAgent([]agentcatalog.Definition{}, in.Options)
	if e != nil {
		return out, e
	}
	var schema *workflow.SchemaValidator
	var schemaJSON json.RawMessage
	if in.Schema != nil {
		type prepared struct {
			validator *workflow.SchemaValidator
			raw       json.RawMessage
		}
		future, e := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (prepared, error) { v, r, e := in.Schema.Prepare(ctx); return prepared{v, r}, e })
		if e != nil {
			return out, e
		}
		v, e := future.Await(scope)
		if e != nil {
			return out, fmt.Errorf("agent({schema}): %s", e)
		}
		schema, schemaJSON = v.validator, v.raw
	}
	settings, e := m.command.Startup.GetInitialSettings()
	if e != nil {
		return out, e
	}
	selector, e := modelselection.NewParsing(modelselection.Options{Environment: m.command.Native.Environment, Settings: settings.Clone()})
	if e != nil {
		return out, e
	}
	modelRaw, e := selector.AgentModel(modelselection.AgentInput{ParentModel: quoteMain(m.model), ToolSpecifiedModel: in.Options.Get("model"), PermissionMode: "acceptEdits"})
	if e != nil {
		return out, e
	}
	model, e := decodeNativeModel(modelRaw)
	if e != nil {
		return out, e
	}
	id, e := nativeAgentID()
	if e != nil {
		return out, e
	}
	identity, e := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (struct{}, error) { return struct{}{}, in.OnAgentID(id) })
	if e != nil {
		return out, e
	}
	if _, e = identity.Await(scope); e != nil {
		return out, e
	}
	request := builtintools.AgentRequest{Prompt: in.Prompt, AgentType: selected.Name, Description: in.Options.String("label"), Isolation: in.Options.String("isolation"), PermissionContext: emptyMCPPermissionContext()}
	if in.Options.Get("label") == nil {
		units, _ := protocol.DecodeJavaScriptJSONString(quoteMain(in.Prompt))
		if len(units) > 60 {
			units = units[:60]
		}
		_ = json.Unmarshal(protocol.QuoteJavaScriptUTF16(units), &request.Description)
	}
	tree := &nativeAgentWorktree{CWD: m.state.Snapshot().CWD}
	if request.Isolation == "worktree" {
		settings, e := m.command.Startup.GetInitialSettings()
		if e != nil {
			return out, e
		}
		cfg := worktree.Settings{}
		if settings != nil {
			_ = json.Unmarshal(settings.Get("worktree"), &cfg)
		}
		cwd := tree.CWD
		future, e := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (worktree.AgentAttempt, error) {
			return m.worktreeService.CreateAgent(context.Background(), cwd, "agent-"+id[:8], cfg)
		})
		if e != nil {
			return out, e
		}
		attempt, e := future.Await(scope)
		if e != nil {
			return out, e
		}
		tree.service, tree.Info, tree.Skipped = m.worktreeService, attempt.Worktree, attempt.UnavailableReason
		// Stock runWorkflowAgent creates the async generator inside the cwd
		// override, then consumes it outside. Ordinary MCP therefore executes
		// the worker in the original directory, while retaining the actual
		// worktree creation, metadata and cleanup producers.
	}

	env, e := systemprompt.ComputeAgentEnvironment(ctx, systemprompt.Options{Environment: m.command.Settings.environment, CWD: func() string { return tree.CWD }, OSInfo: nativeOSInfo, Platform: m.platform, IsGit: func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: m.command.Native}, tree.CWD) != "", nil
	}}, model, nil)
	if e != nil {
		return out, e
	}
	prompt := workflowAgentPrompt
	if in.Schema != nil {
		prompt = workflowStructuredAgentPrompt
		request.Prompt += workflowStructuredNote
	}
	directory, e := m.background.OutputDirectory(scope)
	if e != nil {
		return out, e
	}
	d := &NativeDetachedAgentOptions{Bootstrap: m.state.Snapshot(), PromptSections: []json.RawMessage{quoteMain(prompt), quoteMain(workflowAgentNotes), env}, PermissionMode: "acceptEdits", TaskDirectory: directory, AgentType: selected.Name, sourceMCP: &NativeMCPToolContext{platform: m.platform, model: model}}
	if in.Schema != nil {
		d.synthetic = &nativeWorkflowSynthetic{Schema: schemaJSON, Validate: schema.Validate}
	}
	d.bindTasks = func(child *MainPrint) *builtintools.BackgroundTaskBindings {
		return d.sourceMCP.toolsBackgroundBindings(child)
	}
	d.transferSettings = func(child *MainPrint, s *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
		return ordinaryWorkflowBridge(m, child, s, m.models.TransferNativeSettings)
	}
	d.liveSettings = func(child *MainPrint, s *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
		type view struct {
			settings   *protocol.JSONObject
			permission protocol.JSONObject
		}
		v, e := ordinaryWorkflowBridge(m, child, s, func(scope *cooperative.Scope) (view, error) {
			settings, e := m.command.Startup.GetInitialSettings()
			if e != nil {
				return view{}, e
			}
			copy := settings.Clone()
			permission := emptyMCPPermissionContext()
			permission.Set("mode", quoteMain("acceptEdits"))
			return view{&copy, permission}, nil
		})
		return v.settings, v.permission, e
	}
	d.accountingRelay = func(child *MainPrint, s *cooperative.Scope, delta modelaccounting.LedgerDelta) error {
		_, e := ordinaryWorkflowBridge(m, child, s, func(scope *cooperative.Scope) (struct{}, error) {
			return struct{}{}, m.models.ApplyLedgerDelta(scope, delta.Clone())
		})
		return e
	}
	d.takeQueued = func(child *MainPrint, s *cooperative.Scope) ([]protocol.JSONObject, error) {
		return []protocol.JSONObject{}, s.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
	}
	d.pruneQueued = func(child *MainPrint, s *cooperative.Scope, _ string) error {
		return s.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
	}
	d.userImageAuthorized = func(child *MainPrint, s *cooperative.Scope, path string) (bool, error) {
		return ordinaryWorkflowBridge(m, child, s, func(scope *cooperative.Scope) (bool, error) {
			return m.modelAuth.nativeUserImageAuthorized(scope, path)
		})
	}
	d.rememberUserImage = func(child *MainPrint, s *cooperative.Scope, path string) error {
		_, e := ordinaryWorkflowBridge(m, child, s, func(scope *cooperative.Scope) (struct{}, error) {
			return struct{}{}, m.modelAuth.nativeRememberUserImage(scope, path)
		})
		return e
	}

	_, pool, e := m.tools.Catalog(scope)
	if e != nil {
		return out, e
	}
	available := []string{}
	for _, tool := range pool {
		available = append(available, tool.Name)
	}
	if in.Schema != nil {
		available = append(available, "StructuredOutput")
	}
	resolved := agentcatalog.ResolveTools(selected, available, false, false)
	names := resolved.ResolvedTools
	disabled := false
	n := m.command.Native.agentScopedBoundary(tree.CWD)
	o := MainPrintOptions{OnlyToolNames: &names, DetachedAgent: d, AgentID: id, QuerySource: "workflow_agent", Arguments: PrintArguments{ComputerUse: &disabled, SessionID: d.Bootstrap.SessionID, Model: model, MaxTurns: math.MaxInt32, PermissionMode: "acceptEdits", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: m.command.Settings.sources, Tools: []string{strings.Join(names, ",")}}, Native: n, PID: m.command.pid, Version: m.command.version, Platform: m.platform, Input: strings.NewReader(""), Output: io.Discard, Error: m.command.Error, OnOutput: func([]byte) error { return nil }}
	ref, e := m.binding.Acquire(scope)
	if e != nil {
		return out, e
	}
	return nativeWorkflowAgentPrepared{options: o, request: request, agentID: id, worktree: tree, ref: ref}, nil
}
func (m *NativeMCPToolContext) workflowChildOptions(scope *cooperative.Scope, child *MainPrint, options builtintools.Options) (builtintools.Options, error) {
	m.bindWorkflowCommand(child)
	m.state, m.plans, m.binding, m.workflowDefinitions = child.state, child.plans, options.NativeBinding, child.workflowDefinitions
	m.cache = options.Cache

	m.modelAuth = child
	ordinary, e := m.ordinaryOptions(options)
	if e != nil {
		return options, e
	}
	// The native worker owns the actual Web/image/shell transport, permission and
	// history. Only the fresh AppState and fixed command/agent producers are
	// inherited from ordinary MCP, as in Source createSubagentContext.
	options.AgentDefinitions, options.RunAgent, options.RunBackgroundAgent = ordinary.AgentDefinitions, ordinary.RunAgent, ordinary.RunBackgroundAgent
	options.GetTodos, options.SetTodos, options.PlanMode, options.BackgroundTasks = ordinary.GetTodos, ordinary.SetTodos, ordinary.PlanMode, ordinary.BackgroundTasks
	options.RipgrepConfig = ordinary.RipgrepConfig
	options.Skill = ordinary.Skill
	options.Skill.Call = child.nativeSkillCall
	options.Workflow = ordinary.Workflow
	m.background = ordinary.BackgroundTasks
	return options, nil
}
func (m *NativeMCPToolContext) toolsBackgroundBindings(child *MainPrint) *builtintools.BackgroundTaskBindings {
	m.bindWorkflowCommand(child)
	m.state = child.state
	return m.ordinaryBackgroundBindings()
}

func (m *NativeMCPToolContext) bindWorkflowCommand(child *MainPrint) {
	m.command = &MainCommand{version: child.options.Version, pid: child.options.PID, Owner: child.Owner, Native: child.options.Native, Settings: child.nativeSettings, Startup: child.Startup, Global: child.Global, Secure: child.secure, Output: io.Discard, Error: child.options.Error, NonInteractive: true}
}
