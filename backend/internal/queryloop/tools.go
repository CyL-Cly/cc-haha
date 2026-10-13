package queryloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func quoted(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// RegisteredTool supplies the original tool implementation and original schema
// parser. The same original definition is rendered by requestcontext; an API
// schema captured from Source is never an input to this execution boundary.
type RegisteredTool struct {
	AlwaysLoad           bool
	ShouldDefer          bool
	SearchHint           json.RawMessage
	nativeMCP            *mcpruntime.Service
	Name                 string
	Aliases              []string
	IsMCP                bool
	ParseInput           func(protocol.JSONObject) (protocol.JSONObject, error)
	IsConcurrencySafe    func(protocol.JSONObject) bool
	ValidateInput        func(context.Context, protocol.JSONObject, any) error
	NormalizeInputJSON   func(context.Context, protocol.JSONObject, JSONToolCallContext) (protocol.JSONObject, error)
	Backfill             Backfill
	Call                 func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error)
	MapResult            func(json.RawMessage, string) (protocol.JSONObject, error)
	CallJSON             func(context.Context, protocol.JSONObject, JSONToolCallContext) (ToolCallResult, error)
	StartCallJSON        func(context.Context, protocol.JSONObject, JSONToolCallContext) (*ownerasync.OwnerFuture[ToolCallResult], error)
	MapResultJSON        func(json.RawMessage, json.RawMessage) (protocol.JSONObject, error)
	PersistenceThreshold float64 // Declared tool cap; the native result consumer applies Source overrides and the global clamp.
}

type ToolCallContext struct {
	ToolUseID    string
	UserModified bool
	Opaque       any
	Invocation   Invocation
	// Source toolUseContext.messages is the pre-response, resolved query
	// history. Observable backfill and current response envelopes are excluded.
	Messages      []Message
	QueryTracking Tracking
}

// JSONToolCallContext retains JavaScript code units in a quoted JSON string.
// Scalar callbacks remain available but never receive a replaced surrogate.
type JSONToolCallContext struct {
	NativePermissionToolNames  []string
	NativePermissionAgentType  string
	NativePermissionHookSignal context.Context // Actual query/tool controller, distinct from batch cleanup.
	CancelModel                context.CancelFunc
	OnProgress                 func(json.RawMessage, protocol.JSONObject) error
	PermDecision               *ToolPermission
	ToolUseID                  json.RawMessage
	UserModified               bool
	Opaque                     any
	Invocation                 Invocation
	Messages                   []Message
	QueryTracking              Tracking
	HookPermission             *ToolPermission
	PreToolStopped             bool
	PreToolStopMessage         *Message
}

// PreToolEffects is the original pre-tool stage's retained messages and
// permission result. Progress is delivered immediately; other messages precede
// tool feedback, while AfterMessages follow successful tool-provided messages.
type PreToolEffects struct {
	Input         *protocol.JSONObject
	Messages      []Message
	AfterMessages []Message
	Permission    *ToolPermission
	Stop          bool
	StopMessage   *Message
}
type PostToolEffects struct {
	Messages      []Message
	UpdatedOutput json.RawMessage
}
type JSONToolBatchCapabilities struct {
	PreTool            func(context.Context, RegisteredTool, *protocol.JSONObject, JSONToolCallContext) (*protocol.JSONObject, error)
	PreToolEffects     func(context.Context, RegisteredTool, *protocol.JSONObject, JSONToolCallContext, func(Message) error) (PreToolEffects, error)
	ResolvePermission  func(context.Context, RegisteredTool, *protocol.JSONObject, JSONToolCallContext) (ToolPermission, error)
	PostTool           func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, JSONToolCallContext) ([]Message, error)
	PostToolEffects    func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, JSONToolCallContext, func(Message) error) (PostToolEffects, error)
	FailureToolEffects func(context.Context, RegisteredTool, *protocol.JSONObject, error, JSONToolCallContext) ([]Message, error)
	InProgress         func(json.RawMessage, bool) error
}

// A resolved catalog retains Source's ordered first name-or-alias match. A
// descriptor without an implementation is enough to reject a deprecated alias
// requiring an unported handler; it is never an execution default.
type ToolDescriptor struct {
	Name           string
	Aliases        []string
	Implementation *RegisteredTool
}
type ToolCallResult struct {
	NewMessages      []Message       // Original tool-provided users/attachments, after tool feedback.
	StructuredOutput json.RawMessage // Original Tool.call structured_output; nil retains absent.
	Data             json.RawMessage
	MCPMeta          json.RawMessage
	ModifyContext    func(any) (any, error)
}

