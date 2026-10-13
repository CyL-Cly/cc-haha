package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/compaction"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"strings"
	"time"
)

func (h *Host) compactStatus(scope *cooperative.Scope, status any, chars *int) error {
	data := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("status")}, protocol.JSONField{Name: "status", Value: raw(status)}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())})
	if chars != nil {
		data.Set("compact_progress", raw(map[string]int{"output_chars": *chars}))
	}
	_, e := h.enqueue(scope, data)
	return e
}
func (h *Host) compactConversationOnOwner(ctx context.Context, scope *cooperative.Scope, history []queryloop.Message, custom, direction string, pivot int, auto bool) (compaction.Result, error) {
	result := compaction.Result{}
	if h.options.Print == nil || !h.options.Print.NativeProcess || !h.nativeCompactModels.Owns(h.options.Owner) || !scope.BelongsTo(h.options.Owner) {
		return result, cooperative.ErrRestricted
	}
	if costAuthEnvTruthy(h.options.OriginalEnv["DISABLE_COMPACT"]) {
		return result, errors.New("Compaction is disabled")
	}
	records, e := querypreflight.InspectionRecords(history)
	if e != nil {
		return result, e
	}
	snapshot, e := h.sideQuestionFallback(scope)
	if e != nil {
		return result, e
	}
	snapshot.request.Context.Options.QuerySource = "compact"
	snapshot.request.Context.Options.SkipCacheWrite = true
	path, e := h.options.Controller.Writer().ComputedTranscriptPath(scope)
	if e != nil {
		return result, e
	}
	if e = h.compactStatus(scope, "compacting", nil); e != nil {
		return result, e
	}
	defer h.compactStatus(scope, nil, nil)
	call := queryhooks.Context{Scope: scope, AgentID: h.agentID(), History: history, QuerySource: "compact", Interactive: h.options.Print.NativeInteractive, TrustAccepted: true, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
	dep := compaction.Dependencies{Factory: h.options.Factory, UUID: h.options.UUID, Now: h.options.Now,
		Summarize: func(modelCtx context.Context, input compaction.SummaryInput) (compaction.Summary, error) {
			return h.compactSummary(modelCtx, scope, snapshot, input)
		},
		PreHook: func(ctx context.Context, trigger, instructions string) (compaction.HookResult, error) {
			r, e := h.hooks.ExecuteCompactHooks(ctx, "PreCompact", trigger, instructions, "", call)
			return compaction.HookResult{Instructions: r.Instructions, Display: r.Display}, e
		},
		PostHook: func(ctx context.Context, trigger, summary string) (compaction.HookResult, error) {
			r, e := h.hooks.ExecuteCompactHooks(ctx, "PostCompact", trigger, "", summary, call)
			return compaction.HookResult{Display: r.Display}, e
		},
		Restore: func(ctx context.Context, keep []protocol.JSONObject) ([]protocol.JSONObject, error) {
			out := []protocol.JSONObject{}
			if h.options.Print.Tools != nil {
				var e error
				excluded := []string{}
				if h.options.Print.CompactExcludedFiles != nil {
					excluded, e = h.options.Print.CompactExcludedFiles(scope)
					if e != nil {
						return nil, e
					}
				}
				out, e = h.options.Print.Tools.RestoreCompactFiles(ctx, scope, keep, h.options.Factory, excluded...)
				if e != nil {
					return nil, e
				}
			}
			if h.options.Print.CompactAttachments != nil {
				more, e := h.options.Print.CompactAttachments(ctx, scope, keep)
				if e != nil {
					return nil, e
				}
				out = append(out, more...)
			}
			return out, nil
		},
		SessionStart: func(ctx context.Context) ([]protocol.JSONObject, error) {
			r, e := h.hooks.ExecuteSessionStart(ctx, queryhooks.NativeSessionStartInput{SessionID: h.options.Controller.SessionID(), Source: "compact", Model: quote(snapshot.request.Context.Options.Model), Call: call})
			if e == nil && len(r.WatchPaths) > 0 {
				e = h.hooks.ApplyCompactWatchPaths(scope, r.WatchPaths)
			}
			return r.Messages, e
		},
	}
	originalScope := scope
	e = h.nativeCompactModels.Run(scope, ctx, func(current *cooperative.Scope) error {
		scope = current
		call.Scope = current
		var err error
		result, err = compaction.Run(ctx, compaction.Input{History: records, Custom: custom, Direction: direction, Pivot: pivot, Auto: auto, SuppressFollowUp: auto, Arm: h.options.OriginalEnv["CLAUDE_CODE_CURRIED_TRINKET"], TranscriptPath: path, Env: h.options.OriginalEnv}, dep)
		return err
	})
	scope = originalScope
	if e != nil {
		return result, e
	}
	if direction == "from" {
		keepCount := 0
		for _, record := range records[:pivot] {
			if record.String("type") != "progress" {
				keepCount++
			}
		}
		if keepCount > 0 {
			summary := result.History[1]
			reordered := append([]protocol.JSONObject{result.History[0]}, result.History[2:2+keepCount]...)
			reordered = append(reordered, summary)
			result.History = append(reordered, result.History[2+keepCount:]...)
		}
	}
	rebuilt := make([]queryloop.Message, len(result.History))
	for i, m := range result.History {
		rebuilt[i] = queryloop.Message{Record: m}
	}
	if e = h.serial.ReplaceCompactHistory(scope, rebuilt); e != nil {
		return result, e
	}
	if e = h.options.Controller.ReappendCompactMetadataOnOwner(scope); e != nil {
		return result, e
	}
	if h.options.Print.CompactCleanup != nil {
		if e = h.options.Print.CompactCleanup(scope); e != nil {
			return result, e
		}
	}
	if !auto {
		h.preflight.SuppressWarning()
	}
	return result, nil
}
func (h *Host) compactSummary(ctx context.Context, scope *cooperative.Scope, snapshot nativeSideQuestionCache, in compaction.SummaryInput) (compaction.Summary, error) {
	result := compaction.Summary{}
	history := append([]protocol.JSONObject{}, in.History...)
	history = append(history, messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(in.Prompt)})))
	projected, e := h.serial.Resolve(ctx, snapshot.raw, history)
	if e != nil {
		return result, e
	}
	request := snapshot.request
	request.Context.History = projected.History
	request.Context.Options.QuerySource = "compact"
	request.Context.Host.NativeToolDefinitions = snapshot.request.Context.Host.NativeToolDefinitions
	model := request.Context.Options.Model
	normal := requestcontext.NativeConfiguredMaxOutputTokens(model, h.options.OriginalEnv)
	window := querypreflight.ConfiguredContextWindow(model, h.options.OriginalEnv)
	if len(h.options.SDKBetas) > 0 {
		window, e = h.ContextWindowForModel(scope, model)
		if e != nil {
			return result, e
		}
	}
	if normal > 20000 && in.PreTokens+5000+normal > window {
		cap := float64(20000)
		request.Context.Options.MaxOutputTokensOverride = &cap
	}
	if !in.CacheSharing {
		request.Context.SystemPrompt = []json.RawMessage{quote("You are a helpful AI assistant tasked with summarizing conversations.")}
		request.Context.Thinking = requestcontext.Thinking{Type: "disabled"}
		cap := min(normal, 20000)
		request.Context.Options.MaxOutputTokensOverride = &cap
		fallbackTools := []protocol.JSONObject{}
		for _, tool := range request.Context.Tools {
			if tool.String("name") == "Read" {
				fallbackTools = append(fallbackTools, tool)
			}
		}
		fallbackDefinitions := []toolschema.Definition{}
		if request.Context.Host.NativeToolDefinitions != nil {
			for _, definition := range *request.Context.Host.NativeToolDefinitions {
				if definition.Name == "Read" {
					fallbackDefinitions = append(fallbackDefinitions, definition)
				}
			}
		}
		request.Context.Tools = fallbackTools
		request.Context.Host.NativeToolDefinitions = &fallbackDefinitions
		request.Context.Host.NativeToolSearch = nil
		request.Context.Host.ToolSearchCatalogue = nil
		for i, m := range request.Context.History {
			request.Context.History[i] = stripCompactMedia(m)
		}
	}
	binding, e := h.nativeFastModeBinding(scope)
	if e != nil {
		return result, e
	}
	var proof *cooperative.NativeFastModeRequest
	if binding != nil {
		proof = binding.Proof
	}
	request.Context.Host.NativeFastModeProof = proof
	request.ResolvedFastMode, e = h.options.Controller.ResolvedFastModeOnOwner(scope, proof)
	if e != nil {
		return result, e
	}
	deps := h.requestDependencies(scope)
	deps.NativeCompactSource = h.nativeCompactModels
	deps.APIAttemptBudget = in.Budget
	request.Context.Host.NativeCodexOAuth = deps.NativeCodexOAuth
	request.Context.Host.NativeGrokOAuth = deps.NativeGrokOAuth
	progress := compactProgressReporter{}
	_, e = modelyield.Run(ctx, request, modelyield.Dependencies{Request: deps, Activity: h.options.Activity, Session: h.sessionAccounting, AgentContext: h.agentContext, StreamClock: h.streamClock, StreamPolicy: h.streamPolicy, Emit: func(y modelyield.Yield) (requestretry.NoticeAction, error) {
		if y.Notice != nil {
			return requestretry.Resume, nil
		}
		if y.Update == nil {
			return requestretry.Resume, nil
		}
		if y.Update.Envelope != nil {
			serialized, err := y.Update.Envelope.MarshalJSON()
			if err != nil {
				return requestretry.Stop, err
			}
			if err = result.Record.UnmarshalJSON(serialized); err != nil {
				return requestretry.Stop, err
			}
			api, _ := result.Record.Object("message")
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(api.Get("content"), &blocks)
			var text strings.Builder
			for _, b := range blocks {
				if b.String("type") == "text" {
					text.WriteString(b.String("text"))
				}
			}
			result.Text = text.String()
		}
		if y.Update.Event != nil {
			var event protocol.JSONObject
			_ = json.Unmarshal(y.Update.Event, &event)
			if chars := progress.report(event, h.options.NowMillis); chars != nil {
				if err := h.compactStatus(scope, "compacting", chars); err != nil {
					return requestretry.Stop, err
				}
			}
		}
		return requestretry.Resume, nil
	}})
	if ctx.Err() != nil {
		return result, context.Cause(ctx)
	}
	return result, e
}

