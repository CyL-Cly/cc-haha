// Package application binds the CLI and desktop transports to the shared Go
// Controller/ASK/query loop. It does not implement another provider consumer.
package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

type Options struct {
	publicPrintLoadErrors         bool
	AnalyticsHTTP                 func(context.Context, analytics.Request) (analytics.Response, error)
	Interactive                   bool
	desktopTeamLaunch             *desktopTeamLaunch
	desktopManagedClaudeOAuth     bool
	DesktopTeamsEnabled           func(context.Context) (bool, error)
	HookEvents                    bool
	TeamWorker                    *teamworker.Start
	PaneTeammate                  *teamspawn.PaneIdentity
	RequestTeammateShutdown       func(context.Context) error
	Trace                         *tracecapture.Service
	Arguments                     clibootstrap.PrintArguments
	Environment                   map[string]string
	WorkDir, Version, Platform    string
	Stderr                        io.Writer
	Native                        *clibootstrap.NativeBoundary
	NativeShells                  *windowshell.Resolver
	OnSDK                         func(json.RawMessage) error
	OnCanonicalTranscriptMutation func(string) error
	OnCommandsChanged             func([]sdkhost.ControlCommand) error
	OnPermissionModeChanged       func(string) error
	Store                         *storage.Store
	RuntimeSelection              *providerconfig.Selection
	NativeFactory                 NativeFactory
	ProviderOptions               *providerconfig.Options
	ComputerUse                   *clibootstrap.NativeComputerUseOptions
	NonInteractivePermissions     bool
	ForegroundShellProgress       bool
	SDKRecoverOfficialOAuth       func(context.Context, string) (*string, error)
	DesktopOAuthToken             func(context.Context) (*string, error)
	DesktopOAuthAccountUUID       func(context.Context, string) (*string, error)
	AutoQuestion                  *autoquestion.Service
	AutoQuestionContext           func(context.Context, string, string) (json.RawMessage, error)
	AutoQuestionProvider          func(context.Context, string) (json.RawMessage, error)
}

// NativeFactory replaces external primitives in an isolated build; it cannot
// replace the application, tool catalog or provider consumer.
type NativeFactory func(map[string]string, string, string) (*clibootstrap.NativeBoundary, error)

type pendingPermission struct {
	request tui.PermissionRequest
	future  *sdkhost.OwnerFuture[tui.PermissionDecision]
	ordinal uint64
}

type Session struct {
	autoConsentUI                *terminalAutoConsentUI
	terminalInteractive          bool
	terminalPlatform             string
	desktopNetwork               *desktopNetworkState
	desktopOAuthToken            func(context.Context) (*string, error)
	desktopOfficialToken         string
	desktopOAuthError            io.Writer
	initialTUIRecords            []protocol.JSONObject
	initialTUIEnvironment        map[string]string
	leaderInbox                  *sessionLeaderInbox
	leaderInboxError             io.Writer
	teammateEventSink            *teammateEventSink
	closeDone                    chan struct{}
	closeError                   error
	worktreeExit                 *clibootstrap.WorktreeExitSnapshot
	processPermissionModeChanged func(string) error
	process                      *clibootstrap.MainPrint
	teammateShutdownDone         chan struct{}
	teammateShutdownError        error
	mu                           sync.Mutex
	turnMu                       sync.Mutex
	cancel                       context.CancelFunc
	interrupt                    context.CancelCauseFunc
	closed                       bool
	turnID                       string
	emit                         tui.Emit
	pending                      map[string]*pendingPermission
	teamPaneSetupDisplay         *tui.PermissionRequest
	onSDK                        func(json.RawMessage) error
	projection                   *Projection
	permissionOrdinal            uint64
	native                       *clibootstrap.NativeBoundary
	modelPicker                  *pendingModelPicker
	thinkingPicker               string
	terminalPermission           *terminalPermissionState
	commandSnapshot              []tui.Completion
	commandListeners             map[uint64]func([]tui.Completion)
	commandListenerID            uint64
	environmentNoticeID          uint64
	environmentNotices           map[uint64]func(tui.Event)
	onCommandsChanged            func([]sdkhost.ControlCommand) error
}