// PermissionInput is the input selected by the real host's hook/rule/permission
// resolver. UpdatedInput represents the decision's optional explicit override.
// Pointer identity is needed by Source's pre-backfill call-input convergence.
type ToolPermission struct {
	Behavior           string
	Message            json.RawMessage // Source optional message; nil retains undefined.
	Input              *protocol.JSONObject
	UpdatedInput       *protocol.JSONObject
	UserModified       bool
	AcceptFeedback     json.RawMessage
	ContentBlocks      []json.RawMessage
	Description        json.RawMessage
	DisplayName        json.RawMessage
	UpdatedPermissions json.RawMessage
	Suggestions        json.RawMessage
	DecisionReason     json.RawMessage
	Metadata           json.RawMessage // Original local permission-UI metadata.
}
type ToolBatchCapabilities struct {
	ProcessToolResult           func(context.Context, protocol.JSONObject, string, float64) (protocol.JSONObject, error)
	ToolAllowedNow              func(string) bool
	OmitSuccessfulToolUseResult bool // Actual Source agentId && !preserveToolUseResults.
	OmitMCPMeta                 bool // Original tool executor keeps MCP metadata only on main thread.
	Owner                       *cooperative.Owner
	Scope                       *cooperative.Scope
	MaxConcurrency              int
	PreTool                     func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (*protocol.JSONObject, error)
	ResolvePermission           func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallContext) (ToolPermission, error)
	PostTool                    func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, ToolCallContext) ([]Message, error)
	InProgress                  func(string, bool) error
	JSON                        *JSONToolBatchCapabilities
	DeprecatedCatalog           []ToolDescriptor // nil means the host has not resolved it.
	CancellationContent         json.RawMessage  // Resolved withMemoryCorrectionHint text.
}

// SerialToolBatch ports the non-concurrent successful branch of runTools,
// runToolsSerially and checkPermissionsAndCallTool. Permissions and hooks are
// required host capabilities. Their absent implementation is not an allow.
type SerialToolBatch struct {
	tools        []RegisteredTool
	capabilities ToolBatchCapabilities
	factory      *messages.Factory
	general      bool
}

func NewSerialToolBatch(tools []RegisteredTool, capabilities ToolBatchCapabilities, factory *messages.Factory) (*SerialToolBatch, error) {
	return newToolBatch(tools, capabilities, factory, false)
}

// NewToolBatch uses the existing tool consumer with model-visible failure
// feedback and native Bash/readonly tools. The legacy serial profile retains
// its explicit capability refusals and is not silently relabeled as complete.
func NewToolBatch(tools []RegisteredTool, capabilities ToolBatchCapabilities, factory *messages.Factory) (*SerialToolBatch, error) {
	return newToolBatch(tools, capabilities, factory, true)
}

func newToolBatch(tools []RegisteredTool, capabilities ToolBatchCapabilities, factory *messages.Factory, general bool) (*SerialToolBatch, error) {
	if factory == nil || capabilities.JSON == nil && (capabilities.PreTool == nil || capabilities.ResolvePermission == nil || capabilities.PostTool == nil || capabilities.InProgress == nil) {
		return nil, errors.New("serial tool batch requires explicit factory, hook, permission and progress capabilities")
	}
	if capabilities.JSON != nil && (capabilities.JSON.PreTool == nil || capabilities.JSON.ResolvePermission == nil || capabilities.JSON.PostTool == nil || capabilities.JSON.InProgress == nil) {
		return nil, errors.New("serial tool batch requires all JSON hook, permission and progress capabilities")
	}
	for _, tool := range tools {
		if err := validateRegisteredToolMode(tool, general); err != nil {
			return nil, err
		}
	}
	for _, descriptor := range capabilities.DeprecatedCatalog {
		if !scalarRegistryStrings(descriptor.Name, descriptor.Aliases) {
			return nil, unsupported("non-scalar deprecated registry definition", ErrUnsupported)
		}
		if descriptor.Implementation != nil {
			if err := validateRegisteredTool(*descriptor.Implementation); err != nil {
				return nil, err
			}
			if descriptor.Implementation.Name != descriptor.Name || !reflect.DeepEqual(descriptor.Implementation.Aliases, descriptor.Aliases) {
				return nil, errors.New("deprecated descriptor must retain original implementation identity")
			}
		}
	}
	return &SerialToolBatch{tools: append([]RegisteredTool(nil), tools...), capabilities: capabilities, factory: factory, general: general}, nil
}
func scalarRegistryStrings(name string, aliases []string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for _, alias := range aliases {
		if !utf8.ValidString(alias) {
			return false
		}
	}
	return true
}
func validateRegisteredTool(tool RegisteredTool) error {
	return validateRegisteredToolMode(tool, false)
}
func validateRegisteredToolMode(tool RegisteredTool, general bool) error {
	if !scalarRegistryStrings(tool.Name, tool.Aliases) {
		return unsupported("non-scalar registered tool definition", ErrUnsupported)
	}
	if tool.ParseInput == nil || tool.IsConcurrencySafe == nil || tool.Call == nil && tool.CallJSON == nil || tool.MapResult == nil && tool.MapResultJSON == nil || !(tool.PersistenceThreshold > 0) {
		return errors.New("serial tool batch requires original registered tool implementations and resolved thresholds")
	}
	if (tool.IsMCP || strings.HasPrefix(tool.Name, "mcp__")) && (!general || tool.nativeMCP == nil) || tool.Name == "Bash" && !general {
		return unsupported("MCP/Bash-specific execution policies", ErrUnsupported)
	}
	return nil
}

