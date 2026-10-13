package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// NativeAutoDreamQuery receives the actual root cache snapshot and the fork's
// own abort controller. It does not create another Controller or transcript.
type NativeAutoDreamQuery func(context.Context, string, string, func(protocol.JSONObject) error) error

func (h *Host) startNativeAutoDream(scope *cooperative.Scope) func(queryhooks.CacheSafeParams) error {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.NativeAutoDream == nil || h.isAgent() || (h.querySource() != "sdk" && h.querySource() != "repl_main_thread") || costAuthEnvTruthy(h.options.OriginalEnv["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	return func(cache queryhooks.CacheSafeParams) error {
		if h.sideQuestionCandidate == nil {
			return cap("auto-dream actual root cache-safe context")
		}
		snapshot := *h.sideQuestionCandidate
		snapshot.raw.Context.SystemPrompt = append([]json.RawMessage{}, snapshot.raw.Context.SystemPrompt...)
		if prompt, ok := cache.Context.SystemPrompt.([]json.RawMessage); ok {
			snapshot.raw.Context.SystemPrompt = append([]json.RawMessage{}, prompt...)
		}
		snapshot.request.Context.History = []protocol.JSONObject{}
		for _, message := range cache.Messages {
			data, err := message.MarshalJSON()
			if err != nil {
				return err
			}
			var value protocol.JSONObject
			if err = value.UnmarshalJSON(data); err != nil {
				return err
			}
			snapshot.request.Context.History = append(snapshot.request.Context.History, value)
		}
		snapshot.request.Context.Options.QuerySource = "auto_dream"
		snapshot.request.Context.Options.SkipCacheWrite = false
		// runForkedAgent supplies neither the caller's task budget nor a compact
		// attempt budget. These controls are per query, outside cache-safe params.
		snapshot.request.Context.Options.TaskBudget = nil
		return h.nativeAutoDreamModels.Start(scope, context.Background(), func(next *cooperative.Scope) error {
			return h.options.Print.NativeAutoDream(next, cache, func(ctx context.Context, prompt, memoryRoot string, progress func(protocol.JSONObject) error) error {
				return h.queryAutoDream(ctx, next, snapshot, prompt, memoryRoot, progress)
			})
		}, func(next *cooperative.Scope, err error) {
			if err != nil && h.options.UnhandledRecord != nil {
				h.options.UnhandledRecord(next, err)
			}
		})
	}
}

func (h *Host) CloseAutoDream(ctx context.Context) error {
	if h == nil || h.options.Print == nil || h.options.Print.CloseNativeAutoDream == nil {
		return nil
	}
	if err := h.options.Print.CloseNativeAutoDream(ctx); err != nil {
		return err
	}
	return h.nativeAutoDreamModels.Join(ctx)
}

func (h *Host) AppendAutoDreamMemorySaved(scope *cooperative.Scope, record protocol.JSONObject) error {
	if !h.nativeAutoDreamModels.Current(scope) || h.closing {
		return cooperative.ErrRestricted
	}
	future, err := h.serial.AppendNativeMemorySaved(scope, record)
	if err != nil {
		return err
	}
	h.observeRecord(scope, future)
	return nil
}

func (h *Host) autoDreamFork(scope *cooperative.Scope) (*Host, error) {
	if !h.nativeAutoDreamModels.Current(scope) {
		return nil, cooperative.ErrRestricted
	}
	tools, err := h.options.Print.Tools.WithAutoMemoryFork(scope)
	if err != nil {
		return nil, err
	}
	_, implementations, err := tools.Catalog(scope)
	if err != nil {
		return nil, err
	}
	fork := *h
	print := *h.options.Print
	print.Tools = tools
	print.NativeAutoDream, print.CloseNativeAutoDream = nil, nil
	print.AgentID, print.QuerySource = "", "auto_dream"
	print.NativeTeammateStop, print.NativeLeaderInbox = nil, nil
	fork.options.Print = &print
	fork.askSkillOverrides = nil
	fork.registered = append([]queryloop.RegisteredTool{}, h.registered...)
	for index, original := range fork.registered {
		for _, replacement := range implementations {
			if replacement.Name == original.Name {
				fork.registered[index] = replacement
				break
			}
		}
	}
	fork.nativePermissionHookContext = nil
	return &fork, nil
}
