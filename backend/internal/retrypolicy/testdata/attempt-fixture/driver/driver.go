package driver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

type Fault struct {
	Kind     string            `json:"kind"`
	SDKClass string            `json:"sdkClass"`
	Status   *int              `json:"status"`
	Message  *string           `json:"message"`
	Payload  json.RawMessage   `json:"payload"`
	Headers  map[string]string `json:"headers"`
	Code     *string           `json:"code"`
	Cause    *Fault            `json:"cause"`
}
type Options struct {
	Model                       *string         `json:"model"`
	ThinkingConfig              json.RawMessage `json:"thinkingConfig"`
	FastMode                    *bool           `json:"fastMode"`
	MaxRetries                  json.RawMessage `json:"maxRetries"`
	FallbackModel               string          `json:"fallbackModel"`
	QuerySource                 *string         `json:"querySource"`
	InitialConsecutive529Errors float64         `json:"initialConsecutive529Errors"`
}
type Input struct {
	Home            string            `json:"home"`
	Name            string            `json:"name"`
	Options         Options           `json:"options"`
	Env             map[string]string `json:"env"`
	Failures        []Fault           `json:"failures"`
	FactoryFailures []*Fault          `json:"factoryFailures"`
	Abort           string            `json:"abort"`
	StopAfterYield  bool              `json:"stopAfterYield"`
	Random          *float64          `json:"random"`
	Cooldown        *float64          `json:"cooldown"`
}
type nativeFailure struct {
	message string
	cause   error
	syntax  bool
}

func (failure *nativeFailure) Error() string { return failure.message }
func (failure *nativeFailure) Unwrap() error { return failure.cause }
func (failure *nativeFailure) EnumerableErrorJSON() (protocol.JSONObject, error) {
	return nativeProperties(failure, nil)
}

type codedFailure struct {
	*nativeFailure
	code string
}

func (failure *codedFailure) Code() string { return failure.code }
func (failure *codedFailure) EnumerableErrorJSON() (protocol.JSONObject, error) {
	return nativeProperties(failure.nativeFailure, &failure.code)
}
func nativeProperties(failure *nativeFailure, code *string) (protocol.JSONObject, error) {
	fields := protocol.NewJSONObject()
	if code != nil {
		raw, _ := json.Marshal(*code)
		fields.Set("code", protocol.StringifyValidatedJavaScriptJSON(raw))
	}
	if failure.cause != nil {
		native, ok := failure.cause.(retrypolicy.EnumerableFailure)
		if !ok {
			return protocol.JSONObject{}, errors.New("fixture native cause has an undeclared enumerable boundary")
		}
		cause, err := native.EnumerableErrorJSON()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		raw, err := cause.MarshalJSON()
		if err != nil {
			return protocol.JSONObject{}, err
		}
		fields.Set("cause", raw)
	}
	return fields, nil
}

func fault(spec Fault) error {
	var cause error
	if spec.Cause != nil {
		cause = fault(*spec.Cause)
	}
	message := ""
	if spec.Message != nil {
		message = *spec.Message
	}
	if spec.Kind == "native" || spec.Kind == "syntax" {
		failure := &nativeFailure{message: message, cause: cause, syntax: spec.Kind == "syntax"}
		if spec.Code != nil {
			return &codedFailure{nativeFailure: failure, code: *spec.Code}
		}
		return failure
	}
	class := spec.SDKClass
	if class == "" {
		class = "APIError"
	}
	if spec.Message == nil {
		switch class {
		case "APIConnectionError":
			message = "Connection error."
		case "APIConnectionTimeoutError":
			message = "Request timed out."
		case "APIUserAbortError":
			message = "Request was aborted."
		default:
			message = "(no status code or body)"
		}
	}
	var headers http.Header
	if spec.Headers != nil {
		headers = make(http.Header)
	}
	for name, value := range spec.Headers {
		headers.Set(name, value)
	}
	var requestID *string
	if value, exists := spec.Headers["request-id"]; exists {
		requestID = &value
	}
	return &provider.SDKStreamError{Class: class, Name: "Error", Message: message, Status: spec.Status, Payload: spec.Payload, Headers: headers, RequestID: requestID, Cause: cause}
}