// BindNativeMCP retains the finite declaration guard while admitting tools
// produced by an actual native connection service into the general batch.
func BindNativeMCP(tool RegisteredTool, runtime *mcpruntime.Service) (RegisteredTool, error) {
	if runtime == nil || !tool.IsMCP || !strings.HasPrefix(tool.Name, "mcp__") {
		return RegisteredTool{}, ErrUnsupported
	}
	tool.nativeMCP = runtime
	if err := validateRegisteredToolMode(tool, true); err != nil {
		return RegisteredTool{}, err
	}
	return tool, nil
}
func (batch *SerialToolBatch) findJSON(name json.RawMessage) *RegisteredTool {
	for index := range batch.tools {
		tool := &batch.tools[index]
		if batch.capabilities.ToolAllowedNow != nil && !batch.capabilities.ToolAllowedNow(tool.Name) {
			continue
		}
		if matchesName(name, tool.Name, tool.Aliases) {
			return tool
		}
	}
	return nil
}
func matchesName(name json.RawMessage, primary string, aliases []string) bool {
	if equalString(name, quoted(primary)) {
		return true
	}
	return matchesAlias(name, aliases)
}
func matchesAlias(name json.RawMessage, aliases []string) bool {
	for _, alias := range aliases {
		if equalString(name, quoted(alias)) {
			return true
		}
	}
	return false
}
func (batch *SerialToolBatch) executionTool(name json.RawMessage) (*RegisteredTool, error) {
	if tool := batch.findJSON(name); tool != nil {
		return tool, nil
	}
	if batch.capabilities.DeprecatedCatalog == nil {
		return nil, unsupported("unresolved deprecated tool catalog", ErrUnsupported)
	}
	for _, descriptor := range batch.capabilities.DeprecatedCatalog {
		if !matchesName(name, descriptor.Name, descriptor.Aliases) {
			continue
		}
		if !matchesAlias(name, descriptor.Aliases) {
			return nil, nil
		}
		if descriptor.Implementation == nil {
			return nil, unsupported("deprecated tool handler", ErrUnsupported)
		}
		return descriptor.Implementation, nil
	}
	return nil, nil
}
func (batch *SerialToolBatch) LookupBackfillJSON(name json.RawMessage) (Backfill, error) {
	if _, err := protocol.DecodeJavaScriptJSONString(name); err != nil {
		return nil, unsupported("non-string model tool name", err)
	}
	if tool := batch.findJSON(name); tool != nil {
		return tool.Backfill, nil
	}
	return nil, nil
}
func (batch *SerialToolBatch) find(name string) *RegisteredTool {
	for index := range batch.tools {
		tool := &batch.tools[index]
		if batch.capabilities.ToolAllowedNow != nil && !batch.capabilities.ToolAllowedNow(tool.Name) {
			continue
		}
		if tool.Name == name {
			return tool
		}
		for _, alias := range tool.Aliases {
			if alias == name {
				return tool
			}
		}
	}
	return nil
}
func (batch *SerialToolBatch) LookupBackfill(name string) (Backfill, error) {
	if tool := batch.find(name); tool != nil {
		return tool.Backfill, nil
	}
	return nil, nil // Source's observable projection ignores unregistered tools.
}

