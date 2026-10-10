package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

const workflowAgentPrompt = "You are a subagent spawned by a workflow orchestration script. Use the tools available to complete the task.\nNOTE: You are running inside a workflow script. Your final text response is returned verbatim as a string to the calling script — it is your return value, not a message to a human. Output the literal result; do not output confirmations like \"Done.\" Be concise — the script will parse your output."

const workflowAgentNotes = "Notes:\n- Agent threads always have their cwd reset between bash calls, as a result please only use absolute file paths.\n- In your final response, share file paths (always absolute, never relative) that are relevant to the task. Include code snippets only when the exact text is load-bearing (e.g., a bug you found, a function signature the caller asked for) — do not recap code you merely read.\n- For clear communication with the user the assistant MUST avoid using emojis.\n- Do not use a colon before tool calls. Text like \"Let me read the file:\" followed by a read tool call should just be \"Let me read the file.\" with a period."

type nativeWorkflowAgentPrepared struct {
	options  MainPrintOptions
	request  builtintools.AgentRequest
	agentID  string
	worktree *nativeAgentWorktree
	ref      *ownerasync.NativeReference
}

// Workflow owns the only orchestration task. Its synchronous agents use the
// shared MainPrint query/tool graph on an independent native Owner, without
// entering the ordinary background Agent launcher or creating local_agent rows.
func (m *MainPrint) runNativeWorkflowAgent(ctx context.Context, request workflow.AgentExecution) (workflow.AgentOutcome, error) {
	prepare := func(ctx context.Context, request workflow.AgentExecution) (nativeWorkflowAgentPrepared, error) {
		var prepared nativeWorkflowAgentPrepared
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			var e error
			prepared, e = m.prepareNativeWorkflowAgent(ctx, scope, request)
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		return prepared, e
	}
	finish := func(prepared nativeWorkflowAgentPrepared) {
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			_, e := m.finishAgentWorktree(scope, prepared.worktree)
			return e
		})
		if e == nil {
			_ = receipt.Wait()
		}
	}
	emit := func(prepared nativeWorkflowAgentPrepared, streamID, kind string, message json.RawMessage, cause error) error {
		envelope := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("agent_run_message")}, protocol.JSONField{Name: "run_agent_id", Value: quoteMain(prepared.agentID)}, protocol.JSONField{Name: "stream_id", Value: quoteMain(streamID)}, protocol.JSONField{Name: "target_agent_id", Value: quoteMain(prepared.agentID)}, protocol.JSONField{Name: "event_kind", Value: quoteMain(kind)}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "session_id", Value: quoteMain(prepared.options.Arguments.SessionID)})
		envelope.Set("message", message)
		if cause != nil {
			envelope.Set("error", quoteMain(cause.Error()))
		}
		data, e := protocol.EncodeNDJSON(envelope)
		if e != nil {
			return e
		}
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return m.enqueue(data) })
		if e == nil {
			e = receipt.Wait()
		}
		return e
	}
	return runNativeWorkflowPrepared(ctx, request, prepare, finish, emit, m.options.AgentID, m.writeAgentWorktreeMetadata)
}

