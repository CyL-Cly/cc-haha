package sdkhost

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

type nativeSDKSuggestionAttempt struct {
	ctx     context.Context
	cancel  context.CancelFunc
	settled *OwnerFuture[struct{}]
}

// Model and output state are Owner-only. mu also guards the off-owner Close
// cancellation/publication boundary, never a Scope or an in-flight query.
type nativeSDKSuggestions struct {
	mu              sync.Mutex
	models          *cooperative.NativeSideQuestionModels
	enabled, closed bool
	current         *nativeSDKSuggestionAttempt
	pending         *protocol.JSONObject
}

func newNativeSDKSuggestions(scope *cooperative.Scope, owner *cooperative.Owner) (*nativeSDKSuggestions, error) {
	models, err := cooperative.NewNativeSideQuestionModels(scope, owner)
	if err != nil {
		return nil, err
	}
	return &nativeSDKSuggestions{models: models}, nil
}

func (h *Host) cancelSDKPromptSuggestion() {
	s := h.sdkSuggestions
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.current != nil {
		s.current.cancel()
	}
	s.pending = nil
	s.mu.Unlock()
}

// The Source print IIFE catches generation failures. They never replace an
// already emitted ordinary result or stop its next input consumer.
func (h *Host) launchSDKPromptSuggestion(scope *cooperative.Scope) {
	if err := h.startSDKPromptSuggestion(scope); err != nil {
		h.logSDKPromptSuggestionError(err)
	}
}

func (h *Host) logSDKPromptSuggestionError(err error) {
	if err != nil && h.options.Diagnostics != nil {
		value := protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: quote(err.Error())})
		_ = h.options.Diagnostics.Log("error", "prompt_suggestion_generation", &value)
	}
}

func (h *Host) startSDKPromptSuggestion(scope *cooperative.Scope) error {
	s := h.sdkSuggestions
	if s == nil || h.isAgent() || !s.enabled {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if value := strings.ToLower(protocol.TrimJavaScriptString(h.options.OriginalEnv["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"])); value == "0" || value == "false" || value == "no" || value == "off" {
		return nil
	}
	h.cancelSDKPromptSuggestion()
	if h.sideQuestionCache == nil {
		return nil
	}
	history := h.serial.History()
	assistants := 0
	var lastMessage queryloop.Message
	for _, message := range history {
		if message.Type() == "assistant" {
			assistants++
			lastMessage = message
		}
	}
	if assistants < 2 {
		return nil
	}
	data, err := lastMessage.MarshalJSON()
	if err != nil {
		return err
	}
	var last protocol.JSONObject
	if err := last.UnmarshalJSON(data); err != nil {
		return err
	}
	if openaiauth.AuthTruthy(last.Get("isApiErrorMessage")) {
		return nil
	}
	message, _ := last.Object("message")
	usage, _ := message.Object("usage")
	uncached := float64(0)
	for _, field := range []string{"input_tokens", "cache_creation_input_tokens", "output_tokens"} {
		var count float64
		_ = json.Unmarshal(usage.Get(field), &count)
		uncached += count
	}
	if uncached > 10000 {
		return nil
	}
	permission, err := h.NativePermissionContext(scope)
	if err != nil {
		return err
	}
	if permission.String("mode") == "plan" || len(h.sandboxPending) > 0 || h.sdkPermissions != nil && len(h.sdkPermissions.pending) > 0 || h.sdkHooks != nil && len(h.sdkHooks.permissionRequests) > 0 {
		return nil
	}
	// The real process's quota/elicitation state is sampled by its producer,
	// rather than substituting raw utilization or a fixed 'allowed' status.
	if h.options.Print.NativeSuggestionSuppression != nil {
		reason, err := h.options.Print.NativeSuggestionSuppression(scope)
		if err != nil {
			return err
		}
		if reason != "" {
			return nil
		}
	}
	snapshot := *h.sideQuestionCache
	snapshot.request.Context.History = []protocol.JSONObject{}
	for _, message := range history {
		data, err := message.MarshalJSON()
		if err != nil {
			return err
		}
		var value protocol.JSONObject
		if err := value.UnmarshalJSON(data); err != nil {
			return err
		}
		snapshot.request.Context.History = append(snapshot.request.Context.History, value)
	}
	snapshot.request.Context.Options.QuerySource = "prompt_suggestion"
	snapshot.request.Context.Options.SkipCacheWrite = true
	snapshot.request.Context.Options.TaskBudget = nil
	ctx, cancel := context.WithCancel(context.Background())
	settled, err := NewOwnerFuture[struct{}](h.options.Owner)
	if err != nil {
		cancel()
		return err
	}
	attempt := &nativeSDKSuggestionAttempt{ctx: ctx, cancel: cancel, settled: settled}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return cooperative.ErrClosed
	}
	s.current = attempt
	s.mu.Unlock()
	err = s.models.Start(scope, ctx, func(next *cooperative.Scope) error {
		records, err := h.querySDKPromptSuggestion(ctx, next, snapshot, sdkSuggestionPrompt)
		if err != nil || ctx.Err() != nil {
			return err
		}
		text := sdkSuggestionText(records)
		if sdkSuggestionFilteredJSON(text) {
			return nil
		}
		output := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("prompt_suggestion")}, protocol.JSONField{Name: "suggestion", Value: text}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())})
		s.mu.Lock()
		live := !s.closed && s.current == attempt && ctx.Err() == nil
		if live && h.heldBackResult.IsObject() {
			s.pending = &output
			live = false
		}
		s.mu.Unlock()
		if live {
			_, err = h.enqueue(next, output)
		}
		return err
	}, func(next *cooperative.Scope, failure error) {
		if ctx.Err() == nil {
			h.logSDKPromptSuggestionError(failure)
		}
		cancel()
		s.mu.Lock()
		if s.current == attempt {
			s.current = nil
		}
		s.mu.Unlock()
		_ = settled.Resolve(next, struct{}{})
	})
	if err != nil {
		cancel()
		s.mu.Lock()
		if s.current == attempt {
			s.current = nil
		}
		s.mu.Unlock()
		_ = settled.Resolve(scope, struct{}{})
	}
	return err
}