// Legacy host callbacks use scalar Go strings. This conversion is strict:
// arbitrary model code units use the JSON interfaces instead of replacing a
// lone surrogate or exposing an empty ID to a legacy callback.
func modelExecutionString(raw json.RawMessage, field string) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", unsupported("non-string model tool "+field, err)
	}
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return "", unsupported("unpaired UTF-16 model tool "+field, ErrUnsupported)
			}
			index++
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			return "", unsupported("unpaired UTF-16 model tool "+field, ErrUnsupported)
		}
	}
	return string(utf16.Decode(units)), nil
}
func invocationInput(use Invocation) (protocol.JSONObject, error) {
	if use.Block == nil || use.Assistant == nil {
		return protocol.JSONObject{}, errors.New("tool invocation requires its original assistant envelope")
	}
	var input protocol.JSONObject
	if err := json.Unmarshal(use.Block.Object.Get("input"), &input); err != nil || !input.IsObject() {
		return protocol.JSONObject{}, unsupported("tool input validation feedback", ErrUnsupported)
	}
	return input, nil
}
func concurrencySafe(tool RegisteredTool, input protocol.JSONObject) (safe bool) {
	// Source catches only isConcurrencySafe failures and conservatively selects
	// its serial branch. Other parser/host/call failures retain their ownership.
	defer func() {
		if recover() != nil {
			safe = false
		}
	}()
	return tool.IsConcurrencySafe(input)
}
func equalString(a, b json.RawMessage) bool {
	aUnits, aError := protocol.DecodeJavaScriptJSONString(a)
	bUnits, bError := protocol.DecodeJavaScriptJSONString(b)
	return aError == nil && bError == nil && reflect.DeepEqual(aUnits, bUnits)
}
func callInput(original, backfilled, processed *protocol.JSONObject) (protocol.JSONObject, error) {
	if backfilled != nil && processed != original && processed.Get("file_path") != nil && original.Get("file_path") != nil {
		// File tools declare strings. JSON alone cannot preserve JavaScript
		// reference equality for an object-valued host replacement; do not
		// silently treat that unported convergence branch as unequal.
		for _, value := range []json.RawMessage{processed.Get("file_path"), backfilled.Get("file_path"), original.Get("file_path")} {
			if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
				return protocol.JSONObject{}, unsupported("non-string file path convergence", err)
			}
		}
		if equalString(processed.Get("file_path"), backfilled.Get("file_path")) {
			copy := processed.Clone()
			copy.Set("file_path", original.Get("file_path"))
			return copy, nil
		}
	}
	if processed != backfilled {
		return *processed, nil
	}
	return *original, nil
}

