package builtintools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source validates the returned content after the filesystem await, before
// mutating the read cache. The model counter itself must await on this owner;
// calling it in the filesystem goroutine loses the native continuation.
func (s *Service) validateReadTokens(ctx context.Context, scope *cooperative.Scope, content string, divisor, maxTokens float64) error {
	estimate := math.Floor(float64(len(utf16.Encode([]rune(content))))/divisor + 0.5)
	if estimate == 0 || estimate <= maxTokens/4 {
		return nil
	}
	var count *float64
	var err error
	if s.options.CountTokensOnOwner != nil {
		count, err = s.options.CountTokensOnOwner(ctx, scope, content)
	} else if s.options.CountTokens != nil {
		// Preserve the legacy primitive seam, but give its blocking work the
		// same actual NativeBinding/settlement lifetime as other native I/O.
		work, failure := native(s, scope, func() (*int, error) { return s.options.CountTokens(ctx, content) })
		if failure != nil {
			return failure
		}
		value, failure := work.Await(scope)
		err = failure
		if value != nil {
			n := float64(*value)
			count = &n
		}
	} else {
		return capability("large Read actual token-count API")
	}
	if err != nil {
		return err
	}
	if count != nil {
		estimate = *count
	}
	if estimate > maxTokens {
		return fmt.Errorf("File content (%s tokens) exceeds maximum allowed tokens (%s). Use offset and limit parameters to read specific portions of the file, or search for specific content instead of reading the whole file.", readTokenNumber(estimate), readTokenNumber(maxTokens))
	}
	return nil
}

func readTokenNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	encoded, _ := json.Marshal(value)
	text, _ := protocol.JoinJavaScriptValues([]json.RawMessage{encoded}, "")
	return text
}
