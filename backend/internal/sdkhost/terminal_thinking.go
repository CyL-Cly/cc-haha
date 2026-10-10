package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type terminalThinkingState struct{ original requestcontext.Thinking }

func cloneThinking(value requestcontext.Thinking) requestcontext.Thinking {
	if value.BudgetTokens != nil {
		budget := *value.BudgetTokens
		value.BudgetTokens = &budget
	}
	return value
}

func (h *Host) TerminalThinking(scope *cooperative.Scope) (requestcontext.Thinking, bool, error) {
	if e := h.checkInferenceOwner(scope); e != nil {
		return requestcontext.Thinking{}, false, e
	}
	value := cloneThinking(h.options.Print.Thinking)
	if value.Type == "" {
		value.Type = "adaptive"
	}
	mid := false
	for _, message := range h.serial.History() {
		if message.Type() == "assistant" {
			mid = true
			break
		}
	}
	return value, mid, nil
}

// The REPL stores a per-session enable switch. Re-enabling restores the
// original startup config (including legacy MAX_THINKING_TOKENS); an original
// disabled config becomes adaptive. Neither settings nor global config writes.
func (h *Host) SetTerminalThinking(scope *cooperative.Scope, enabled bool) error {
	if e := h.checkInferenceOwner(scope); e != nil {
		return e
	}
	if h.terminalThinking == nil {
		h.terminalThinking = &terminalThinkingState{original: cloneThinking(h.options.Print.Thinking)}
	}
	value := requestcontext.Thinking{Type: "disabled"}
	if enabled {
		value = cloneThinking(h.terminalThinking.original)
		if value.Type == "" || value.Type == "disabled" {
			value = requestcontext.Thinking{Type: "adaptive"}
		}
	}
	h.options.Print.Thinking = value
	return nil
}