func (batch *SerialToolBatch) executeSequential(ctx context.Context, round Round, uses []Invocation, emit func(Message) error) (any, error) {
	if emit == nil {
		return nil, errors.New("serial tool batch requires a consumer")
	}
	// Unknown tools and schema failures are serial in Source. Partition uses
	// only the available registry; deprecated aliases are resolved at execution.
	for _, use := range uses {
		if use.Block == nil || use.Assistant == nil {
			return nil, errors.New("tool invocation requires its original assistant envelope")
		}
		name, err := modelJSONString(use.Block.Object.Get("name"), "name")
		if err != nil {
			return nil, err
		}
		id, err := modelJSONString(use.Block.Object.Get("id"), "id")
		if err != nil {
			return nil, err
		}
		tool := batch.findJSON(name)
		if err := batch.requireIdentityCapabilities(id, tool); err != nil {
			return nil, err
		}
		if tool == nil {
			continue
		}
		input, err := invocationInput(use)
		if err != nil {
			continue
		}
		if batch.general {
			continue
		}
		parsed, err := tool.ParseInput(input)
		if err != nil {
			continue
		}
		if concurrencySafe(*tool, parsed) {
			return nil, unsupported("concurrent tool batch", ErrUnsupported)
		}
	}
	current := round.ToolContext
	batchEmit := emit
	for _, use := range uses {
		var before, after []Message
		emit := emitPreMessages(batchEmit, &before)
		name, err := modelJSONString(use.Block.Object.Get("name"), "name")
		if err != nil {
			return nil, err
		}
		id, err := modelJSONString(use.Block.Object.Get("id"), "id")
		if err != nil {
			return nil, err
		}
		if err := batch.requireIdentityCapabilities(id, batch.findJSON(name)); err != nil {
			return nil, err
		}
		if err := batch.progress(id, true); err != nil {
			return nil, err
		}
		if preview, length, count, marker := unparsedMarker(use.Block.Object.Get("input")); marker {
			if err := batch.markerFeedback(use, name, id, preview, length, count, emit); err != nil {
				return nil, err
			}
			if err := batch.progress(id, false); err != nil {
				return nil, err
			}
			continue
		}
		tool, err := batch.executionTool(name)
		if err != nil {
			return nil, err
		}
		if tool == nil {
			if err := batch.unknownFeedback(use, name, id, emit); err != nil {
				return nil, err
			}
			if err := batch.progress(id, false); err != nil {
				return nil, err
			}
			continue
		}
		if err := batch.requireIdentityCapabilities(id, tool); err != nil {
			return nil, err
		}
		failedCall := func(failure error) error {
			if err := batch.callFailureFeedback(use, id, tool.Name, failure, emit); err != nil {
				return err
			}
			return batch.progress(id, false)
		}
		if ctx.Err() != nil {
			if err := batch.cancellationFeedback(use, id, emit); err != nil {
				return nil, err
			}
			if err := batch.progress(id, false); err != nil {
				return nil, err
			}
			continue
		}
		input, err := invocationInput(use)
		if err != nil {
			if batch.general {
				failure := toolschema.ObjectInputValidationError(use.Block.Object.Get("input"))
				if err := batch.schemaFailureFeedback(use, id, tool.Name, failure, emit); err != nil {
					return nil, err
				}
				if err := batch.progress(id, false); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		parsed, err := tool.ParseInput(input)
		if err != nil {
			if batch.general {
				if err := batch.schemaFailureFeedback(use, id, tool.Name, err, emit); err != nil {
					return nil, err
				}
				if err := batch.progress(id, false); err != nil {
					return nil, err
				}
				continue
			}
			return nil, unsupported("tool schema validation feedback", err)
		}
		if tool.ValidateInput != nil {
			if err := tool.ValidateInput(ctx, parsed, current); err != nil {
				if batch.general {
					var rawSkill interface{ SkillValidationMessageJSON() json.RawMessage }
					if tool.Name == "Skill" && errors.As(err, &rawSkill) && rawSkill.SkillValidationMessageJSON() != nil {
						if err := batch.skillValidationFeedbackJSON(use, id, rawSkill.SkillValidationMessageJSON(), emit); err != nil {
							return nil, err
						}
					} else if err := batch.failureFeedback(use, id, err.Error(), emit); err != nil {
						return nil, err
					}
					if err := batch.progress(id, false); err != nil {
						return nil, err
					}
					continue
				}
				return nil, unsupported("tool value validation feedback", err)
			}
		}
		callContext := JSONToolCallContext{OnProgress: func(childID json.RawMessage, data protocol.JSONObject) error {
			return emit(Message{Record: batch.factory.CreateProgress(childID, id, data)})
		}, ToolUseID: id, Opaque: current, Invocation: use, Messages: round.History, QueryTracking: round.Tracking}
		if tool.NormalizeInputJSON != nil {
			parsed, err = tool.NormalizeInputJSON(ctx, parsed, callContext)
			if err == nil && !parsed.IsObject() {
				err = errors.New("normalized tool input must be an object")
			}
			if err != nil {
				if batch.general {
					if err := failedCall(err); err != nil {
						return nil, err
					}
					continue
				}
				return nil, err
			}
		}
		original := &parsed
		processed := original
		var backfilled *protocol.JSONObject
		if tool.Backfill != nil {
			copy := original.Clone()
			backfilled = &copy
			if err := tool.Backfill(backfilled); err != nil {
				if batch.general {
					if err := failedCall(err); err != nil {
						return nil, err
					}
					continue
				}
				return nil, err
			}
			processed = backfilled
		}
		processed, err = batch.preEffects(ctx, *tool, processed, &callContext, &before, &after, batchEmit)
		if err != nil {
			if batch.general {
				if err := failedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		if processed == nil || !processed.IsObject() {
			return nil, unsupported("pre-tool input replacement", ErrUnsupported)
		}
		if callContext.PreToolStopped {
			if err := batch.emitPreStop(callContext, emit); err != nil {
				return nil, err
			}
			continue
		}
		permission, err := batch.permission(ctx, *tool, processed, callContext)
		if err != nil {
			if fatalToolError(err) {
				return nil, err
			}
			if batch.general {
				if err := failedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		if permission.Behavior != "allow" || permission.Input == nil || !permission.Input.IsObject() {
			if batch.general && (permission.Behavior == "deny" || permission.Behavior == "ask") {
				if err := batch.permissionFeedbackHistory(use, id, permission, round.History, emit); err != nil {
					return nil, err
				}
				if err := batch.progress(id, false); err != nil {
					return nil, err
				}
				continue
			}
			return nil, unsupported("tool permission feedback", ErrUnsupported)
		}
		processed = permission.Input
		if permission.UpdatedInput != nil {
			processed = permission.UpdatedInput
		}
		if !processed.IsObject() {
			return nil, unsupported("permission input replacement", ErrUnsupported)
		}
		callContext.UserModified = permission.UserModified
		inputForCall, err := callInput(original, backfilled, processed)
		if err != nil {
			if batch.general {
				if err := failedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		result, err := callTool(ctx, *tool, inputForCall, callContext)
		if err != nil {
			if batch.general {
				if err := batch.innerFailureWithHooks(ctx, use, id, *tool, processed, err, callContext, emit); err != nil {
					return nil, err
				}
				continue
			}
			return nil, unsupported("tool call failure feedback", err)
		}
		structured, err := batch.structuredAttachment(result.StructuredOutput)
		if err != nil {
			return nil, err
		}
		innerFailedCall := func(cause error) error {
			return batch.innerFailureWithHooks(ctx, use, id, *tool, processed, cause, callContext, emit)
		}
		mapped, err := mapTool(*tool, result.Data, id)
		if err != nil {
			if batch.general {
				if err := innerFailedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		mapped, err = batch.processToolResult(ctx, mapped, tool.Name, tool.PersistenceThreshold)
		if err != nil {
			if batch.general {
				if err := innerFailedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		options, err := permissionUserContent(mapped, permission, round.History)
		if err != nil {
			return nil, err
		}
		options["sourceToolAssistantUUID"] = quoted(use.Assistant.UUID)
		if !batch.capabilities.OmitSuccessfulToolUseResult {
			options["toolUseResult"] = result.Data
		}
		if result.MCPMeta != nil && !batch.capabilities.OmitMCPMeta {
			options["mcpMeta"] = result.MCPMeta
		}
		var message Message
		if !tool.IsMCP || !batch.nativeHookEffects() {
			message = CreateOrderedUserMessage(batch.factory, options)
		}
		post, changed, err := batch.postEffects(ctx, *tool, processed, &result, callContext, batchEmit)
		if err != nil {
			if batch.general {
				if err := innerFailedCall(err); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		if changed {
			mapped, err = mapTool(*tool, result.Data, id)
			if err == nil {
				mapped, err = batch.processToolResult(ctx, mapped, tool.Name, tool.PersistenceThreshold)
			}
			if err == nil {
				options, err = permissionUserContent(mapped, permission, round.History)
			}
			if err != nil {
				return nil, err
			}
			options["sourceToolAssistantUUID"] = quoted(use.Assistant.UUID)
			if !batch.capabilities.OmitSuccessfulToolUseResult {
				options["toolUseResult"] = result.Data
			}
			if result.MCPMeta != nil && !batch.capabilities.OmitMCPMeta {
				options["mcpMeta"] = result.MCPMeta
			}
		}
		if tool.IsMCP && batch.nativeHookEffects() {
			message = CreateOrderedUserMessage(batch.factory, options)
		}
		if result.ModifyContext != nil {
			current, err = result.ModifyContext(current)
			if err != nil {
				return nil, err
			}
		}
		if structured != nil {
			if err := emit(*structured); err != nil {
				return nil, err
			}
		}
		if err := emit(message); err != nil {
			return nil, err
		}
		if batch.nativeHookEffects() && !tool.IsMCP {
			for _, value := range post {
				if err := emit(value); err != nil {
					return nil, err
				}
			}
			post = nil
		}
		for _, message := range result.NewMessages {
			if err := emit(message); err != nil {
				return nil, err
			}
		}
		for _, message := range after {
			if err := emit(message); err != nil {
				return nil, err
			}
		}
		for _, message := range post {
			if err := emit(message); err != nil {
				return nil, err
			}
		}
		if err := batch.progress(id, false); err != nil {
			return nil, err
		}
	}
	return current, nil
}

func (batch *SerialToolBatch) failureFeedback(use Invocation, id json.RawMessage, detail string, emit func(Message) error) error {
	return batch.errorFeedback(use, id, detail, "Error: "+detail, emit)
}

// This producer is selected only by Skill's dedicated validation marker.
// Parse failures, call errors and every other tool retain their old adapters.
func (batch *SerialToolBatch) skillValidationFeedbackJSON(use Invocation, id, message json.RawMessage, emit func(Message) error) error {
	text, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("<tool_use_error>"), message, quoted("</tool_use_error>")}, "")
	if e != nil {
		return e
	}
	receipt, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), message}, "")
	if e != nil {
		return e
	}
	return batch.feedback(use, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: text}, protocol.JSONField{Name: "is_error", Value: json.RawMessage("true")}, protocol.JSONField{Name: "tool_use_id", Value: id}), receipt, emit)
}

func (batch *SerialToolBatch) schemaFailureFeedback(use Invocation, id json.RawMessage, name string, failure error, emit func(Message) error) error {
	content, receipt := toolschema.ValidationFeedback(name, failure)
	return batch.errorFeedback(use, id, "InputValidationError: "+content, "InputValidationError: "+receipt, emit)
}
func (batch *SerialToolBatch) callFailureFeedback(use Invocation, id json.RawMessage, name string, failure error, emit func(Message) error) error {
	var shell *toolerrors.ShellError
	if errors.As(failure, &shell) {
		content := toolerrors.FormatShellError(shell)
		receipt, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), content}, "")
		if e != nil {
			return e
		}
		block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "is_error", Value: []byte("true")}, protocol.JSONField{Name: "tool_use_id", Value: id})
		blocks, e := json.Marshal([]protocol.JSONObject{block})
		if e != nil {
			return e
		}
		return emit(CreateOrderedUserMessage(batch.factory, storage.Object{"content": blocks, "toolUseResult": receipt, "sourceToolAssistantUUID": quoted(use.Assistant.UUID)}))
	}
	detail := "Error calling tool (" + name + "): " + failure.Error()
	return batch.errorFeedback(use, id, detail, detail, emit)
}
func (batch *SerialToolBatch) permissionFeedback(use Invocation, id json.RawMessage, permission ToolPermission, emit func(Message) error) error {
	return batch.permissionFeedbackHistory(use, id, permission, nil, emit)
}

func (batch *SerialToolBatch) permissionFeedbackHistory(use Invocation, id json.RawMessage, permission ToolPermission, history []Message, emit func(Message) error) error {
	message, err := protocol.JavaScriptStringJSON(permission.Message)
	if err != nil {
		return err
	}
	receipt, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), message}, "")
	if err != nil {
		return err
	}
	block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: permission.Message}, protocol.JSONField{Name: "is_error", Value: []byte("true")}, protocol.JSONField{Name: "tool_use_id", Value: id})
	options, err := permissionUserContent(block, permission, history)
	if err != nil {
		return err
	}
	options["toolUseResult"], options["sourceToolAssistantUUID"] = receipt, quoted(use.Assistant.UUID)
	return emit(CreateOrderedUserMessage(batch.factory, options))
}
func (batch *SerialToolBatch) errorFeedback(use Invocation, id json.RawMessage, detail, receipt string, emit func(Message) error) error {
	block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: quoted("<tool_use_error>" + detail + "</tool_use_error>")}, protocol.JSONField{Name: "is_error", Value: []byte("true")}, protocol.JSONField{Name: "tool_use_id", Value: id})
	content, err := json.Marshal([]protocol.JSONObject{block})
	if err != nil {
		return err
	}
	return emit(CreateOrderedUserMessage(batch.factory, storage.Object{"content": content, "toolUseResult": quoted(receipt), "sourceToolAssistantUUID": quoted(use.Assistant.UUID)}))
}

