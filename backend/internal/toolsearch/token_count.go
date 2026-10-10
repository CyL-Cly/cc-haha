package toolsearch

import (
	"context"
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type tokenCount struct {
	done  chan struct{}
	value *float64
}

// Source memoizes the promise by the comma-joined names in original catalogue
// order, without model/description/schema in the key. Failure and zero are
// memoized as null, while a nonzero count below the overhead becomes zero.
func (s *Service) deferredTokenCount(ctx context.Context, tools []Tool, model string, inputs GateInputs) (*float64, error) {
	names := []json.RawMessage{}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	keyRaw, e := protocol.JoinJavaScriptValuesJSON(names, ",")
	if e != nil {
		return nil, e
	}
	key := string(keyRaw)
	s.tokenMu.Lock()
	entry, exists := s.tokens[key]
	if !exists {
		if inputs.CountDefinitionTokens == nil {
			s.tokenMu.Unlock()
			return nil, &CapabilityError{"original auto-threshold token-count producer"}
		}
		entry = &tokenCount{done: make(chan struct{})}
		s.tokens[key] = entry
	}
	s.tokenMu.Unlock()
	if exists {
		select {
		case <-entry.done:
		default:
			if inputs.AwaitTokenCount == nil {
				return nil, &CapabilityError{"original memoized token-count promise owner wait"}
			}
			if e := inputs.AwaitTokenCount(ctx, entry.done); e != nil {
				return nil, e
			}
			select {
			case <-entry.done:
			default:
				return nil, &CapabilityError{"token-count wait returned before original promise settled"}
			}
		}
	} else {
		count, e := inputs.CountDefinitionTokens(ctx, tools, model)
		if e == nil && count != nil && *count != 0 {
			value := math.Max(0, *count-500)
			entry.value = &value
		}
		close(entry.done)
	}
	if entry.value == nil {
		return nil, nil
	}
	copy := *entry.value
	return &copy, nil
}