func NewSession(ctx context.Context, o Options) (*Session, error) {
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.Version == "" {
		return nil, errors.New("application requires its release version")
	}
	if o.desktopTeamLaunch != nil && o.ProviderOptions != nil && o.ProviderOptions.ClaudeRuntimeModel != nil {
		// Restarts can cross between a saved provider and null official runtime.
		// Re-evaluate only the actual Claude subscription producer at that boundary.
		o.desktopManagedClaudeOAuth = false
		if o.RuntimeSelection != nil && o.RuntimeSelection.ProviderID == nil {
			active, err := o.ProviderOptions.ClaudeRuntimeModel(ctx, nil)
			if err != nil {
				return nil, err
			}
			if active != nil {
				selected, err := providerconfig.ResolveDesktopRuntimeSelection(ctx, *o.ProviderOptions, o.RuntimeSelection)
				if err != nil {
					return nil, err
				}
				o.desktopManagedClaudeOAuth = true
				o.Arguments.Model = selected.ModelID
			}
		}
	}
	native := o.Native
	configDirExplicit := o.Environment["CLAUDE_CONFIG_DIR"] != ""
	var err error
	if native == nil {
		store := o.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		proxyOptions := []providerconfig.ManagedEnvironmentOptions{}
		if o.ProviderOptions != nil {
			proxyOptions = append(proxyOptions, providerconfig.ManagedEnvironmentOptions{ServerPort: o.ProviderOptions.ServerPort, ProxyReady: o.ProviderOptions.ProxyReady, GrokRuntimeModelCatalog: o.ProviderOptions.GrokRuntimeModelCatalog})
		}
		o.Environment = desktopOAuthBaseEnvironment(o, o.Environment)
		o.Environment, err = ResolveProcessEnvironment(ctx, store, o.Environment, o.RuntimeSelection, proxyOptions...)
		if err != nil {
			return nil, err
		}
		o.Environment = desktopOAuthEnvironment(ctx, o, o.Environment)
		o.Environment, err = desktopTeamEnvironment(ctx, o, o.Environment)
		if err != nil {
			return nil, err
		}
		if o.TeamWorker != nil {
			o.Environment = teamworker.Environment(o.Environment, *o.TeamWorker)
		}
		if o.NativeFactory != nil {
			native, err = o.NativeFactory(o.Environment, o.WorkDir, o.Platform)
		} else {
			native, err = clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: o.WorkDir, Platform: o.Platform})
		}
		if err != nil {
			return nil, err
		}
		native.ConfigDirExplicit = configDirExplicit
	} else if o.desktopTeamLaunch != nil {
		environment, err := desktopTeamEnvironment(ctx, o, native.Environment)
		if err != nil {
			return nil, err
		}
		native.Environment = environment
	}
	networkState, err := initializeDesktopNetwork(ctx, o, native)
	if err != nil {
		return nil, err
	}
	s := &Session{desktopNetwork: networkState, pending: make(map[string]*pendingPermission), onSDK: o.OnSDK, native: native, processPermissionModeChanged: o.OnPermissionModeChanged, onCommandsChanged: o.OnCommandsChanged}
	s.terminalInteractive = o.Interactive
	s.terminalPlatform = o.Platform
	s.leaderInboxError = o.Stderr
	if o.DesktopOAuthToken != nil && o.desktopTeamLaunch != nil && native.Environment["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop" {
		s.desktopOAuthToken, s.desktopOfficialToken, s.desktopOAuthError = o.DesktopOAuthToken, native.Environment["CLAUDE_CODE_OAUTH_TOKEN"], o.Stderr
	}
	var leaderReady func() error
	if o.Interactive || o.OnSDK != nil {
		leaderReady = s.startLeaderInbox
	}
	resolvePermission := s.requestPermission
	startSandboxNetworkPermission := s.startSandboxNetworkPermission
	var paneSetup func(context.Context, *cooperative.Scope, teamspawn.PaneSetupPrompt) (string, error)
	if o.Interactive {
		paneSetup = s.requestTeamPaneSetup
	}
	if o.NonInteractivePermissions {
		resolvePermission = nil
		startSandboxNetworkPermission = nil
	}
	shutdown := o.RequestTeammateShutdown
	if (o.TeamWorker != nil || o.PaneTeammate != nil) && shutdown == nil {
		shutdown = s.requestTeammateShutdown
	}
	s.process, err = clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{NativeShells: o.NativeShells, NativeDesktopNetworkBound: networkState != nil, PublicPrintLoadErrors: o.publicPrintLoadErrors, AnalyticsHTTP: o.AnalyticsHTTP, TeamPaneSetup: paneSetup, Interactive: o.Interactive, OnInteractiveTeamTurn: s.interactiveTeamTurn, OnLeaderInboxReady: leaderReady, OnEnvironmentHookMessage: s.environmentHookNotice, OnLocalInputMessages: s.localInputMessages, OnNativeFastModeNotification: s.nativeFastModeNotification, HookEvents: o.HookEvents, TeamWorker: o.TeamWorker, PaneTeammate: o.PaneTeammate, RequestTeammateShutdown: shutdown, ForegroundShellProgress: o.ForegroundShellProgress, Trace: o.Trace, OnCanonicalTranscriptMutation: o.OnCanonicalTranscriptMutation, OnQueryYield: s.nativeQueryYield, OnPermissionModeChanged: func(_ context.Context, mode string) error {
		return s.permissionModeChanged(mode)
	}, OnCommandsChanged: s.commandsChanged, ComputerUse: o.ComputerUse, Arguments: o.Arguments, Native: native, PID: os.Getpid(), Version: o.Version, Platform: o.Platform, Input: strings.NewReader(""), Output: io.Discard, Error: o.Stderr, OnOutput: s.output, StartSandboxNetworkPermission: startSandboxNetworkPermission, ResolvePermission: resolvePermission})
	if err != nil {
		return nil, err
	}
	if err = s.process.StartSession(ctx); err != nil {
		s.process.Close()
		return nil, err
	}
	if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if o.Interactive {
			if err := s.captureInitialTUIHistoryOnOwner(scope); err != nil {
				return err
			}
		}
		return s.setCommands(s.process.Registry.ControlCommands(), false)
	}); err != nil {
		_ = s.process.ShutdownSession(context.Background())
		return nil, err
	}
	mode, err := s.process.SessionPermissionMode(ctx)
	if err != nil {
		_ = s.process.ShutdownSession(context.Background())
		return nil, err
	}
	s.terminalPermission = &terminalPermissionState{mode: mode}
	if o.Interactive && o.TeamWorker == nil {
		if err := s.startInteractiveLeaderInbox(); err != nil {
			_ = s.Close(context.Background())
			return nil, err
		}
	}
	return s, nil
}