// CreateOrderedUserMessage is the Source createUserMessage enumeration adapter
// for shared Go factory defaults/allocations. Order is fixed business source,
// never reconstructed from a map iteration or UUID-dependent golden capture.
func CreateOrderedUserMessage(factory *messages.Factory, options storage.Object) Message {
	value := factory.CreateUser(options)
	api := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoted("user")}, protocol.JSONField{Name: "content", Value: options["content"]})
	var unordered storage.Object
	_ = json.Unmarshal(value["message"], &unordered)
	api.Set("content", unordered["content"])
	apiRaw, _ := api.MarshalJSON()
	ordered := protocol.NewJSONObject()
	for _, name := range []string{"type", "message", "isMeta", "isVisibleInTranscriptOnly", "isVirtual", "isCompactSummary", "summarizeMetadata", "uuid", "timestamp", "toolUseResult", "mcpMeta", "imagePasteIds", "sourceToolAssistantUUID", "permissionMode", "origin"} {
		if raw := value[name]; raw != nil {
			if name == "message" {
				raw = apiRaw
			}
			ordered.Set(name, raw)
		}
	}
	return Message{Record: ordered}
}

func jsWhitespace(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == 0x20 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}

func (batch *SerialToolBatch) processToolResult(ctx context.Context, block protocol.JSONObject, name string, threshold float64) (protocol.JSONObject, error) {
	if batch.capabilities.ProcessToolResult != nil {
		return batch.capabilities.ProcessToolResult(ctx, block, name, threshold)
	}
	return ProcessSmallTextToolResult(block, name, threshold)
}