// fallback media placeholders only affect the new summary request view.
func stripCompactMedia(record protocol.JSONObject) protocol.JSONObject {
	if record.String("type") != "user" {
		return record
	}
	api, present := record.Object("message")
	if !present {
		return record
	}
	var walk func(json.RawMessage) json.RawMessage
	walk = func(rawValue json.RawMessage) json.RawMessage {
		var arr []json.RawMessage
		if json.Unmarshal(rawValue, &arr) == nil {
			for i, v := range arr {
				arr[i] = walk(v)
			}
			return raw(arr)
		}
		var object protocol.JSONObject
		if object.UnmarshalJSON(rawValue) != nil || !object.IsObject() {
			return rawValue
		}
		if object.String("type") == "image" || object.String("type") == "document" {
			return raw(map[string]string{"type": "text", "text": "[" + object.String("type") + "]"})
		}
		for _, f := range object.OwnProperties() {
			_ = object.SetPropertyJSON(f.NameJSON, walk(f.Value))
		}
		return raw(object)
	}
	api = api.Clone()
	api.Set("content", walk(api.Get("content")))
	record = record.Clone()
	record.Set("message", raw(api))
	return record
}
func (h *Host) processCompactInput(scope *cooperative.Scope, args string) (*sessioncontroller.ProcessedInput, error) {
	ctx := h.inputModelContext
	if ctx == nil {
		return nil, cap("compact input lifetime")
	}
	start := h.options.NowMillis()
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote("<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args>" + args + "</command-args>")}))
	caveat := h.options.Factory.CreateLocalCaveat()
	result, e := h.compactConversationOnOwner(ctx, scope, h.serial.History(), strings.TrimSpace(args), "", 0, false)
	if e != nil {
		return nil, e
	}
	text := "Compacted (ctrl+o to see full summary)"
	if result.Display != "" {
		text += "\n" + result.Display
	}
	stdout := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote("<local-command-stdout>" + text + "</local-command-stdout>"), "timestamp": quote(h.options.Now().Add(100 * time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z"))}))
	prefix := result.History[:2]
	if e = h.serial.ReplaceCompactHistory(scope, []queryloop.Message{{Record: prefix[0]}, {Record: prefix[1]}}); e != nil {
		return nil, e
	}
	commands := append([]protocol.JSONObject{caveat, user, stdout}, result.History[2:]...)
	public := append(append([]protocol.JSONObject{}, prefix...), commands...)
	return &sessioncontroller.ProcessedInput{Messages: commands, LocalResult: &sessioncontroller.LocalInputResult{Text: text, StartMillis: start, CompactHistory: public}}, nil
}

func (h *Host) emitCompactOutput(scope *cooperative.Scope, record protocol.JSONObject) error {
	output := record
	if record.String("type") == "system" && record.String("subtype") == "compact_boundary" {
		meta, _ := record.Object("compactMetadata")
		mapped := protocol.NewJSONObject(protocol.JSONField{Name: "trigger", Value: meta.Get("trigger")}, protocol.JSONField{Name: "pre_tokens", Value: meta.Get("preTokens")})
		if segment, present := meta.Object("preservedSegment"); present {
			mapped.Set("preserved_segment", raw(map[string]json.RawMessage{"head_uuid": segment.Get("headUuid"), "anchor_uuid": segment.Get("anchorUuid"), "tail_uuid": segment.Get("tailUuid")}))
		}
		output = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("compact_boundary")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "uuid", Value: record.Get("uuid")}, protocol.JSONField{Name: "compact_metadata", Value: raw(mapped)})
	} else if record.String("type") == "user" {
		api, _ := record.Object("message")
		compact := string(record.Get("isCompactSummary")) == "true"
		if !compact && !strings.Contains(api.String("content"), "<local-command-stdout>") {
			return nil
		}
		synthetic := string(record.Get("isMeta")) == "true" || string(record.Get("isVisibleInTranscriptOnly")) == "true"
		output = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "message", Value: raw(api)}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)}, protocol.JSONField{Name: "uuid", Value: record.Get("uuid")}, protocol.JSONField{Name: "timestamp", Value: record.Get("timestamp")}, protocol.JSONField{Name: "isReplay", Value: raw(!compact)}, protocol.JSONField{Name: "isSynthetic", Value: raw(synthetic)})
	} else {
		return nil
	}
	_, e := h.enqueue(scope, output)
	return e
}

// CompactHistoryOnOwner is shared by native message selectors and server
// callers; the caller supplies the original pivot and current abort lifetime.
func (h *Host) CompactHistoryOnOwner(ctx context.Context, scope *cooperative.Scope, pivot int, direction, feedback string) error {
	result, e := h.compactConversationOnOwner(ctx, scope, h.serial.History(), feedback, direction, pivot, false)
	if e != nil {
		return e
	}
	for _, record := range result.History {
		if e = h.emitCompactOutput(scope, record); e != nil {
			return e
		}
	}
	return h.serial.PersistCompactHistory(scope)
}

func (h *Host) RestoreConversationOnOwner(scope *cooperative.Scope, history []queryloop.Message) error {
	if h.options.Print == nil || !h.options.Print.NativeProcess || !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if h.options.Print.Tools != nil {
		if e := h.options.Print.Tools.ClearCompactCache(scope); e != nil {
			return e
		}
	}
	return h.serial.ReplaceCompactHistory(scope, history)
}
