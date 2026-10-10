package retrypolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

var ErrPolicyPayloadProfile = errors.New("unsupported policy payload JSON profile")

func nilFailure(failure error) bool {
	if failure == nil {
		return true
	}
	value := reflect.ValueOf(failure)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// HasPolicyRejection checks explicit upstream policy codes before any retry.
// Structured SDK payloads and ordinary error messages/cause chains are retained;
// unfamiliar JSON grammar fails explicitly instead of permitting a resend.
func HasPolicyRejection(failure error) (bool, error) {
	seen := make(map[error]bool)
	var visitError func(error, int) (bool, error)
	var visitValue func(any, int) (bool, error)
	visitValue = func(value any, depth int) (bool, error) {
		if depth > 12 {
			return false, nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return false, nil
		}
		if code, ok := object["code"].(string); ok && (code == "cyber_policy" || code == "content_policy" || code == "content_policy_violation") {
			return true, nil
		}
		for _, key := range []string{"error", "response", "originalError", "cause"} {
			if found, err := visitValue(object[key], depth+1); found || err != nil {
				return found, err
			}
		}
		if message, ok := object["message"].(string); ok {
			if decoded, ok, err := policyMessage(message); err != nil {
				return false, err
			} else if ok {
				return visitValue(decoded, depth+1)
			}
		}
		return false, nil
	}
	visitError = func(failure error, depth int) (bool, error) {
		if nilFailure(failure) || depth > 12 {
			return false, nil
		}
		// A cause can cycle; value errors need not be comparable in Go.
		if reflect.TypeOf(failure).Comparable() {
			if seen[failure] {
				return false, nil
			}
			seen[failure] = true
		}
		if coded, ok := failure.(interface{ Code() string }); ok && policyCode(coded.Code()) {
			return true, nil
		}
		if sdk, ok := failure.(*provider.SDKStreamError); ok && sdk.Payload != nil {
			decoded, err := policyPayload(sdk.Payload)
			if err != nil {
				return false, fmt.Errorf("%w: %v", ErrPolicyPayloadProfile, err)
			}
			if found, err := visitValue(decoded, depth+1); found || err != nil {
				return found, err
			}
		}
		if found, err := visitError(errors.Unwrap(failure), depth+1); found || err != nil {
			return found, err
		}
		if decoded, ok, err := policyMessage(failure.Error()); err != nil {
			return false, err
		} else if ok {
			return visitValue(decoded, depth+1)
		}
		return false, nil
	}
	return visitError(failure, 0)
}

func policyPayload(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("policy body contains extra JSON input")
	}
	return value, nil
}

func policyMessage(message string) (any, bool, error) {
	if len(message) > 3 && message[0] >= '0' && message[0] <= '9' && message[1] >= '0' && message[1] <= '9' && message[2] >= '0' && message[2] <= '9' {
		trimmed := strings.TrimLeftFunc(message[3:], jsWhitespace)
		if len(trimmed) != len(message[3:]) {
			message = trimmed
		}
	}
	message = strings.TrimFunc(message, jsWhitespace)
	if !strings.HasPrefix(message, "{") {
		return nil, false, nil
	}
	value, err := policyPayload([]byte(message))
	if err != nil && strings.Contains(err.Error(), "exceeded max depth") {
		return nil, false, fmt.Errorf("%w: %v", ErrPolicyPayloadProfile, err)
	}
	return value, err == nil, nil
}

func policyCode(code string) bool {
	return code == "cyber_policy" || code == "content_policy" || code == "content_policy_violation"
}

// ConnectionCode follows only native error causes, bounded to the Source's five
// visited error objects. Transports provide Code() at their fault boundary;
// arbitrary prose is never converted into a disconnect code.
func ConnectionCode(failure error) (string, bool) {
	for depth := 0; !nilFailure(failure) && depth < 5; depth++ {
		if coded, ok := failure.(interface{ Code() string }); ok {
			return coded.Code(), true
		}
		next := errors.Unwrap(failure)
		if next != nil && reflect.TypeOf(failure).Comparable() && reflect.TypeOf(next).Comparable() && next == failure {
			break
		}
		failure = next
	}
	return "", false
}

func IsRetryableStreamAPIError(failure error) (bool, error) {
	if rejected, err := HasPolicyRejection(failure); rejected || err != nil {
		return false, err
	}
	sdk, ok := failure.(*provider.SDKStreamError)
	if !ok || sdk == nil || sdk.Kind == "syntax" || sdk.Class == "SyntaxError" {
		return false, nil
	}
	// The representation is public. Unknown future classes cannot stand in for
	// Source instanceof APIError merely because they use the same Go record.
	switch sdk.Class {
	case "APIError", "APIUserAbortError", "APIConnectionError", "APIConnectionTimeoutError", "BadRequestError", "AuthenticationError", "PermissionDeniedError", "NotFoundError", "ConflictError", "UnprocessableEntityError", "RateLimitError", "InternalServerError":
	default:
		return false, nil
	}
	return strings.Contains(sdk.Message, `"type":"api_error"`) || strings.Contains(sdk.Message, `"type":"overloaded_error"`), nil
}

func IsRetryableStreamTransportError(failure error) (bool, error) {
	if rejected, err := HasPolicyRejection(failure); rejected || err != nil {
		return false, err
	}
	code, exists := ConnectionCode(failure)
	if !exists {
		return false, nil
	}
	switch code {
	case "ECONNRESET", "ECONNABORTED", "EPIPE", "UND_ERR_SOCKET", "ERR_STREAM_PREMATURE_CLOSE":
		return true, nil
	default:
		return false, nil
	}
}

type DisconnectState struct {
	HasCrossedSideEffectBoundary bool
	StreamIdleAborted            bool
	SignalAborted                bool
}

func ShouldRetryTransportDisconnect(failure error, state DisconnectState) (bool, error) {
	// Keep Source short-circuit order: committed tools, watchdog and caller abort
	// skip even inspecting the fault. A consumer must never replay these attempts.
	if state.HasCrossedSideEffectBoundary || state.StreamIdleAborted || state.SignalAborted {
		return false, nil
	}
	return IsRetryableStreamTransportError(failure)
}