// ProcessSmallTextToolResult ports empty-result correction and the exact JS
// UTF-16 size decision. Persistence, media and structured result policies are
// explicit unported boundaries, not silent truncation or an identity fallback.
func ProcessSmallTextToolResult(block protocol.JSONObject, name string, threshold float64) (protocol.JSONObject, error) {
	if block.String("type") != "tool_result" || name == "" || !(threshold > 0) {
		return protocol.JSONObject{}, unsupported("mapped tool result profile", ErrUnsupported)
	}
	content := block.Get("content")
	if content == nil || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		copy := block.Clone()
		copy.Set("content", quoted("("+name+" completed with no output)"))
		return copy, nil
	}
	empty, size := true, 0
	parts := []json.RawMessage{content}
	if len(content) > 0 && content[0] == '[' {
		var values []protocol.JSONObject
		if err := json.Unmarshal(content, &values); err != nil {
			return protocol.JSONObject{}, err
		}
		// Original maybePersistLargeToolResult sends any image-bearing result
		// as-is, even when its text sibling exceeds the persistence threshold.
		for _, value := range values {
			if value.String("type") == "image" {
				return block, nil
			}
		}
		parts = []json.RawMessage{}
		for _, value := range values {
			if value.String("type") == "tool_reference" {
				empty = false
			} else if value.String("type") == "text" {
				parts = append(parts, value.Get("text"))
			} else {
				return protocol.JSONObject{}, unsupported("tool result media processing", ErrUnsupported)
			}
		}
	}
	for _, part := range parts {
		units, err := protocol.DecodeJavaScriptJSONString(part)
		if err != nil {
			return protocol.JSONObject{}, unsupported("non-string tool result processing", err)
		}
		size += len(units)
		for _, unit := range units {
			if !jsWhitespace(unit) {
				empty = false
				break
			}
		}
	}

	if empty {
		copy := block.Clone()
		copy.Set("content", quoted("("+name+" completed with no output)"))
		return copy, nil
	}
	if float64(size) > threshold {
		return protocol.JSONObject{}, unsupported("large tool result persistence", ErrUnsupported)
	}
	return block, nil
}