func (s *Session) hasConversation(ctx context.Context) (bool, error) {
	var found bool
	err := s.process.Owner.Run(ctx, func(*cooperative.Scope) error {
		for _, m := range s.process.Controller.History() {
			if m.String("type") == "user" || m.String("type") == "assistant" {
				found = true
				break
			}
		}
		return nil
	})
	return found, err
}

// Submit returns only after the original ASK's finally/checkpoint has returned.
// A streamed result is a display event, never permission to begin another ASK.
func (s *Session) Submit(ctx context.Context, in tui.Input, emit tui.Emit) error {
	return s.submitContent(ctx, in, emit, nil)
}

// The desktop attachment producer retains original content blocks while using
// the same serial ASK, permission projection and cancellation as ordinary TUI.
func (s *Session) submitContent(ctx context.Context, in tui.Input, emit tui.Emit, content json.RawMessage) error {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	if len(in.Attachments) > 0 {
		s.mu.Unlock()
		return &clibootstrap.MainCapabilityError{Boundary: "interactive attachment producer"}
	}
	for _, pasted := range in.PastedContents {
		if pasted.Type != "text" {
			s.mu.Unlock()
			return &clibootstrap.MainCapabilityError{Boundary: "interactive pasted attachment producer " + pasted.Type}
		}
	}
	modelContext, interrupt := context.WithCancelCause(ctx)
	cancel := func() { interrupt(context.Canceled) }
	s.cancel, s.turnID, s.emit = cancel, in.TurnID, emit
	s.interrupt = interrupt
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.interrupt = nil
		s.emit = nil
		s.mu.Unlock()
	}()
	if in.Mode == "bash" {
		return s.submitTerminalBash(modelContext, in)
	}
	if in.Mode != "" && in.Mode != "prompt" {
		return &clibootstrap.MainCapabilityError{Boundary: "interactive input mode " + in.Mode}
	}
	if in.Mode == "prompt" && emit != nil && !s.process.SlashCommandsDisabled() {
		if command := tui.ParseSlashCommand(in.Text); command != nil && command.CommandName == "doctor" {
			return s.InvokeDoctor(modelContext, emit)
		}
		if command := tui.ParseSlashCommand(in.Text); command != nil && command.CommandName == "ide" {
			return s.InvokeIDEPicker(modelContext, command.Args, emit)
		}
		if command := tui.ParseSlashCommand(in.Text); command != nil && command.CommandName == "model" {
			if command.Args != "" {
				result, err := s.process.NativeModelCommand(modelContext, command.Args)
				if err != nil {
					return err
				}
				return emit(tui.Event{Kind: tui.Notice, Text: result.Notice})
			}
			return s.InvokeModelPicker(modelContext, true, emit)
		}
	}
	var messageContent any = in.Text
	if err := s.process.SetNativeWorkflowKeywordSuppression(modelContext, in.SuppressWorkflowKeyword); err != nil {
		return err
	}
	if content != nil {
		messageContent = content
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "session_id", Value: raw(s.process.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: raw(map[string]any{"role": "user", "content": messageContent})})
	if err := s.refreshDesktopNetwork(modelContext); err != nil {
		return err
	}
	if err := s.refreshDesktopOAuth(modelContext); err != nil {
		return err
	}
	if err := s.emitNativeSpinner(modelContext, in.TurnID, emit); err != nil {
		return err
	}
	return s.process.RunTurn(modelContext, user)
}

