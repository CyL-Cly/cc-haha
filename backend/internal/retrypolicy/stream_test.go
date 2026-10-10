package retrypolicy

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type codedFailure struct {
	code    string
	message string
	cause   error
}

func (err *codedFailure) Error() string { return err.message }
func (err *codedFailure) Code() string  { return err.code }
func (err *codedFailure) Unwrap() error { return err.cause }

type causeFailure struct {
	message string
	cause   error
}

func (err *causeFailure) Error() string { return err.message }
func (err *causeFailure) Unwrap() error { return err.cause }

type nonComparableFailure []int

func (err nonComparableFailure) Error() string { return "non-comparable error" }

func TestAbsentFaultDoesNotInvokeTypedNilMethods(t *testing.T) {
	var api *provider.SDKStreamError
	var coded *codedFailure
	var cause *causeFailure
	for _, failure := range []error{nil, api, coded, cause, nonComparableFailure(nil)} {
		if rejected, err := HasPolicyRejection(failure); rejected || err != nil {
			t.Fatal(rejected, err)
		}
		if code, ok := ConnectionCode(failure); ok || code != "" {
			t.Fatal(code, ok)
		}
		if retry, err := IsRetryableStreamAPIError(failure); retry || err != nil {
			t.Fatal(retry, err)
		}
		if retry, err := ShouldRetryTransportDisconnect(failure, DisconnectState{}); retry || err != nil {
			t.Fatal(retry, err)
		}
	}
	// An absent cause stays absent rather than panicking during the cause walk.
	if retry, err := IsRetryableStreamTransportError(&causeFailure{cause: coded}); retry || err != nil {
		t.Fatal(retry, err)
	}
}

func TestReplayForbiddenOnceToolCommitsOrAnyAbortWins(t *testing.T) {
	bad := &provider.SDKStreamError{Payload: []byte(`{`), Class: "APIError", Message: `{"type":"api_error"}`}
	for _, state := range []DisconnectState{{HasCrossedSideEffectBoundary: true}, {StreamIdleAborted: true}, {SignalAborted: true}} {
		if retry, err := ShouldRetryTransportDisconnect(bad, state); retry || err != nil {
			t.Fatal("protected attempt evaluated policy body or became retryable", retry, err)
		}
	}
	for _, code := range []string{"ECONNRESET", "ECONNABORTED", "EPIPE", "UND_ERR_SOCKET", "ERR_STREAM_PREMATURE_CLOSE", "ETIMEDOUT", "", "econnreset"} {
		failure := &provider.SDKStreamError{Class: "APIConnectionError", Message: "Connection error.", Cause: &codedFailure{code: code}}
		retry, err := ShouldRetryTransportDisconnect(failure, DisconnectState{})
		want := code != "ETIMEDOUT" && code != "" && code != "econnreset"
		if err != nil || retry != want {
			t.Fatal(code, retry, err)
		}
	}
}

func TestCauseWalkIsBoundedAndUsesFirstCodeNotProse(t *testing.T) {
	leaf := &codedFailure{code: "EPIPE"}
	var failure error = leaf
	for index := 0; index < 4; index++ {
		failure = fmt.Errorf("wrapper: %w", failure)
	}
	if code, ok := ConnectionCode(failure); !ok || code != "EPIPE" {
		t.Fatal(code, ok)
	}
	if code, ok := ConnectionCode(fmt.Errorf("sixth: %w", failure)); ok || code != "" {
		t.Fatal("walk exceeded five Source objects", code, ok)
	}
	if code, ok := ConnectionCode(&codedFailure{code: "ignored-first-code", cause: leaf}); !ok || code != "ignored-first-code" {
		t.Fatal(code, ok)
	}
	cycle := &causeFailure{message: "ECONNRESET is only prose"}
	cycle.cause = cycle
	for _, value := range []error{nil, cycle, errors.New("ECONNRESET"), nonComparableFailure{1, 2}} {
		if code, ok := ConnectionCode(value); ok || code != "" {
			t.Fatal(code, ok)
		}
		if rejected, err := HasPolicyRejection(value); rejected || err != nil {
			t.Fatal(rejected, err)
		}
	}
}

