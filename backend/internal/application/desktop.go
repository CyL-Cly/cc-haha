package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopattachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type DesktopSession struct {
	outputGeneration         uint64
	runtimeTransition        chan struct{}
	planRestart              *desktopPlanRestart
	acceptedPlanApprovals    map[string]*Session
	canContinueRuntime       func() bool
	onRuntimeClosed          func(context.Context, server.LiveSession) error
	onBackgroundTaskStopped  func(string, bool) func()
	sdk                      *desktopSDKState
	autoQuestionUnbind       func()
	engine                   *Session
	emit                     server.Emit
	mu                       sync.Mutex
	projection               *Projection
	running                  bool
	foregroundPending        bool
	cliRunning               bool
	admission                sync.Mutex
	runtimeMu                sync.Mutex
	options                  Options
	ctx                      context.Context
	id                       string
	sideChat                 bool
	deferred                 *providerconfig.RuntimeSelection
	rejectedRuntime          string
	closed                   bool
	updateLaunchMetadata     func(context.Context, json.RawMessage) error
	onSDK                    func(json.RawMessage)
	collaborationStopEpoch   uint64
	resolveSessionReferences func(context.Context, json.RawMessage, json.RawMessage) (json.RawMessage, error)
}

func DesktopFactory(base Options) server.SessionFactory {
	return func(ctx context.Context, options server.SessionOptions, emit server.Emit) (server.LiveSession, error) {
		d := &DesktopSession{canContinueRuntime: options.CanContinueRuntime, onRuntimeClosed: options.OnRuntimeClosed, onBackgroundTaskStopped: options.OnBackgroundTaskStopped, emit: emit, projection: NewProjection(), ctx: ctx, id: options.ID, sideChat: options.SideChat != nil, updateLaunchMetadata: options.UpdateLaunchMetadata, onSDK: options.OnSDK, resolveSessionReferences: options.ResolveSessionReferences}
		o := base
		o.HookEvents = true
		// Original conversationService always launches its SDK with
		// --replay-user-messages; the ordinary public CLI defaults to false.
		o.Arguments.Replay = true
		o.Environment = make(map[string]string, len(base.Environment)+1)
		for key, value := range base.Environment {
			o.Environment[key] = value
		}
		for key, value := range options.Environment {
			o.Environment[key] = value
		}
		delete(o.Environment, "CC_HAHA_OPENAI_FAST_MODE")
		// Source keeps SDK auth identity for custom providers and stamps desktop
		// ownership separately. claude-desktop auth identity is official-only.
		o.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop"
		o.Environment["CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"] = "1"
		o.TeamWorker = options.TeamWorker
		if o.TeamWorker != nil {
			o.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop-team-worker"
		}
		// Original desktop SDK enables the real checkpoint producer for each
		// conversation. Tools and QueryEngine consume this same environment.
		o.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
		o.WorkDir = options.WorkDir
		o.Arguments.SessionID = options.ID
		if options.Resume {
			o.Arguments.Resume = options.ID
			o.Arguments.SessionID = ""
			if o.TeamWorker != nil {
				worker := *o.TeamWorker
				worker.ResumeSessionID = options.ID
				o.TeamWorker = &worker
			}
		}
		if side := options.SideChat; side != nil {
			o.Arguments.SessionID = side.CLISessionID
			o.Arguments.Resume = side.ResumePath
			o.Arguments.ResumeAt = side.ResumeAt
			o.Arguments.ForkSession = true
			o.Arguments.SessionPersistenceDisabled = true
			o.Arguments.AppendSystemPrompt = side.Boundary
			delete(o.Environment, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN")
			delete(o.Environment, "CC_HAHA_TRACE_API_CALLS")
			delete(o.Environment, "CLAUDE_CODE_DIAGNOSTICS_FILE")
		}
		if options.PermissionMode != "" {
			o.Arguments.PermissionMode = options.PermissionMode
		}
		meta := obj(options.Metadata)
		if raw := meta.Get("runtimeProviderId"); raw != nil {
			selection := &providerconfig.Selection{ModelID: meta.String("runtimeModelId"), EffortLevel: meta.String("effortLevel")}
			if string(raw) != "null" {
				var id string
				if err := json.Unmarshal(raw, &id); err != nil {
					return nil, err
				}
				selection.ProviderID = &id
			}
			var fast bool
			if value := meta.Get("openAIFastMode"); string(value) == "true" || string(value) == "false" {
				_ = json.Unmarshal(value, &fast)
				selection.OpenAIFastMode = &fast
			}
			o.RuntimeSelection = selection
			o.Arguments.EffortLevel = selection.EffortLevel
		}
		if model := meta.String("runtimeModelId"); model != "" {
			o.Arguments.Model = model
		}
		if o.ProviderOptions != nil && o.ProviderOptions.ClaudeRuntimeModel != nil && (o.RuntimeSelection == nil || o.RuntimeSelection.ProviderID == nil) {
			// A null subscription resolver result explicitly preserves the existing
			// API-key/PAYG path. It cannot synthesize a new null-provider override.
			active, e := o.ProviderOptions.ClaudeRuntimeModel(ctx, nil)
			if e != nil {
				return nil, e
			}
			if active != nil {
				selected, e := providerconfig.ResolveDesktopRuntimeSelection(ctx, *o.ProviderOptions, o.RuntimeSelection)
				if e != nil {
					return nil, e
				}
				o.desktopManagedClaudeOAuth = true
				o.RuntimeSelection = &selected
				o.Arguments.Model, o.Arguments.EffortLevel = selected.ModelID, selected.EffortLevel
			}
		}
		o.desktopTeamLaunch = &desktopTeamLaunch{model: meta.String("runtimeModelId"), effort: meta.String("effortLevel")}
		if o.RuntimeSelection != nil && o.RuntimeSelection.ProviderID != nil {
			id := *o.RuntimeSelection.ProviderID
			o.desktopTeamLaunch.provider = &id
		}
		var repositoryLaunch *server.RepositorySessionLaunch
		if value := meta.Get("repository"); len(value) > 0 && string(value) != "null" {
			if options.PrepareRepositoryLaunch == nil || options.FinalizeRepositoryLaunch == nil {
				return nil, &clibootstrap.MainCapabilityError{Boundary: "desktop repository launch preparation/persistence"}
			}
			prepared, err := options.PrepareRepositoryLaunch(ctx)
			if err != nil {
				return nil, err
			}
			repositoryLaunch = &prepared
			o.WorkDir = prepared.WorkDir
			if prepared.WorktreeName != "" {
				name := prepared.WorktreeName
				o.Arguments.Worktree = &worktree.Startup{Name: &name, BaseRef: prepared.BaseRef}
			}
		}
		var err error
		if options.SideChat == nil {
			o.Environment, err = desktopTraceEnvironment(ctx, o)
		}
		if err != nil {
			return nil, err
		}
		o.OnPermissionModeChanged = func(mode string) error {
			d.mu.Lock()
			d.options.Arguments.PermissionMode = mode
			d.mu.Unlock()
			if d.updateLaunchMetadata != nil {
				if err := d.updateLaunchMetadata(ctx, raw(map[string]any{"permissionMode": mode})); err != nil {
					return err
				}
			}
			d.emit(frame("permission_mode_changed", field("mode", mode)))
			return nil
		}
		o.OnCanonicalTranscriptMutation = options.OnCanonicalTranscriptMutation
		var engine *Session
		if o.TeamWorker != nil {
			o.RequestTeammateShutdown = func(ctx context.Context) error { return d.requestTeammateShutdown(ctx, engine) }
		}
		o.OnSDK = d.nextRuntimeOutput()
		o.OnCommandsChanged = func(commands []sdkhost.ControlCommand) error {
			d.emit(frame("system_notification", field("subtype", "slash_commands"), field("data", server.NormalizeSessionSlashCommands(raw(commands)))))
			return nil
		}
		engine, err = NewSession(ctx, o)
		if err != nil {
			return nil, err
		}
		if repositoryLaunch != nil {
			cwd, err := engine.process.WorkingDirectory(ctx)
			if err == nil {
				err = options.FinalizeRepositoryLaunch(ctx, cwd, repositoryLaunch.Repository)
			}
			if err != nil {
				_ = engine.Close(ctx)
				return nil, err
			}
			o.WorkDir = cwd
		}
		d.engine = engine
		d.options = o
		if err = d.bindAutoQuestions(); err != nil {
			_ = engine.Close(ctx)
			return nil, err
		}
		return d, nil
	}
}

func (d *DesktopSession) output(raw json.RawMessage) error {
	return d.outputForGeneration(raw, 0)
}
func (d *DesktopSession) outputForGeneration(raw json.RawMessage, generation uint64) error {
	d.mu.Lock()
	if generation != 0 && (d.closed || d.outputGeneration != generation) {
		terminal := obj(raw)
		ownedTerminal := d.closed && d.outputGeneration == generation && terminal.String("type") == "system" && terminal.String("subtype") == "task_notification"
		status := terminal.String("status")
		ownedTerminal = ownedTerminal && (status == "completed" || status == "failed" || status == "stopped" || status == "killed")
		onSDK := d.onSDK
		d.mu.Unlock()
		if ownedTerminal && onSDK != nil {
			onSDK(append(json.RawMessage(nil), raw...))
		}
		return nil
	}
	if state := obj(raw); state.String("type") == "result" && d.planRestart != nil && d.planRestart.engine == d.engine {
		select {
		case <-d.planRestart.result:
		default:
			close(d.planRestart.result)
		}
	}
	if state := obj(raw); state.String("type") == "result" {
		// The result settles the registered user turn before its network frame
		// is sent. CLI/background cleanup can continue after this boundary.
		d.foregroundPending = false
	}
	if state := obj(raw); state.String("type") == "system" && state.String("subtype") == "session_state_changed" {
		if value := state.String("state"); value == "running" || value == "idle" {
			d.cliRunning = value == "running"
		}
	}
	frames, err := d.projection.Translate(raw)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	for _, f := range frames {
		d.emit(f)
	}
	if d.onSDK != nil {
		d.onSDK(append(json.RawMessage(nil), raw...))
	}
	return nil
}
func (d *DesktopSession) Handle(ctx context.Context, data json.RawMessage) error {
	m := obj(data)
	switch m.String("type") {
	case "user_message":
		if err := ctx.Err(); err != nil {
			return err
		}
		d.mu.Lock()
		restart := d.planRestart
		d.mu.Unlock()
		if pending, ok := ctx.Value(planPendingInputKey{}).(*desktopPlanRestart); ok {
			restart = pending
		}
		if restart != nil && ctx.Value(planContinueProducerKey{}) != restart {
			select {
			case <-restart.finished:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			d.mu.Lock()
			superseded := restart.continued
			d.mu.Unlock()
			// Original handleUserMessage waits for the runtime transition. Its
			// still-unsent input is superseded by the new automatic active turn.
			if superseded {
				return nil
			}
		}
		if err := d.stopSDKTransport(false); err != nil {
			return err
		}
		d.admission.Lock()
		defer d.admission.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		content := m.String("content")
		if m.Get("sessionReferences") != nil {
			if d.resolveSessionReferences == nil && string(m.Get("sessionReferences")) != "[]" {
				return &clibootstrap.MainCapabilityError{Boundary: "desktop session reference context without original session resolver"}
			}
			if d.resolveSessionReferences != nil {
				resolved, e := d.resolveSessionReferences(ctx, m.Get("content"), m.Get("sessionReferences"))
				if e != nil {
					return e
				}
				_ = json.Unmarshal(resolved, &content)
			}
		}
		native := d.currentEngine().native
		blocks, materializeErr := desktopattachments.Build(content, d.id, m.Get("attachments"), desktopattachments.Options{HomeDir: native.Home, ConfigDir: native.Environment["CLAUDE_CONFIG_DIR"], NewID: newID, ImageProcessor: normalizeDesktopImage})
		if materializeErr != nil {
			if errors.Is(materializeErr, desktopattachments.ErrImageProducer) {
				return &clibootstrap.MainCapabilityError{Boundary: "desktop image attachment normalization producer"}
			}
			return materializeErr
		}
		d.runtimeMu.Lock()
		if err := ctx.Err(); err != nil {
			d.runtimeMu.Unlock()
			return err
		}
		d.mu.Lock()
		rejected := d.rejectedRuntime
		closed := d.closed
		d.mu.Unlock()
		if closed {
			d.runtimeMu.Unlock()
			return context.Canceled
		}
		if rejected != "" {
			d.runtimeMu.Unlock()
			d.emit(frame("error", field("code", "USER_TURN_FAILED"), field("message", "The message was not sent. "+rejected+" Select a valid model and reasoning effort, then retry."), field("retryable", true)))
			return nil
		}
		d.mu.Lock()
		d.running, d.foregroundPending = true, true
		d.projection.Stopped = false
		d.mu.Unlock()
		d.runtimeMu.Unlock()
		// Original user admission emits status. session_state is authoritative
		// reconnect evidence: sending a running snapshot for every new turn makes
		// React discard the preceding unpersisted side-chat answer as stale.
		d.emit(frame("status", field("state", "thinking"), field("verb", "Thinking")))
		engine := d.currentEngine()
		err := engine.submitContent(ctx, tui.Input{TurnID: newID(), Text: content}, d.events, blocks)
		d.mu.Lock()
		planInterrupted := d.planRestart != nil && d.planRestart.engine == engine
		if planInterrupted {
			select {
			case <-d.planRestart.returned:
			default:
				close(d.planRestart.returned)
			}
		}
		d.running, d.foregroundPending = false, false
		d.mu.Unlock()
		if planInterrupted && errors.Is(err, context.Canceled) {
			err = nil
		}
		// Source publishes session_state only in response to sync_state. A
		// normal result already projected message_complete and replayed pending
		// teammate approvals; an unsolicited idle snapshot clears those prompts
		// again and triggers the renderer's terminal-history replacement.
		if restartErr := d.applyDeferredRuntime(ctx); err == nil {
			err = restartErr
		}
		return err
	case "permission_response":
		state := d.sdkRuntime()
		state.mu.Lock()
		automatic := state.autoResolved[m.String("requestId")]
		state.mu.Unlock()
		if automatic {
			return nil
		}
		d.removeAutoQuestion(m.String("requestId"))
		if handled, err := d.respondSDKPermission(m); handled {
			return err
		}
		toolName := ""
		engine := d.currentEngine()
		for _, pending := range engine.PendingPermissions() {
			if pending.ID == m.String("requestId") {
				toolName = pending.ToolName
				break
			}
		}
		if toolName == "" {
			d.mu.Lock()
			accepted := d.acceptedPlanApprovals[m.String("requestId")] != nil
			accepted = accepted || d.planRestart != nil && d.planRestart.requestID == m.String("requestId")
			d.mu.Unlock()
			if accepted {
				return nil
			}
		}
		if string(m.Get("allowed")) == "true" && sourceSDKTruthy(m.Get("runtimeOverride")) && toolName == "ExitPlanMode" {
			return d.approvePlanRuntime(ctx, m, engine)
		}
		decision, err := desktopPermissionDecision(m, toolName)
		if err != nil {
			return err
		}
		decision.PermissionUpdates = m.Get("permissionUpdates")
		return engine.RespondPermission(ctx, m.String("requestId"), decision)
	case "stop_generation":
		if err := d.stopSDKTransport(true); err != nil {
			return err
		}
		d.mu.Lock()
		d.projection.Stopped = true
		d.collaborationStopEpoch++
		d.mu.Unlock()
		engine := d.currentEngine()
		if err := engine.process.CancelSessionMessages(ctx); err != nil {
			return err
		}
		engine.Cancel()
		return engine.process.StopAllBackgroundTasks(ctx)
	case "prewarm_session":
		return nil // The factory already initialized this real shared host.
	case "set_permission_mode":
		d.runtimeMu.Lock()
		defer d.runtimeMu.Unlock()
		mode := m.String("mode")
		if err := d.currentEngine().process.SetSessionPermissionMode(ctx, mode); err != nil {
			return err
		}
		if d.updateLaunchMetadata != nil {
			if err := d.updateLaunchMetadata(ctx, raw(map[string]any{"permissionMode": mode})); err != nil {
				return err
			}
		}
		d.mu.Lock()
		d.options.Arguments.PermissionMode = mode
		d.mu.Unlock()
		d.emit(frame("permission_mode_changed", field("mode", mode)))
		return nil
	case "set_runtime_config":
		return d.setRuntime(ctx, m)
	case "stop_background_task":
		return d.stopBackgroundTask(ctx, m.String("taskId"))
	case "ask_user_question_activity":
		d.cancelAutoQuestion(m.String("requestId"))
		return nil
	case "computer_use_permission_response":
		return &clibootstrap.MainCapabilityError{Boundary: "desktop control " + m.String("type")}
	default:
		return errors.New("unknown desktop session message")
	}
}
func (d *DesktopSession) events(event tui.Event) error {
	switch event.Kind {
	case tui.PermissionAsked:
		p := event.Permission
		if p == nil {
			return errors.New("missing permission request")
		}
		fields := []protocol.JSONField{field("requestId", p.ID), field("toolName", p.ToolName), {Name: "input", Value: p.Input}}
		for _, name := range []string{"toolUseId", "agentId", "displayName", "description", "permissionSuggestions", "decisionReason"} {
			if value := obj(p.Payload).Get(name); value != nil {
				fields = append(fields, protocol.JSONField{Name: name, Value: value})
			}
		}
		d.emit(frame("permission_request", fields...))
		d.trackAutoQuestion(p.ID, p.ToolName, obj(p.Payload).String("agentId"), p.Input)
	case tui.PermissionClosed:
		if p := event.Permission; p != nil {
			d.removeAutoQuestion(p.ID)
			d.emit(frame("permission_resolved", field("requestId", p.ID), field("permissionType", "tool"), protocol.JSONField{Name: "allowed", Value: obj(p.Payload).Get("allowed")}))
		}
	}
	return nil
}
func (d *DesktopSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	d.mu.Lock()
	running := d.foregroundPending && !d.projection.Stopped
	d.mu.Unlock()
	state := "idle"
	if running {
		state = "running"
	}
	out := []json.RawMessage{frame("session_state", field("turnState", state))}
	sdkPermissions := d.sdkPermissionSnapshot()
	out = append(out, sdkPermissions...)
	ids := []string{}
	for _, permission := range sdkPermissions {
		ids = append(ids, obj(permission).String("requestId"))
	}
	for _, p := range d.currentEngine().PendingPermissions() {
		ids = append(ids, p.ID)
		fields := []protocol.JSONField{field("requestId", p.ID), field("toolName", p.ToolName), {Name: "input", Value: p.Input}}
		for _, name := range []string{"toolUseId", "agentId", "displayName", "description", "permissionSuggestions", "decisionReason"} {
			if value := obj(p.Payload).Get(name); value != nil {
				fields = append(fields, protocol.JSONField{Name: name, Value: value})
			}
		}
		out = append(out, frame("permission_request", fields...))
	}
	out = append(out, frame("permission_requests_snapshot", field("toolRequestIds", ids), field("computerUseRequestIds", []string{}), field("turnActive", running)))
	return out, nil
}
func (d *DesktopSession) currentEngine() *Session { d.mu.Lock(); defer d.mu.Unlock(); return d.engine }

func (d *DesktopSession) ControlComponents(ctx context.Context, subtype, serverName string) (protocol.JSONObject, error) {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	d.mu.Lock()
	closed, engine := d.closed, d.engine
	d.mu.Unlock()
	if closed {
		return protocol.JSONObject{}, context.Canceled
	}
	if subtype == "reload_plugins" {
		response, err := engine.process.ReloadComponents(ctx)
		if err == nil {
			d.emit(frame("system_notification", field("subtype", "slash_commands"), field("data", server.NormalizeSessionSlashCommands(response.Get("commands")))))
		}
		return response, err
	}
	if subtype == "mcp_status" || subtype == "mcp_reconnect" || subtype == "mcp_toggle" {
		return engine.process.MCPControl(ctx, subtype, serverName)
	}
	return protocol.JSONObject{}, &clibootstrap.MainCapabilityError{Boundary: "component control " + subtype}
}
func (d *DesktopSession) Close(ctx context.Context) error {
	return d.closeDesktopEngine(ctx, nil)
}
