package queryhooks

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// SessionEndTimeout preserves parseInt(base10), then finite/strict-positive.
func SessionEndTimeout(raw string) time.Duration {
	raw = strings.TrimLeftFunc(raw, javaScriptWhitespace)
	sign := ""
	if strings.HasPrefix(raw, "+") || strings.HasPrefix(raw, "-") {
		sign = raw[:1]
		raw = raw[1:]
	}
	end := 0
	for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
		end++
	}
	if end == 0 {
		return 1500 * time.Millisecond
	}
	value, err := strconv.ParseFloat(sign+raw[:end], 64)
	if err != nil || math.IsInf(value, 0) || value <= 0 {
		return 1500 * time.Millisecond
	}
	// time.Duration cannot represent Source's unbounded finite number; retain
	// a bounded native deadline rather than overflow into an immediate timeout.
	if value > float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(value) * time.Millisecond
}

func (s *Service) ExecuteSessionEnd(ctx context.Context, reason string, call Context, timeout time.Duration, writeFailure func(json.RawMessage) error) error {
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: nil}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "reason", Value: commandJSON(reason)})
	results, err := s.ExecuteOutside(ctx, OutsideInput{Event: "SessionEnd", SessionID: s.mainSessionID(), MatchQuery: reason, Fields: fields, Call: call, Timeout: timeout})
	if err != nil {
		return err
	}
	for _, result := range results {
		if !result.Succeeded && hookTruthy(result.Output) && writeFailure != nil {
			command, err := protocol.DecodeJavaScriptJSONString(result.Command)
			if err != nil {
				return err
			}
			output, err := protocol.DecodeJavaScriptJSONString(result.Output)
			if err != nil {
				return err
			}
			units := utf16.Encode([]rune("SessionEnd hook ["))
			units = append(units, command...)
			units = append(units, utf16.Encode([]rune("] failed: "))...)
			units = append(units, output...)
			line := protocol.QuoteJavaScriptUTF16(append(units, '\n'))
			if err = writeFailure(line); err != nil {
				return err
			}
		}
	}
	s.ClearSession(s.mainSessionID())
	s.ClearNativeAgentHooks(s.mainSessionID())
	return nil
}