func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SubmitSDK admits the original structured user envelope to the same serial
// ASK used by desktop and terminal input. Scheduled runs retain content blocks
// and UUIDs; they do not reconstruct a second text-only conversation.
func (s *Session) SubmitSDK(ctx context.Context, input protocol.JSONObject) error {
	return s.submitSDKEvents(ctx, input, nil)
}
func (s *Session) submitSDKEvents(ctx context.Context, input protocol.JSONObject, emit tui.Emit) error {
	admitted := false
	if input.String("priority") == "now" {
		var err error
		admitted, err = s.process.AdmitNativeSDKInput(ctx, input, func() {
			s.mu.Lock()
			interrupt := s.interrupt
			s.mu.Unlock()
			if interrupt != nil {
				interrupt(queryloop.SubmitInterrupt)
			}
		})
		if err != nil || !admitted {
			return err
		}
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	modelContext, interrupt := context.WithCancelCause(ctx)
	cancel := func() { interrupt(context.Canceled) }
	s.cancel, s.turnID, s.emit = cancel, "", emit
	s.interrupt = interrupt
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.interrupt = nil
		s.emit = nil
		s.mu.Unlock()
	}()
	if err := s.refreshDesktopNetwork(modelContext); err != nil {
		return err
	}
	if err := s.refreshDesktopOAuth(modelContext); err != nil {
		return err
	}
	if admitted {
		return s.process.RunAdmittedSDKTurn(modelContext)
	}
	return s.process.RunTurn(modelContext, input)
}