// Both ordinary MCP and ASK Workflow use this same native Agent/query runner.
func runNativeWorkflowPrepared(ctx context.Context, request workflow.AgentExecution, prepare func(context.Context, workflow.AgentExecution) (nativeWorkflowAgentPrepared, error), finish func(nativeWorkflowAgentPrepared), deliver func(nativeWorkflowAgentPrepared, string, string, json.RawMessage, error) error, ownerAgentID string, writeMetadata func(string, protocol.JSONObject)) (out workflow.AgentOutcome, returned error) {
	if request.OnAgentID == nil || request.OnProgress == nil {
		return out, mainCap("Workflow real journal/progress callbacks")
	}
	prepared, e := prepare(ctx, request)
	if e != nil {
		return out, e
	}
	defer prepared.ref.DeliveryFinished()
	defer finish(prepared)
	var child *MainPrint
	o := prepared.options
	progress := &foregroundAgentProgress{}
	messages := []queryloop.Message{}
	var structured json.RawMessage
	structuredSeen := false
	streamID, e := nativeAgentID()
	if e != nil {
		return out, e
	}
	emit := func(kind string, message json.RawMessage, cause error) error {
		return deliver(prepared, streamID, kind, message, cause)
	}
	o.OnQueryYield = func(scope *cooperative.Scope, yield *queryloop.Yield) error {
		var data json.RawMessage
		var e error
		if yield.Stream != nil {
			data, e = yield.Stream.EventJSON()
		} else if yield.Message != nil {
			data, e = yield.Message.MarshalJSON()
		}
		if e != nil {
			return e
		}
		if data == nil {
			return nil
		}
		if yield.Message != nil {
			var displayed protocol.JSONObject
			if e = displayed.UnmarshalJSON(data); e != nil {
				return e
			}
			messages = append(messages, queryloop.Message{Record: displayed.Clone()})
			if displayed.String("type") == "attachment" {
				attachment, _ := displayed.Object("attachment")
				if attachment.String("type") == "structured_output" {
					structured = append(json.RawMessage(nil), attachment.Get("data")...)
					structuredSeen = true
				}
			}
			if displayed.String("type") == "assistant" {
				last := progress.observe(data)
				if e = request.OnProgress(workflow.AgentProgress{Tokens: progress.input + progress.output, ToolCalls: progress.tools, LastToolName: last}); e != nil {
					return e
				}
			}
		}
		return emit("message", data, nil)
	}
	child, e = NewMainPrint(o)
	if e != nil {
		return out, e
	}
	defer func() { _ = child.ShutdownSession(context.Background()) }()
	if e = child.StartSession(ctx); e != nil {
		return out, e
	}
	var inline []mcpruntime.Connection
	defer func() {
		for _, peer := range inline {
			_ = child.mcpRuntime.CloseConnection(context.Background(), peer)
		}
		child.hook.ClearNativeAgentHooks(child.options.AgentID)
	}()
	if e = child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		preset := child.options.DetachedAgent.preset
		if preset == nil {
			return child.Host.InstallNativeAgentPrelude(scope, []protocol.JSONObject{})
		}
		if e := preset.registerHooks(child, scope); e != nil {
			return e
		}
		rows, e := preset.skills(ctx, child, scope)
		if e != nil {
			return e
		}
		if e = child.Host.InstallNativeAgentPrelude(scope, rows); e != nil {
			return e
		}
		additional, created, e := preset.connectMCP(ctx, child, scope)
		inline = created
		if e != nil {
			return e
		}
		if e = child.mcpTools.InstallAgentConnections(scope, preset.inherited, additional, preset.resources); e != nil {
			return e
		}
		names := []string{}
		for _, peer := range additional {
			if peer.Status == "connected" {
				for _, tool := range peer.Tools {
					names = append(names, mcpclient.ToolName(peer.Name, tool.String("name")))
				}
			}
		}
		return child.Host.AppendNativeAgentMCPTools(scope, names)
	}); e != nil {
		return out, e
	}
	var path string
	if e = child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		path, e = child.Controller.Writer().ComputedTranscriptPath(scope)
		return e
	}); e != nil {
		return out, e
	}
	provenance := protocol.NewJSONObject(protocol.JSONField{Name: "runId", Value: quoteMain(request.RunID)}, protocol.JSONField{Name: "name", Value: quoteMain(request.WorkflowName)}, protocol.JSONField{Name: "phaseIndex", Value: rawMain(request.PhaseIndex)}, protocol.JSONField{Name: "agentIndex", Value: rawMain(request.Index)})
	if request.WorkflowName == "" {
		provenance.Delete("name")
	}
	if request.PhaseTitle != "" {
		provenance.Set("phaseTitle", quoteMain(request.PhaseTitle))
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain(prepared.request.AgentType)}, protocol.JSONField{Name: "description", Value: quoteMain(prepared.request.Description)}, protocol.JSONField{Name: "workflow", Value: rawMain(provenance)})
	if request.Options.String("model") != "" {
		metadata.Set("model", request.Options.Get("model"))
	}
	if ownerAgentID != "" {
		metadata.Set("ownerAgentId", quoteMain(ownerAgentID))
	}
	if prepared.worktree.Info != nil {
		metadata.Set("worktreePath", quoteMain(prepared.worktree.Info.Path))
	}
	writeMetadata(path, metadata)
	e = child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(o.Arguments.SessionID)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(prepared.request.Prompt)}))})
		if e := child.Host.RunOne(scope, ctx, input); e != nil {
			return e
		}
		if child.Host.ExitCode() != 0 {
			return fmt.Errorf("Workflow agent %s query failed", prepared.agentID)
		}
		return nil
	})
	if e != nil {
		kind := "error"
		if ctx.Err() != nil {
			kind = "cancelled"
		}
		_ = emit(kind, nil, e)
		return out, e
	}
	if e = emit("complete", nil, nil); e != nil {
		return out, e
	}
	if request.Schema != nil {
		if !structuredSeen {
			return out, errors.New("agent({schema}): the subagent ended without calling StructuredOutput")
		}
		return workflow.AgentOutcome{AgentID: prepared.agentID, Value: structured, Tokens: progress.input + progress.output, ToolCalls: progress.tools}, nil
	}
	value := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type() != "assistant" {
			continue
		}
		api, _ := messages[i].Record.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(api.Get("content"), &blocks)
		texts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				texts = append(texts, block.String("text"))
			}
		}
		candidate := workflowTrim(strings.Join(texts, "\n"))
		if candidate != "" {
			value = candidate
			break
		}
	}
	return workflow.AgentOutcome{AgentID: prepared.agentID, Value: quoteMain(value), Tokens: progress.input + progress.output, ToolCalls: progress.tools}, nil
}

func workflowTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == 0xfeff || r != 0x85 && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000)
	})
}

func (m *MainPrint) prepareNativeWorkflowAgent(ctx context.Context, scope *cooperative.Scope, input workflow.AgentExecution) (nativeWorkflowAgentPrepared, error) {
	var out nativeWorkflowAgentPrepared
	if !scope.BelongsTo(m.Owner) || m.nativeTools == nil || m.nativeModelSelector == nil || m.prompt == nil {
		return out, cooperative.ErrRestricted
	}
	var definitions []agentcatalog.Definition
	if input.Options.Get("agentType") != nil {
		result, e := m.agents.List(scope)
		if e != nil {
			return out, e
		}
		definitions = result.Active
	}
	selected, e := resolveNativeWorkflowAgent(definitions, input.Options)
	if e != nil {
		return out, e
	}

	var schemaValidator *workflow.SchemaValidator
	var schemaJSON json.RawMessage
	if input.Schema != nil {
		type schemaPreparation struct {
			validator *workflow.SchemaValidator
			raw       json.RawMessage
		}
		future, e := nativeSkillWork(m, scope, func() (schemaPreparation, error) {
			validator, raw, e := input.Schema.Prepare(ctx)
			return schemaPreparation{validator, raw}, e
		})
		if e != nil {
			return out, e
		}
		prepared, e := future.Await(scope)
		if e != nil {
			return out, fmt.Errorf("agent({schema}): %s", e)
		}
		schemaValidator, schemaJSON = prepared.validator, prepared.raw
	}
	mode := selected.Fields.String("permissionMode")
	if mode == "" {
		mode = "acceptEdits"
	}
	if mode != "default" && mode != "acceptEdits" && mode != "plan" && mode != "bypassPermissions" && mode != "dontAsk" {
		return out, mainCap("Workflow custom Agent mode " + mode)
	}
	permission, e := m.nativeToolPermissionContext(scope)
	if e != nil {
		return out, e
	}
	permission = permission.Clone()
	permission.Set("mode", quoteMain(mode))
	_, pool, e := m.nativeTools.Catalog(scope)
	if e != nil {
		return out, e
	}
	names := []string{}
	for _, tool := range pool {
		names = append(names, tool.Name)
	}
	if m.mcpTools != nil {
		_, pool, e = m.mcpTools.Catalog(scope)
		if e != nil {
			return out, e
		}
		for _, tool := range pool {
			names = append(names, tool.Name)
		}
	}
	if input.Schema != nil {
		clean := names[:0]
		for _, name := range names {
			if name != "StructuredOutput" {
				clean = append(clean, name)
			}
		}
		names = append(clean, "StructuredOutput")
	}
	resolved := agentcatalog.ResolveTools(selected, names, false, false)
	prompt := quoteMain(selected.SystemPrompt)
	if selected.Name != "workflow-subagent" {
		prompt, e = m.nativeAgentPromptJSON(scope, selected)
		if e != nil {
			return out, e
		}
	}
	if input.Schema != nil {
		if selected.Name == "workflow-subagent" {
			prompt = quoteMain(workflowStructuredAgentPrompt)
		} else {
			prompt, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{prompt, quoteMain("\n" + workflowStructuredNote)}, "")
			if e != nil {
				return out, e
			}
		}
	}
	model, e := m.Host.InferenceModel(scope)
	if e != nil {
		return out, e
	}
	var definitionModel json.RawMessage
	if selected.Model != "" {
		definitionModel = quoteMain(selected.Model)
	}
	resolvedModel, e := m.nativeModelSelector.AgentModel(modelselection.AgentInput{AgentModel: definitionModel, ParentModel: quoteMain(model), ToolSpecifiedModel: input.Options.Get("model"), PermissionMode: mode})
	if e != nil {
		return out, e
	}
	model, e = decodeNativeModel(resolvedModel)
	if e != nil {
		return out, e
	}
	id, e := nativeAgentID()
	if e != nil {
		return out, e
	}
	// Source journals the actual identity before physical worktree creation.
	// Deliver off this Owner and await its real future so the callback may
	// submit to the same Controller without blocking its event loop.
	identity, e := nativeSkillWork(m, scope, func() (struct{}, error) {
		return struct{}{}, input.OnAgentID(id)
	})
	if e != nil {
		return out, e
	}
	if _, e = identity.Await(scope); e != nil {
		return out, e
	}
	request := builtintools.AgentRequest{Prompt: input.Prompt, AgentType: selected.Name, SelectedAgent: selected.Fields.Clone(), Description: input.Options.String("label"), Isolation: input.Options.String("isolation"), PermissionContext: permission, Tools: resolved.ResolvedTools}
	if input.Schema != nil {
		request.Prompt += workflowStructuredNote
	}
	if input.Options.Get("label") == nil {
		units, _ := protocol.DecodeJavaScriptJSONString(quoteMain(input.Prompt))
		if len(units) > 60 {
			units = units[:60]
		}
		request.Description = string(protocol.QuoteJavaScriptUTF16(units))
		_ = json.Unmarshal([]byte(request.Description), &request.Description)
	}
	tree, e := m.prepareAgentWorktree(ctx, scope, request, id)
	if e != nil {
		return out, e
	}
	fail := func(e error) (nativeWorkflowAgentPrepared, error) {
		_, _ = m.finishAgentWorktree(scope, tree)
		return out, e
	}
	environment, e := m.prompt.AgentEnvironmentAt(ctx, model, nil, tree.CWD, func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: m.options.Native}, tree.CWD) != "", nil
	})
	if e != nil {
		return fail(e)
	}
	snapshot := m.state.Snapshot()
	directory, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return fail(e)
	}
	ledger, e := m.Controller.ModelAccountingFor(m.Owner)
	if e != nil {
		return fail(e)
	}
	d := &NativeDetachedAgentOptions{authParent: m, Bootstrap: snapshot, PromptSections: []json.RawMessage{prompt, quoteMain(workflowAgentNotes), environment}, PermissionMode: mode, OmitClaudeMd: string(selected.Fields.Get("omitClaudeMd")) == "true", TaskDirectory: directory, AgentType: selected.Name}
	m.bindAgentImageAuthorization(d)
	preset, e := m.nativeAgentPreset(scope, selected)
	if e != nil {
		return fail(e)
	}
	d.preset = preset
	if input.Schema != nil {
		d.synthetic = &nativeWorkflowSynthetic{Schema: append(json.RawMessage(nil), schemaJSON...), Validate: schemaValidator.Validate}
	}
	d.bindTasks = func(child *MainPrint) *builtintools.BackgroundTaskBindings {
		return m.detachedTaskBindings(child, directory)
	}
	d.transferSettings = func(child *MainPrint, s *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
		return nativeAgentOwnerCall(child, s, m, m.Host.TransferNativeSettings)
	}
	d.liveSettings = func(child *MainPrint, s *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
		type view struct {
			settings   *protocol.JSONObject
			permission protocol.JSONObject
		}
		v, e := nativeAgentOwnerCall(child, s, m, func(s *cooperative.Scope) (view, error) {
			settings, p, e := m.Controller.SDKSettingsOnOwner(s)
			var copy *protocol.JSONObject
			if settings != nil {
				c := settings.Clone()
				copy = &c
			}
			p = p.Clone()
			p.Set("mode", quoteMain(mode))
			return view{copy, p}, e
		})
		return v.settings, v.permission, e
	}
	d.accountingRelay = func(child *MainPrint, s *cooperative.Scope, delta modelaccounting.LedgerDelta) error {
		copy := delta.Clone()
		return bridgeAgentVoid(child, m, s, func(s *cooperative.Scope) error { return ledger.ApplyLedgerDelta(s, copy) })
	}
	d.takeQueued = func(child *MainPrint, s *cooperative.Scope) ([]protocol.JSONObject, error) {
		return nativeAgentOwnerCall(child, s, m, func(s *cooperative.Scope) ([]protocol.JSONObject, error) {
			return m.Controller.TakeNativeQueuedAttachmentsOnOwner(s, child.options.AgentID)
		})
	}
	d.pruneQueued = func(child *MainPrint, s *cooperative.Scope, id string) error {
		return bridgeAgentVoid(child, m, s, func(s *cooperative.Scope) error { return m.Controller.PruneAgentTaskNotificationsOnOwner(s, id) })
	}
	o := m.options
	o.DetachedAgent = d
	o.Native = m.options.Native.agentScopedBoundary(tree.CWD)
	o.AgentID = id
	o.AgentContext = nil
	o.QuerySource = "workflow_agent"
	o.OnlyToolNames = &resolved.ResolvedTools
	o.Arguments.SessionID = snapshot.SessionID
	o.Arguments.Resume = ""
	o.Arguments.ForkSession = false
	o.Arguments.Worktree = nil
	o.Arguments.Model = model
	o.Arguments.SystemPrompt = ""
	o.Arguments.HasCustomSystemPrompt = false
	cliBases := []string{}
	for _, name := range resolved.ResolvedTools {
		if name != "StructuredOutput" {
			cliBases = append(cliBases, name)
		}
	}
	o.Arguments.Tools = []string{strings.Join(cliBases, ",")}
	o.Arguments.AllowedTools = nil
	o.Arguments.PermissionMode = mode
	o.Arguments.MaxTurns = math.MaxInt32
	o.AgentEffort, o.AgentEffortOverridesEnv, e = m.Host.InferenceEffort(scope)
	if e != nil {
		return fail(e)
	}
	if selected.Effort != nil {
		o.AgentEffort = append(json.RawMessage(nil), selected.Effort...)
		o.AgentEffortOverridesEnv = true
	}
	if turns := selected.Fields.Get("maxTurns"); turns != nil {
		var n float64
		if json.Unmarshal(turns, &n) == nil && n <= math.MaxInt32 {
			o.Arguments.MaxTurns = int(n)
		}
	}
	o.Input = strings.NewReader("")
	o.Output = io.Discard
	o.OnOutput = func([]byte) error { return nil }
	o.OnCommandsChanged = nil
	o.OnPermissionModeChanged = nil
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return fail(e)
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return fail(e)
	}
	return nativeWorkflowAgentPrepared{o, request, id, tree, ref}, nil
}