func observedError(failure error) any {
	if failure == nil {
		return nil
	}
	class, name := "Error", "Error"
	var status, payload any
	headers := []any{}
	if sdk, ok := failure.(*provider.SDKStreamError); ok {
		class = sdk.Class
		if sdk.Name != "" {
			name = sdk.Name
		}
		if sdk.Status != nil {
			status = *sdk.Status
		}
		if len(sdk.Payload) > 0 {
			payload = sdk.Payload
		}
		names := []string{}
		for key := range sdk.Headers {
			names = append(names, strings.ToLower(key))
		}
		sort.Strings(names)
		for _, key := range names {
			headers = append(headers, []string{key, sdk.Headers.Get(key)})
		}
	} else if typed, ok := failure.(*nativeFailure); ok && typed.syntax {
		class, name = "SyntaxError", "SyntaxError"
	}
	var code any
	if coded, ok := failure.(interface{ Code() string }); ok {
		code = coded.Code()
	}
	return map[string]any{"class": class, "name": name, "message": failure.Error(), "status": status, "payload": payload, "headers": headers, "cause": observedError(errors.Unwrap(failure)), "code": code}
}
func number(value float64) any {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	if value == 0 {
		return 0
	}
	return value
}
func observedContext(value retrypolicy.RetryContext) map[string]any {
	result := map[string]any{"model": value.Model, "thinkingConfig": value.ThinkingConfig}
	if value.FastMode != nil {
		result["fastMode"] = *value.FastMode
	}
	if value.MaxTokensOverride != nil {
		result["maxTokensOverride"] = number(*value.MaxTokensOverride)
	}
	return result
}
func numeric(raw json.RawMessage) float64 {
	var value float64
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		panic("invalid explicit number")
	}
	switch text {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	panic("undeclared numeric string")
}

var stop = errors.New("consumer stopped")