func (s *Session) Close(ctx context.Context) error {
	return s.closeWithReason(ctx, "other")
}

func (s *Session) closeWithReason(ctx context.Context, reason string) (err error) {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		if done == nil {
			return nil
		}
		select {
		case <-done:
			s.mu.Lock()
			err = s.closeError
			s.mu.Unlock()
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	s.closed = true
	if s.autoConsentUI != nil && s.autoConsentUI.timer != nil {
		s.autoConsentUI.timer.Stop()
	}
	s.closeDone = make(chan struct{})
	defer func() { s.mu.Lock(); s.closeError = err; close(s.closeDone); s.mu.Unlock() }()
	s.teammateEventSink = nil
	s.commandListeners = nil
	s.environmentNotices = nil
	cancel := s.cancel
	leader := s.leaderInbox
	s.mu.Unlock()
	s.process.BeginNativePDFRuntimeClose()
	s.process.BeginNativeRemoteRuntimeClose()
	if leader != nil {
		leader.cancel()
	}
	if cancel != nil {
		cancel()
	}
	if err = s.cancelSessionPermissions(ctx); err != nil {
		return err
	}
	if err := s.joinLeaderInbox(ctx, leader); err != nil {
		return err
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.process.ShutdownSessionWithReason(ctx, reason)
}

func (s *Session) requestPermission(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	future, err := sdkhost.NewOwnerFuture[tui.PermissionDecision](s.process.Owner)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	id := newID()
	in, err := input.MarshalJSON()
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "toolUseId", Value: call.ToolUseID})
	if decision := call.PermDecision; decision != nil {
		for _, value := range []struct {
			name string
			raw  json.RawMessage
		}{{"description", decision.Description}, {"displayName", decision.DisplayName}, {"permissionSuggestions", decision.Suggestions}, {"decisionReason", decision.DecisionReason}, {"metadata", decision.Metadata}} {
			if value.raw != nil {
				payload.Set(value.name, value.raw)
			}
		}
		if payload.Get("description") == nil && decision.Message != nil {
			payload.Set("description", decision.Message)
		}
	}
	if actual, ok := call.Opaque.(*builtinwrite.CallContext); ok && actual != nil && actual.AgentID != "" {
		payload.Set("agentId", raw(actual.AgentID))
	}
	_, permissions, err := s.process.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	request := tui.PermissionRequest{ID: id, ToolName: tool.Name, Input: in, Payload: raw(payload), PermissionMode: permissions.String("mode"), Description: payload.String("description")}
	if err = s.prepareTerminalPermissionChoices(scope, &request); err != nil {
		return queryloop.ToolPermission{}, err
	}
	s.mu.Lock()
	s.permissionOrdinal++
	s.pending[id] = &pendingPermission{request: request, future: future, ordinal: s.permissionOrdinal}
	s.mu.Unlock()
	allowed := false
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		closed := request
		payload := obj(request.Payload)
		payload.Set("allowed", raw(allowed))
		closed.Payload = payloadJSON(payload)
		_ = s.emitEvent(tui.Event{Kind: tui.PermissionClosed, Permission: &closed, Cancelled: ctx.Err() != nil})
	}()
	if err = s.emitEvent(tui.Event{Kind: tui.PermissionAsked, Permission: &request, ToolName: tool.Name}); err != nil {
		return queryloop.ToolPermission{}, err
	}
	settled := make(chan struct{})
	defer close(settled)
	go func() {
		select {
		case <-ctx.Done():
			_, _ = s.process.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error { return future.Reject(next, context.Cause(ctx)) })
		case <-settled:
		}
	}()
	decision, err := future.Await(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	selected := queryloop.ToolPermission{Behavior: "deny", Input: input, Message: raw("The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.")}
	if decision.Allowed && decision.PermissionUpdates != nil {
		before, e := s.process.Host.NativePermissionContext(scope)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		if e := s.process.Host.ApplyNativePermissionUpdates(scope, decision.PermissionUpdates); e != nil {
			return queryloop.ToolPermission{}, e
		}
		after, e := s.process.Host.NativePermissionContext(scope)
		if e != nil {
			return queryloop.ToolPermission{}, e
		}
		if before.String("mode") != after.String("mode") && s.processPermissionModeChanged != nil {
			if e := s.processPermissionModeChanged(after.String("mode")); e != nil {
				return queryloop.ToolPermission{}, e
			}
		}
	}
	if decision.Allowed {
		allowed = true
		selected.Behavior = "allow"
		selected.UpdatedPermissions = append(json.RawMessage(nil), decision.PermissionUpdates...)
	}
	if decision.UpdatedInput != nil {
		var updated protocol.JSONObject
		if err = updated.UnmarshalJSON(decision.UpdatedInput); err != nil || !updated.IsObject() {
			return queryloop.ToolPermission{}, errors.New("permission updatedInput must be an object")
		}
		selected.UpdatedInput = &updated
		selected.UserModified = true
	}
	if decision.Message != nil {
		selected.Message = append(json.RawMessage(nil), decision.Message...)
	}
	selected.AcceptFeedback = append(json.RawMessage(nil), decision.AcceptFeedback...)
	for _, block := range decision.ContentBlocks {
		selected.ContentBlocks = append(selected.ContentBlocks, append(json.RawMessage(nil), block...))
	}
	if !decision.Allowed && decision.Behavior == "ask" {
		selected.Behavior = "ask"
	}
	return selected, nil
}