func TestExplicitPolicyCodeAlwaysStopsAPIAndSocketRetries(t *testing.T) {
	for _, body := range []string{
		`{"code":"cyber_policy"}`,
		`{"response":{"originalError":{"error":{"code":"content_policy_violation"}}}}`,
		`{"cause":{"message":"429 {\"error\":{\"code\":\"content_policy\"}}"}}`,
		`{"code":123,"error":{"code":"cyber_policy"}}`,
		`{"message":"\ufeff{\"code\":\"cyber_policy\"}"}`,
		`{"message":"429\u00a0{\"code\":\"cyber_policy\"}"}`,
	} {
		failure := &provider.SDKStreamError{Class: "APIError", Message: `{"type":"api_error"}`, Payload: []byte(body), Cause: &codedFailure{code: "EPIPE"}}
		if rejected, err := HasPolicyRejection(failure); !rejected || err != nil {
			t.Fatal(body, rejected, err)
		}
		if retry, err := IsRetryableStreamAPIError(failure); retry || err != nil {
			t.Fatal(body, retry, err)
		}
		if retry, err := IsRetryableStreamTransportError(failure); retry || err != nil {
			t.Fatal(body, retry, err)
		}
	}
	if rejected, err := HasPolicyRejection(&codedFailure{code: "cyber_policy"}); !rejected || err != nil {
		t.Fatal(rejected, err)
	}
	if rejected, err := HasPolicyRejection(&causeFailure{cause: &codedFailure{code: "content_policy"}}); !rejected || err != nil {
		t.Fatal(rejected, err)
	}
}

func TestAPIClassAndExactSerializedTypeOwnStreamRetries(t *testing.T) {
	for _, body := range []string{`{"type":"api_error"}`, `500 {"type":"overloaded_error"}`} {
		if retry, err := IsRetryableStreamAPIError(&provider.SDKStreamError{Class: "APIError", Message: body}); !retry || err != nil {
			t.Fatal(retry, err)
		}
	}
	for _, class := range []string{"APIError", "APIUserAbortError", "APIConnectionError", "APIConnectionTimeoutError", "BadRequestError", "AuthenticationError", "PermissionDeniedError", "NotFoundError", "ConflictError", "UnprocessableEntityError", "RateLimitError", "InternalServerError", "", "FutureError", "AnthropicError", "Error"} {
		retry, err := IsRetryableStreamAPIError(&provider.SDKStreamError{Class: class, Message: `{"type":"api_error"}`})
		if err != nil || retry != (class != "" && class != "FutureError" && class != "AnthropicError" && class != "Error") {
			t.Fatal(class, retry, err)
		}
	}
	for _, failure := range []error{nil, errors.New(`{"type":"api_error"}`), &provider.SDKStreamError{Class: "SyntaxError", Message: `{"type":"api_error"}`}, &provider.SDKStreamError{Class: "APIError", Message: "api_error"}, &provider.SDKStreamError{Class: "APIError", Message: `{"type": "api_error"}`}, &provider.SDKStreamError{Class: "APIError", Message: `{invalid JSON`}} {
		if retry, err := IsRetryableStreamAPIError(failure); retry || err != nil {
			t.Fatal(retry, err)
		}
	}
	for _, message := range []string{"content_policy is prose", `{"message":"cyber_policy"}`, `{"code":"other"}`, `{"code":"cyber_policy"} {}`, `{not valid json`, "\u0085{\"code\":\"cyber_policy\"}"} {
		if rejected, err := HasPolicyRejection(errors.New(message)); rejected || err != nil {
			t.Fatal(message, rejected, err)
		}
	}
	// Twelve property edges are eligible; deeper rejection is outside the
	// Source classifier's scope, independently of the transport cause limit.
	for _, depth := range []int{11, 12} {
		body := strings.Repeat(`{"error":`, depth) + `{"code":"cyber_policy"}` + strings.Repeat("}", depth)
		rejected, err := HasPolicyRejection(&provider.SDKStreamError{Payload: []byte(body)})
		if err != nil || rejected != (depth == 11) {
			t.Fatal(depth, rejected, err)
		}
	}
}

func TestUnsupportedParsedPayloadFailsInsteadOfAllowingResend(t *testing.T) {
	deep := `{"future":` + strings.Repeat("[", 10001) + `0` + strings.Repeat("]", 10001) + `,"code":"cyber_policy"}`
	for _, failure := range []error{&provider.SDKStreamError{Payload: []byte(`{} {}`)}, &provider.SDKStreamError{Payload: []byte(`{`)}, errors.New(deep)} {
		if rejected, err := HasPolicyRejection(failure); rejected || !errors.Is(err, ErrPolicyPayloadProfile) {
			t.Fatal(rejected, err)
		}
		if retry, err := IsRetryableStreamAPIError(failure); retry || !errors.Is(err, ErrPolicyPayloadProfile) {
			t.Fatal(retry, err)
		}
		if retry, err := IsRetryableStreamTransportError(failure); retry || !errors.Is(err, ErrPolicyPayloadProfile) {
			t.Fatal(retry, err)
		}
	}
}