// NormalizeOrdinaryToolResult is the release-default per-result normalization
// seam (tool search disabled). It strips tool references while preserving the
// original ordered envelope. Batch/history merging remains with requestcontext.
func NormalizeOrdinaryToolResult(message Message) ([]Message, error) {
	return NormalizeToolResultWithSearch(message, false)
}

func NormalizeToolResultWithSearch(message Message, toolSearchEnabled bool) ([]Message, error) {
	if message.Type() == "system" && message.Record.String("subtype") == "local_command" {
		return nil, unsupported("local-command result normalization", ErrUnsupported)
	}
	if message.Type() == "progress" || message.Type() == "system" {
		return []Message{}, nil
	}
	if message.Type() != "user" {
		return nil, unsupported("non-user tool normalization", ErrUnsupported)
	}
	virtual := message.Record.Get("isVirtual")
	if string(virtual) == "true" {
		return []Message{}, nil
	}
	if virtual != nil && string(virtual) != "false" {
		return nil, unsupported("non-boolean virtual marker", ErrUnsupported)
	}
	var api protocol.JSONObject
	if err := json.Unmarshal(message.Record.Get("message"), &api); err != nil || !api.IsObject() {
		return nil, unsupported("tool result envelope", ErrUnsupported)
	}
	content := api.Get("content")
	if len(content) > 0 && content[0] == '"' {
		return []Message{message}, nil
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(content, &blocks); err != nil || blocks == nil {
		return nil, unsupported("tool result content", ErrUnsupported)
	}
	found := false
	for _, block := range blocks {
		if block.String("type") == "text" {
			continue
		}
		if block.String("type") == "image" {
			if err := requestcontext.ValidateProcessedImageBlock(block); err != nil {
				return nil, unsupported("tool result media normalization", err)
			}
			continue
		}
		if block.String("type") == "document" {
			if err := requestcontext.ValidateDocumentBlock(block); err != nil {
				return nil, unsupported("tool result document normalization", err)
			}
			continue
		}
		if block.String("type") != "tool_result" {
			return nil, unsupported("tool result media normalization", ErrUnsupported)
		}
		var nested []protocol.JSONObject
		raw := block.Get("content")
		if len(raw) == 0 || raw[0] != '[' {
			continue
		}
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, err
		}
		for _, part := range nested {
			if part.String("type") == "tool_reference" {
				found = true
			} else if part.String("type") == "image" {
				if err := requestcontext.ValidateProcessedImageBlock(part); err != nil {
					return nil, unsupported("nested tool result media normalization", err)
				}
			} else if part.String("type") == "document" {
				if err := requestcontext.ValidateDocumentBlock(part); err != nil {
					return nil, unsupported("nested tool result document normalization", err)
				}
			} else if part.String("type") != "text" {
				return nil, unsupported("nested tool result media normalization", ErrUnsupported)
			}
		}
	}
	if err := requestcontext.ValidateUserImagesForAPI([]protocol.JSONObject{message.Record}); err != nil {
		return nil, err
	}
	if !found || toolSearchEnabled {
		return []Message{message}, nil
	}
	// Source clones every array-valued tool_result once any reference exists,
	// including an empty sibling array which receives its placeholder.
	for index, block := range blocks {
		var nested []protocol.JSONObject
		raw := block.Get("content")
		if block.String("type") != "tool_result" || len(raw) == 0 || raw[0] != '[' {
			continue
		}
		_ = json.Unmarshal(raw, &nested)
		kept := []protocol.JSONObject{}
		for _, part := range nested {
			if part.String("type") != "tool_reference" {
				kept = append(kept, part)
			}
		}
		if len(kept) == 0 {
			kept = append(kept, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("text")}, protocol.JSONField{Name: "text", Value: quoted("[Tool references removed - tool search not enabled]")}))
		}
		copy := block.Clone()
		raw, _ = json.Marshal(kept)
		copy.Set("content", raw)
		blocks[index] = copy
	}
	raw, _ := json.Marshal(blocks)
	api = api.Clone()
	api.Set("content", raw)
	raw, _ = api.MarshalJSON()
	copy := message.Record.Clone()
	copy.Set("message", raw)
	return []Message{{Record: copy}}, nil
}
