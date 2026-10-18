package sdkhost

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

// NativeAgentSummaryBindings belongs to an actual AgentTool child. Load reads
// its sidechain from the original disk loader; Update uses its parent task map.
// Neither callback can start a replacement model or create a transcript.
type NativeAgentSummaryBindings struct {
	Load   func(context.Context, *cooperative.Scope) ([]protocol.JSONObject, error)
	Update func(*cooperative.Scope, json.RawMessage) error
}

type nativeAgentSummary struct {
	mu              sync.Mutex
	models          *cooperative.NativeSideQuestionModels
	started, closed bool
	cancel          context.CancelFunc
}

func newNativeAgentSummary(scope *cooperative.Scope, h *Host) (*nativeAgentSummary, error) {
	if h.options.Print == nil || !h.options.Print.NativeProcess || !h.isAgent() || h.options.Print.NativeAgentSummary == nil || h.options.Print.NativeAgentSummary.Load == nil || h.options.Print.NativeAgentSummary.Update == nil {
		return nil, cooperative.ErrRestricted
	}
	models, err := cooperative.NewNativeSideQuestionModels(scope, h.options.Owner)
	if err != nil {
		return nil, err
	}
	return &nativeAgentSummary{models: models}, nil
}

func (h *Host) startAgentSummary(scope *cooperative.Scope, snapshot nativeSideQuestionCache) error {
	s := h.agentSummary
	if s == nil || !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if s.started {
		return nil
	}
	// Source deliberately drops initial forkContextMessages from this closure.
	snapshot.request.Context.History = nil
	snapshot.raw.Context.History = nil
	snapshot.request.Context.Options.QuerySource = "agent_summary"
	snapshot.request.Context.Options.SkipCacheWrite = false
	snapshot.request.Context.Options.TaskBudget = nil
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return cooperative.ErrClosed
	}
	s.cancel, s.started = cancel, true
	s.mu.Unlock()
	err := s.models.Start(scope, ctx, func(next *cooperative.Scope) error {
		var previous json.RawMessage
		for ctx.Err() == nil {
			if err := waitNativeAgentSummary(next, ctx); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			summary, err := h.runAgentSummary(next, ctx, snapshot, previous)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				h.logNativeAgentSummaryError(err)
			} else if summary != nil {
				// Source retains previousSummary even when the running task disappeared.
				previous = append(json.RawMessage(nil), summary...)
				if err := h.options.Print.NativeAgentSummary.Update(next, summary); err != nil {
					h.logNativeAgentSummaryError(err)
				}
			}
			// Schedule the next actual 30-second timer after completion, never overlap.
		}
		return nil
	}, func(_ *cooperative.Scope, failure error) {
		if ctx.Err() == nil {
			h.logNativeAgentSummaryError(failure)
		}
		cancel()
	})
	if err != nil {
		cancel()
	}
	return err
}

func waitNativeAgentSummary(scope *cooperative.Scope, ctx context.Context) error {
	ready := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(ready) }) }
	timer, err := scope.ArmTimer(30*time.Second, finish)
	if err != nil {
		return err
	}
	// Stop only closes an immutable wake channel. Policy and ClearTimer remain
	// on this exact Owner continuation, including its canceled finally path.
	stop := context.AfterFunc(ctx, finish)
	defer stop()
	defer scope.ClearTimer(timer)
	return scope.AwaitPromise(ready)
}

func (h *Host) runAgentSummary(scope *cooperative.Scope, ctx context.Context, snapshot nativeSideQuestionCache, previous json.RawMessage) (json.RawMessage, error) {
	rows, err := h.options.Print.NativeAgentSummary.Load(ctx, scope)
	if err != nil || ctx.Err() != nil {
		return nil, err
	}
	if len(rows) < 3 {
		return nil, nil
	}
	clean, err := teamspawn.FilterIncompleteContext(rows)
	if err != nil {
		return nil, err
	}
	snapshot.request.Context.History = clean
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	prompt, err := nativeAgentSummaryPrompt(previous)
	if err != nil {
		return nil, err
	}
	records, err := h.queryNativeAgentSummary(queryCtx, scope, snapshot, prompt)
	if err != nil || ctx.Err() != nil {
		return nil, err
	}
	return nativeAgentSummaryText(records), nil
}

func nativeAgentSummaryText(records []protocol.JSONObject) json.RawMessage {
	for _, record := range records {
		if record.String("type") != "assistant" || openaiauth.AuthTruthy(record.Get("isApiErrorMessage")) {
			continue
		}
		message, _ := record.Object("message")
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.String("type") != "text" {
				continue
			}
			units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if err != nil {
				break
			}
			for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
				units = units[1:]
			}
			for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
				units = units[:len(units)-1]
			}
			if len(units) > 0 {
				return protocol.QuoteJavaScriptUTF16(units)
			}
			break
		}
	}
	return nil
}

func (h *Host) logNativeAgentSummaryError(err error) {
	if err != nil && h.options.Diagnostics != nil {
		value := protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: quote(err.Error())})
		_ = h.options.Diagnostics.Log("error", "agent_summary", &value)
	}
}

// Close is off-Owner administration. A canceled HTTP/read is physically joined
// by its existing query/future before the continuation can leave models.Join.
func (h *Host) CloseAgentSummaries(ctx context.Context) error {
	if h == nil || h.agentSummary == nil {
		return nil
	}
	s := h.agentSummary
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	return s.models.Join(ctx)
}