// Observe uses only external client/operation faults and a deterministic clock.
// Retry decisions, branches, notices and state transitions are production Go.
func Observe(input Input) any {
	trace := []any{}
	clientCount, factoryCount, operationCount, uuidCount := 0, 0, 0, 0
	keepAliveDisabled := false
	var settingsFast, penguinOrg any = true, true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if input.Abort == "before" {
		cancel()
	}
	fast := &retrypolicy.FastState{CooldownUntil: input.Cooldown}
	if input.Cooldown != nil {
		fast.CooldownReason = "rate_limit"
	}
	model := "deepseek-flash"
	if input.Options.Model != nil {
		model = *input.Options.Model
	}
	thinking := input.Options.ThinkingConfig
	if len(thinking) == 0 {
		thinking = json.RawMessage(`{"type":"disabled"}`)
	}
	querySource := input.Options.QuerySource
	options := retrypolicy.AttemptOptions{Model: model, ThinkingConfig: thinking, FastMode: input.Options.FastMode, FallbackModel: input.Options.FallbackModel, QuerySource: querySource, InitialConsecutive529Errors: input.Options.InitialConsecutive529Errors}
	if len(input.Options.MaxRetries) > 0 {
		value := numeric(input.Options.MaxRetries)
		options.MaximumRetries = &value
	} else if _, exists := input.Env["CLAUDE_CODE_MAX_RETRIES"]; !exists {
		value := 4.0
		options.MaximumRetries = &value
	}
	host := retrypolicy.AttemptHost{Profile: retrypolicy.AttemptDirectRelayProfile, Env: input.Env, Fast: fast}
	deps := retrypolicy.AttemptDependencies[int]{
		GetClient: func(context.Context) (int, error) {
			clientCount++
			trace = append(trace, map[string]any{"kind": "client", "index": clientCount, "keepAliveDisabled": keepAliveDisabled})
			index := factoryCount
			factoryCount++
			if index < len(input.FactoryFailures) && input.FactoryFailures[index] != nil {
				return 0, fault(*input.FactoryFailures[index])
			}
			return clientCount, nil
		},
		Now:  func() time.Time { return time.UnixMilli(1700000000000) },
		UUID: func() string { uuidCount++; return "retry-attempt-id-" + strconv.Itoa(uuidCount) },
		Random: func() float64 {
			if input.Random != nil {
				return *input.Random
			}
			return 0.25
		},
		Yield: func(notice retrypolicy.RetryNotice) error {
			envelope, err := notice.Envelope()
			if err != nil {
				return err
			}
			wire, err := envelope.MarshalJSON()
			if err != nil {
				return err
			}
			trace = append(trace, map[string]any{"kind": "yield", "notice": map[string]any{"type": notice.Type, "subtype": notice.Subtype, "level": notice.Level, "cause": observedError(notice.Cause), "error": observedError(notice.Error), "retryInMs": number(notice.RetryInMillis), "retryAttempt": number(notice.RetryAttempt), "maxRetries": number(notice.MaxRetries), "timestamp": notice.Timestamp, "uuid": notice.UUID}, "wire": string(wire)})
			if input.Abort == "yield" {
				cancel()
			}
			if input.StopAfterYield {
				return stop
			}
			return nil
		},
		Sleep: func(ctx context.Context, ms float64) error {
			if ctx.Err() != nil {
				return &provider.SDKStreamError{Class: "APIUserAbortError", Name: "Error", Message: "Request was aborted."}
			}
			trace = append(trace, map[string]any{"kind": "sleep", "milliseconds": number(ms)})
			if input.Abort == "sleep" {
				cancel()
				return &provider.SDKStreamError{Class: "APIUserAbortError", Name: "Error", Message: "Request was aborted."}
			}
			return nil
		},
		InvalidateAPIKeyHelper: func() error { trace = append(trace, map[string]any{"kind": "invalidate"}); return nil },
		DisableKeepAlive:       func() error { keepAliveDisabled = true; return nil },
		FastEffect: func(effect retrypolicy.FastEffect) error {
			if effect.Permanent {
				settingsFast = nil
				penguinOrg = false
			}
			switch effect.Kind {
			case "cooldown":
				trace = append(trace, map[string]any{"kind": "cooldown", "resetAt": number(effect.ResetAt), "reason": effect.Reason})
			case "overage":
				trace = append(trace, map[string]any{"kind": "overage", "message": effect.Message})
			case "org_disabled":
				trace = append(trace, map[string]any{"kind": "org_disabled", "enabled": false})
			case "cooldown_expired":
				trace = append(trace, map[string]any{"kind": "cooldown_expired"})
			}
			return nil
		},
	}
	value, failure := retrypolicy.RunAttempts(ctx, options, host, deps, func(client int, attempt float64, retry *retrypolicy.RetryContext) (string, error) {
		trace = append(trace, map[string]any{"kind": "operation", "client": client, "attempt": number(attempt), "context": observedContext(*retry)})
		if input.Abort == "operation" {
			cancel()
		}
		index := operationCount
		operationCount++
		if index < len(input.Failures) {
			return "", fault(input.Failures[index])
		}
		return "success", nil
	})
	var result any
	switch typed := failure.(type) {
	case nil:
		result = map[string]any{"kind": "success", "value": value}
	case *retrypolicy.CannotRetryError:
		result = map[string]any{"kind": "cannot-retry", "name": "RetryError", "message": typed.Error(), "context": observedContext(typed.Context), "original": observedError(typed.Original)}
	case *retrypolicy.FallbackTriggeredError:
		result = map[string]any{"kind": "fallback", "name": "FallbackTriggeredError", "message": typed.Error(), "originalModel": typed.OriginalModel, "fallbackModel": typed.FallbackModel}
	default:
		if failure == stop {
			result = map[string]any{"kind": "consumer-stopped"}
		} else {
			result = map[string]any{"kind": "raw-error", "error": observedError(failure)}
		}
	}
	fastState, err := retrypolicy.ReadFastState(fast, deps.Now(), !truthy(input.Env["CLAUDE_CODE_DISABLE_FAST_MODE"]), deps.FastEffect)
	if err != nil {
		panic(err)
	}
	return map[string]any{"trace": trace, "result": result, "state": map[string]any{"fast": fastState, "settingsFastMode": settingsFast, "penguinModeOrgEnabled": penguinOrg, "keepAliveDisabled": keepAliveDisabled}, "uuidCalls": uuidCount}
}

func truthy(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "\ufeff")
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