func (s *Session) RespondPermission(ctx context.Context, id string, decision tui.PermissionDecision) error {
	if handled, err := s.respondTerminalAutoConsent(ctx, id, decision); handled {
		return err
	}
	s.mu.Lock()
	pending := s.pending[id]
	s.mu.Unlock()
	if pending == nil {
		return errors.New("permission request is no longer pending")
	}
	if err := terminalEditedPermissionUpdates(pending.request, &decision); err != nil {
		return err
	}
	var explicit []json.RawMessage
	var rule string
	_ = json.Unmarshal(decision.Rule, &rule)
	if decision.Allowed && (json.Unmarshal(decision.PermissionUpdates, &explicit) != nil || len(explicit) == 0) && rule == "always" {
		decision.PermissionUpdates = normalizeSessionPermissionUpdates(obj(pending.request.Payload).Get("permissionSuggestions"), pending.request.ToolName)
	}
	receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return pending.future.Resolve(scope, decision) })
	if err != nil {
		return err
	}
	return receipt.Wait()
}

func (s *Session) emitEvent(event tui.Event) error {
	s.mu.Lock()
	emit, turn := s.emit, s.turnID
	if emit == nil && s.teammateEventSink != nil {
		emit = s.teammateEventSink.emit
	}
	s.mu.Unlock()
	event.TurnID = turn
	event.Background = strings.HasPrefix(turn, "teammate-inbox-")
	if event.Kind == tui.PermissionAsked || event.Kind == tui.PermissionClosed {
		event.TurnID = ""
	}
	if emit != nil {
		return emit(event)
	}
	return nil
}

func (s *Session) PendingPermissions() []tui.PermissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	ordered := make([]*pendingPermission, 0, len(s.pending))
	for _, p := range s.pending {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ordinal < ordered[j].ordinal })
	out := make([]tui.PermissionRequest, 0, len(ordered))
	for _, p := range ordered {
		request := p.request
		request.Input = append(json.RawMessage(nil), request.Input...)
		request.Payload = append(json.RawMessage(nil), request.Payload...)
		out = append(out, request)
	}
	return out
}

func payloadJSON(value protocol.JSONObject) json.RawMessage {
	data, _ := value.MarshalJSON()
	return data
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}
