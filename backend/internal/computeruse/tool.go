package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Extra fields preserve the Source binder's host-to-language-worker metadata.
// The MCP boundary removes telemetry before delivering a model result.
type NativeToolResult struct {
	Content                 []ToolContent     `json:"content"`
	IsError                 bool              `json:"isError,omitempty"`
	StructuredContent       map[string]any    `json:"structuredContent,omitempty"`
	Telemetry               map[string]string `json:"telemetry,omitempty"`
	App                     string            `json:"app,omitempty"`
	Apps                    json.RawMessage   `json:"apps,omitempty"`
	NativeError             map[string]any    `json:"nativeError,omitempty"`
	NativeCallNotDispatched bool              `json:"nativeCallNotDispatched,omitempty"`
}
type NativeToolsOptions struct {
	Service          *Service
	SessionID        string
	CurrentSessionID func() string
	NativeCall       func(context.Context, string, json.RawMessage) (json.RawMessage, error)
	PermissionCard   func(context.Context)
	Now              func() time.Time
	// The real CLI wrapper grants all feature flags after the one consent.
	// Alternative host contexts may provide their existing flag policy.
	GrantFlags     func(context.Context) (json.RawMessage, error)
	FormatLockHeld func(string) string
	OnNotification func(context.Context, string, string)
}
type NativeTools struct {
	options      NativeToolsOptions
	ctx          context.Context
	cancel       context.CancelFunc
	queueMu      sync.Mutex
	tail         chan struct{}
	delivered    map[string]bool
	lockHeld     bool
	cardMu       sync.Mutex
	cardInFlight bool
	lastCard     time.Time
}