const workflowStructuredAgentPrompt = "You are a subagent spawned by a workflow orchestration script. Use the tools available to complete the task.\n- After calling StructuredOutput successfully, end your turn. No acknowledgment needed."
const workflowStructuredNote = "\nNOTE: You are running inside a workflow script. You MUST return your final answer by calling the StructuredOutput tool exactly once — the tool's input schema defines the required shape. Do your work, then call StructuredOutput; do NOT put your answer in a text response (the script reads ONLY the tool call). If validation fails, read the error and call StructuredOutput again with a corrected shape."

func resolveNativeWorkflowAgent(definitions []agentcatalog.Definition, input protocol.JSONObject) (agentcatalog.Definition, error) {
	selected := agentcatalog.Definition{Name: "workflow-subagent", Source: "built-in", SystemPrompt: workflowAgentPrompt, Fields: protocol.NewJSONObject(protocol.JSONField{Name: "tools", Value: []byte(`["*"]`)}, protocol.JSONField{Name: "permissionMode", Value: quoteMain("acceptEdits")})}
	if input.Get("agentType") != nil {
		names := []string{}
		for _, d := range definitions {
			names = append(names, d.Name)
			if d.Name == input.String("agentType") {
				return d, nil
			}
		}
		return selected, fmt.Errorf("agent({agentType}): agent type '%s' not found. Available agents: %s", input.String("agentType"), strings.Join(names, ", "))
	}
	return selected, nil
}