func (h *Host) flushSDKPromptSuggestion(scope *cooperative.Scope) error {
	s := h.sdkSuggestions
	if s == nil {
		return nil
	}
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	closed := s.closed
	s.mu.Unlock()
	if closed || pending == nil {
		return nil
	}
	_, err := h.enqueue(scope, *pending)
	return err
}

func (h *Host) finishSDKPromptSuggestionInput(scope *cooperative.Scope) error {
	s := h.sdkSuggestions
	if s == nil {
		return nil
	}
	s.mu.Lock()
	attempt := s.current
	s.mu.Unlock()
	if attempt == nil {
		return nil
	}
	ready := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(ready) }) }
	_, err := attempt.settled.Then(scope, func(*cooperative.Scope, struct{}, error) error { finish(); return nil })
	if err != nil {
		return err
	}
	timer, err := scope.ArmTimer(5*time.Second, finish)
	if err != nil {
		return err
	}
	defer scope.ClearTimer(timer)
	if err := scope.AwaitPromise(ready); err != nil {
		return err
	}
	attempt.cancel()
	return nil
}

func (h *Host) closeSDKPromptSuggestions(ctx context.Context) error {
	s := h.sdkSuggestions
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	if s.current != nil {
		s.current.cancel()
	}
	s.mu.Unlock()
	return s.models.Join(ctx)
}

func (h *Host) sdkSuggestionFork(scope *cooperative.Scope) (*Host, func(*cooperative.Scope) error, error) {
	if h.sdkSuggestions == nil || !h.sdkSuggestions.models.Current(scope) {
		return nil, nil, cooperative.ErrRestricted
	}
	fork := *h
	print := *h.options.Print
	release := func(*cooperative.Scope) error { return nil }
	if print.Tools != nil {
		tools, cleanup, err := print.Tools.WithDeniedToolFork(scope)
		if err != nil {
			return nil, nil, err
		}
		release = cleanup
		_, implementations, err := tools.Catalog(scope)
		if err != nil {
			_ = release(scope)
			return nil, nil, err
		}
		fork.registered = append([]queryloop.RegisteredTool{}, h.registered...)
		for i, original := range fork.registered {
			for _, replacement := range implementations {
				if original.Name == replacement.Name {
					fork.registered[i] = replacement
					break
				}
			}
		}
		print.Tools = tools
	}
	print.AgentID, print.QuerySource = "", "prompt_suggestion"
	print.NativeAutoDream, print.CloseNativeAutoDream = nil, nil
	print.NativeTeammateStop, print.NativeLeaderInbox = nil, nil
	fork.options.Print = &print
	fork.askSkillOverrides = nil
	fork.nativePermissionHookContext = nil
	return &fork, release, nil
}

func (h *Host) bindSDKPromptSuggestionFastMode(scope *cooperative.Scope, input requestretry.Input) (requestretry.Input, error) {
	if h.sdkSuggestions == nil || !h.sdkSuggestions.models.Current(scope) {
		return requestretry.Input{}, cooperative.ErrRestricted
	}
	binding, err := h.nativeFastModeBinding(scope)
	if err != nil {
		return requestretry.Input{}, err
	}
	var proof *cooperative.NativeFastModeRequest
	if binding != nil {
		proof = binding.Proof
	}
	input.Context.Host.NativeFastModeProof = proof
	input.ResolvedFastMode, err = h.options.Controller.ResolvedFastModeOnOwner(scope, proof)
	return input, err
}
