package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type InsightsRequest struct {
	Prompt    json.RawMessage
	Model     string
	MaxTokens float64
}
type InsightsResponse struct {
	Text json.RawMessage
	Err  error
}

// QueryInsightsBatch retains Promise.all's independent requests and input
// ordering. Every model iterator and result mutation stays on the same Owner.
// The admitted Insights authority, rather than a generic Read callback, owns
// the auxiliary model stack. No model or mutable result crosses to a worker.
func (h *Host) QueryInsightsBatch(ctx context.Context, scope *cooperative.Scope, requests []InsightsRequest) ([]InsightsResponse, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || !h.nativeInsightsModels.Owns(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	results := make([]InsightsResponse, len(requests))
	if len(requests) == 0 {
		return results, nil
	}
	done := make(chan struct{})
	remaining := len(requests)
	for i, request := range requests {
		i, request := i, request
		request.Prompt = append(json.RawMessage(nil), request.Prompt...)
		if _, err := h.options.Owner.Microtask(cooperative.Read, func(caller *cooperative.Scope) error {
			text, err := h.QueryInsights(ctx, caller, request.Prompt, request.Model, request.MaxTokens)
			results[i] = InsightsResponse{Text: append(json.RawMessage(nil), text...), Err: err}
			remaining--
			if remaining == 0 {
				close(done)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if err := scope.AwaitPromise(done); err != nil {
		return nil, err
	}
	return results, nil
}