func NewNativeTools(options NativeToolsOptions) (*NativeTools, error) {
	if options.Service == nil || options.SessionID == "" && options.CurrentSessionID == nil {
		return nil, errors.New("native Computer Use tools require the shared service and actual session ID")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.GrantFlags == nil {
		options.GrantFlags = func(context.Context) (json.RawMessage, error) { return encode(defaultFlags(true)), nil }
	}
	if options.FormatLockHeld == nil {
		options.FormatLockHeld = func(holder string) string {
			if len(holder) > 8 {
				holder = holder[:8]
			}
			return "Computer use is in use by another Claude session (" + holder + "…). Wait for that session to finish or run /exit there."
		}
	}
	if options.NativeCall == nil {
		options.NativeCall = func(ctx context.Context, command string, payload json.RawMessage) (json.RawMessage, error) {
			return options.Service.Call(ctx, nativeToolSessionID(options), command, payload)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	tools := &NativeTools{options: options, ctx: ctx, cancel: cancel, delivered: map[string]bool{}}
	if tools.options.PermissionCard == nil {
		tools.options.PermissionCard = func(ctx context.Context) { _ = options.Service.PermissionCard(ctx) }
	}
	return tools, nil
}
func nativeToolSessionID(options NativeToolsOptions) string {
	if options.CurrentSessionID != nil {
		return options.CurrentSessionID()
	}
	return options.SessionID
}
func (tools *NativeTools) sessionID() string { return nativeToolSessionID(tools.options) }
func (tools *NativeTools) now() time.Time    { return tools.options.Now() }
func textResult(text string) NativeToolResult {
	return NativeToolResult{Content: []ToolContent{{Type: "text", Text: text}}}
}
func toolError(text, kind string) NativeToolResult {
	result := textResult(text)
	result.IsError = true
	if kind != "" {
		result.Telemetry = map[string]string{"error_kind": kind}
	}
	switch kind {
	case "app_denied", "bad_args", "tcc_not_granted", "grant_flag_required", "cu_lock_held", "feature_unavailable":
		result.NativeCallNotDispatched = true
	}
	if kind == "tcc_not_granted" {
		result.NativeError = map[string]any{"name": "SkyComputerUseError", "message": text, "code": -10009, "errorName": "permissionsNotGranted", "request": nil, "requestType": "jsonRPC"}
	}
	return result
}
func cancelledResult(text string) NativeToolResult {
	result := textResult(text)
	result.IsError = true
	result.NativeCallNotDispatched = true
	return result
}
func (tools *NativeTools) configuration(ctx context.Context) (object, bool) {
	config, err := tools.options.Service.Config(ctx)
	if err != nil {
		return nil, false
	}
	enabled, _ := boolean(config["enabled"])
	flag := strings.ToLower(trim(tools.options.Service.options.Environment["CLAUDE_COMPUTER_USE_ENABLED"]))
	return config, enabled && flag != "0" && flag != "false" && flag != "no" && flag != "off"
}
func (tools *NativeTools) Disabled(ctx context.Context) bool {
	_, enabled := tools.configuration(ctx)
	return !enabled
}
func (tools *NativeTools) enqueue() (<-chan struct{}, func()) {
	tools.queueMu.Lock()
	previous := tools.tail
	done := make(chan struct{})
	tools.tail = done
	tools.queueMu.Unlock()
	return previous, func() {
		if previous == nil {
			close(done)
			return
		}
		select {
		case <-previous:
			close(done)
		default:
			go func() { <-previous; close(done) }()
		}
	}
}
func (tools *NativeTools) Call(ctx context.Context, name string, raw json.RawMessage) (NativeToolResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(tools.ctx, cancel)
	defer func() { stop(); cancel() }()
	previous, release := tools.enqueue()
	defer release()
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return cancelledResult("Computer Use cancelled before dispatch."), nil
		}
	}
	if ctx.Err() != nil {
		return cancelledResult("Computer Use cancelled before dispatch."), nil
	}
	_, enabled := tools.configuration(ctx)
	if !enabled {
		return toolError("Computer control is disabled in Settings. Enable it and try again.", "other"), nil
	}
	args, _ := decodeObject(raw)
	if args == nil {
		args = object{}
	}
	flagsRaw, err := tools.options.GrantFlags(ctx)
	if err != nil {
		return NativeToolResult{}, err
	}
	flags, _ := decodeObject(flagsRaw)
	request, err := parseNativeTool(name, args, flags, tools.options.Service.options.Platform)
	if err != nil {
		var bad *toolArgumentError
		if !errors.As(err, &bad) {
			return NativeToolResult{}, err
		}
		message := bad.message
		if bad.kind == "bad_args" && knownNativeTool(name) {
			message = `Tool "` + name + `": ` + message
		}
		return toolError(message, bad.kind), nil
	}
	// Preserve the real binder's asynchronous lock before its internal TCC gate.
	lock := tools.checkLock()
	if lock.Held && !lock.Self {
		return toolError(tools.options.FormatLockHeld(lock.Holder), "cu_lock_held"), nil
	}
	if !lock.Held && name != "list_apps" {
		if lock, err = tools.acquireLock(ctx); err != nil {
			return NativeToolResult{}, err
		}
		if lock.Held && !lock.Self {
			return toolError(tools.options.FormatLockHeld(lock.Holder), "cu_lock_held"), nil
		}
	}
	if ctx.Err() != nil {
		return cancelledResult("Computer Use cancelled before dispatch."), nil
	}
	permissionsRaw, err := tools.options.NativeCall(ctx, "check_permissions", json.RawMessage(`{}`))
	if err != nil {
		return NativeToolResult{}, err
	}
	var permissions Permissions
	if err = json.Unmarshal(permissionsRaw, &permissions); err != nil {
		return NativeToolResult{}, err
	}
	if permissions.Accessibility == nil || !*permissions.Accessibility || (permissions.ScreenRecording != nil && !*permissions.ScreenRecording) {
		tools.maybePermissionCard()
		return toolError("Accessibility and Screen Recording permissions are required for Computer Use. Grant them in System Settings and try again.", "tcc_not_granted"), nil
	}
	if ctx.Err() != nil || tools.Disabled(ctx) {
		return toolError("Computer Use stopped before dispatch.", "other"), nil
	}
	if tools.options.Service.options.Platform != "darwin" {
		return toolError("Computer Use is not available on this platform (the native accessibility engine is not wired).", "feature_unavailable"), nil
	}
	if request.target == nil {
		result, err := tools.runTool(ctx, request, object{})
		if err != nil {
			return rejectedNative(err), nil
		}
		return result, nil
	}
	resolvedRaw, err := tools.options.NativeCall(ctx, "resolve_app_target", encode(request.target))
	if err != nil {
		return rejectedNative(err), nil
	}
	resolved, ok := decodeObject(resolvedRaw)
	if !ok {
		return rejectedNative(errors.New("native target resolver returned invalid data")), nil
	}
	identity := provenProcessIdentity(resolved)
	if request.mutating && identity == nil {
		return toolError("Resolving '"+request.requestedApp+"' did not prove the running process lifetime, so the action was not performed. Call get_app_state to re-establish the target, then retry.", "executor_threw"), nil
	}
	if ctx.Err() != nil || tools.Disabled(ctx) {
		return toolError("Computer Use stopped before dispatch.", "other"), nil
	}
	target := dispatchResolvedTarget(resolved, request.target, identity)
	if request.steps != nil {
		return tools.runSequence(ctx, request, target), nil
	}
	result, err := tools.runTool(ctx, request, target)
	if err != nil {
		return rejectedNative(err), nil
	}
	if name == "get_app_state" {
		if path, ok := stringValue(resolved["path"]); ok {
			result.App = path
		}
	}
	return result, nil
}
func truthyRaw(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return false
	}
	if text, ok := stringValue(raw); ok {
		return text != ""
	}
	if number, err := optionalNumber(raw, ""); err == nil && number != nil {
		return *number != 0
	}
	return true
}
func provenProcessIdentity(resolved object) json.RawMessage {
	if identity := resolved["processIdentity"]; truthyRaw(identity) {
		return identity
	}
	pid, err := optionalNumber(resolved["pid"], "pid")
	if err != nil || pid == nil || *pid <= 0 {
		return nil
	}
	launchTime, err := optionalNumber(resolved["launchTime"], "launchTime")
	if err != nil || launchTime == nil {
		return nil
	}
	identity := object{"pid": encode(*pid), "launchTime": encode(*launchTime)}
	for _, key := range []string{"bundleId", "executablePath"} {
		if raw := resolved[key]; raw != nil {
			identity[key] = raw
		}
	}
	return encode(identity)
}
func dispatchResolvedTarget(resolved, original object, identity json.RawMessage) object {
	pid, err := optionalNumber(resolved["pid"], "pid")
	if err != nil || pid == nil || *pid <= 0 || math.IsInf(*pid, 0) {
		return original
	}
	target := object{"pid": encode(*pid)}
	if identity != nil {
		target["expectedProcessIdentity"] = identity
	}
	return target
}
func (tools *NativeTools) runTool(ctx context.Context, request *parsedTool, target object) (NativeToolResult, error) {
	payload := copyObject(target)
	for key, raw := range request.payload {
		payload[key] = raw
	}
	raw, err := tools.options.NativeCall(ctx, request.name, encode(payload))
	if err != nil {
		return NativeToolResult{}, err
	}
	if request.name == "list_apps" {
		var apps []object
		if err = json.Unmarshal(raw, &apps); err != nil {
			return NativeToolResult{}, err
		}
		result := textResult("No running applications are available to control.")
		inventory := []json.RawMessage{}
		lines := []string{}
		for _, app := range apps {
			if _, found := app["id"]; !found {
				app = object{"id": app["bundleId"], "displayName": app["displayName"], "isRunning": encode(true)}
			}
			inventory = append(inventory, encode(app))
			name := app["displayName"]
			if absent(name) {
				name = app["id"]
			}
			lines = append(lines, jsString(name)+" — "+jsString(app["id"]))
		}
		result.Apps = encode(inventory)
		if len(lines) > 0 {
			result.Content[0].Text = strings.Join(lines, "\n")
		}
		return result, nil
	}
	if request.name == "get_app_state" {
		return tools.frameState(raw, request.requestedApp)
	}
	return textResult("Action completed. Call `get_app_state` to fetch the updated UI state."), nil
}
func (tools *NativeTools) frameState(raw json.RawMessage, requested string) (NativeToolResult, error) {
	state, ok := decodeObject(raw)
	if !ok {
		return NativeToolResult{}, errors.New("native get_app_state returned invalid data")
	}
	text, ok := stringValue(state["axText"])
	if !ok {
		return NativeToolResult{}, errors.New("native get_app_state returned no AX tree")
	}
	lines := []string{"Computer Use state (CUA App Version: 1)"}
	instructions, _ := stringValue(state["appInstructions"])
	instructions = trim(instructions)
	if instructions != "" {
		key := requested
		if bundle, ok := stringValue(state["bundleId"]); ok {
			key = bundle
		}
		if !tools.delivered[key] {
			tools.delivered[key] = true
			lines = append(lines, "<app_specific_instructions>", instructions, "</app_specific_instructions>")
		}
	}
	text = strings.TrimRightFunc(text, func(r rune) bool { return trim(string(r)) == "" })
	lines = append(lines, "<app_state>", text, "</app_state>")
	result := textResult(strings.Join(lines, "\n"))
	if screenshot, ok := decodeObject(state["screenshot"]); ok {
		if data, ok := stringValue(screenshot["base64"]); ok && data != "" {
			mime := "image/png"
			if value, ok := stringValue(screenshot["mimeType"]); ok {
				mime = value
			}
			result.Content = append(result.Content, ToolContent{Type: "image", Data: data, MimeType: mime})
		}
	}
	return result, nil
}
func rejectedNative(err error) NativeToolResult {
	result := toolError(err.Error(), "executor_threw")
	result.NativeError = map[string]any{"name": "Error", "message": err.Error()}
	var native *NativeCommandError
	if errors.As(err, &native) && native.Code != "" {
		result.NativeError["nativeCode"] = native.Code
		mapping := map[string]struct {
			name string
			code int
		}{"not_trusted": {"permissionsNotGranted", -10009}, "screen_recording_denied": {"permissionsNotGranted", -10009}, "ax_failed": {"accessibilityError", -10008}, "process_gone": {"runningApplicationNotFound", -10007}, "target_not_running": {"runningApplicationNotFound", -10007}, "ambiguous_target": {"ambiguousApp", -10018}, "protocol_mismatch": {"incompatibleClientVersion", -10013}, "screen_locked": {"screenLocked", -10020}, "user_interference": {"userIntervened", -10016}}
		if mapped, found := mapping[native.Code]; found {
			result.NativeError["name"] = "SkyComputerUseError"
			result.NativeError["code"] = mapped.code
			result.NativeError["errorName"] = mapped.name
			result.NativeError["request"] = nil
			result.NativeError["requestType"] = "jsonRPC"
		}
	}
	return result
}
func (tools *NativeTools) maybePermissionCard() {
	tools.cardMu.Lock()
	if tools.cardInFlight || (!tools.lastCard.IsZero() && tools.now().Sub(tools.lastCard) < 15*time.Second) {
		tools.cardMu.Unlock()
		return
	}
	tools.cardInFlight = true
	tools.lastCard = tools.now()
	tools.cardMu.Unlock()
	go func() {
		tools.options.PermissionCard(tools.ctx)
		tools.cardMu.Lock()
		tools.cardInFlight = false
		tools.cardMu.Unlock()
	}()
}
func (tools *NativeTools) EndTurn(ctx context.Context) error {
	previous, release := tools.enqueue()
	defer release()
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	nativeErr := tools.options.Service.EndTurn(ctx, tools.sessionID())
	if !tools.lockHeld {
		return nativeErr
	}
	return errors.Join(nativeErr, tools.releaseLock(ctx))
}
func (tools *NativeTools) Close(ctx context.Context) error {
	tools.cancel()
	return errors.Join(tools.EndTurn(ctx), tools.options.Service.CloseSession(ctx, tools.sessionID()))
}
