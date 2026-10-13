package requestretry

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"math"
	"sync"
)

type apiAttemptBudgetExhausted struct{}

func (apiAttemptBudgetExhausted) Error() string { return "No API attempts left for this request" }

// This producer has Source's ordinary Error.message, without SDK HTTP fields.
func (e apiAttemptBudgetExhausted) APIMessage() string { return e.Error() }

var ErrCompactBudgetExhausted error = apiAttemptBudgetExhausted{}

// APIAttemptBudget is shared across cache fork, fallback and PTL retries. Every
// retry-loop attempt spends one token before GetClient; retries cannot reset it.
type APIAttemptBudget struct {
	mu        sync.Mutex
	remaining float64
}

func NewCompactAttemptBudget(env map[string]string) *APIAttemptBudget {
	retries := retrypolicy.DefaultMaximum(env["CLAUDE_CODE_MAX_RETRIES"])
	if math.IsNaN(retries) || math.IsInf(retries, 0) || retries < 0 {
		retries = 10
	}
	return &APIAttemptBudget{remaining: retries + 1}
}
func (b *APIAttemptBudget) Consume() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return ErrCompactBudgetExhausted
	}
	b.remaining--
	return nil
}
func (b *APIAttemptBudget) Remaining() float64 {
	if b == nil {
		return math.Inf(1)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}
