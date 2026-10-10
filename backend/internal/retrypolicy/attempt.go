package retrypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const AttemptDirectRelayProfile = "release-direct-relay-empty-oauth-no-model-overrides"

// AttemptHost is a session/process snapshot. The declared release profile has
// no OAuth subscription, cloud credential providers or internal build gates.
// The scheduler itself owns fast-mode and error decisions; dependencies do not
// receive a precomputed Source decision.
type AttemptHost struct {
	Profile string
	Env     map[string]string
	Fast    *FastState
}

type FastState struct {
	mu             sync.Mutex
	CooldownUntil  *float64
	CooldownReason string
	OrgDisabled    bool
	expiryNotified bool
}

type FastSnapshot struct {
	Status  string   `json:"status"`
	ResetAt *float64 `json:"resetAt,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// ReadFastState retains Source's lazy expiration and notification order. A
// notification may synchronously read the state again without notifying twice.
func ReadFastState(state *FastState, now time.Time, enabled bool, emit func(FastEffect) error) (FastSnapshot, error) {
	if state == nil {
		return FastSnapshot{}, missingAttempt("process fast state")
	}
	state.mu.Lock()
	expired := state.CooldownUntil != nil && float64(now.UnixMilli()) >= *state.CooldownUntil
	notify := expired && enabled && !state.expiryNotified
	if notify {
		state.expiryNotified = true
	}
	state.mu.Unlock()
	if notify {
		if emit == nil {
			return FastSnapshot{}, missingAttempt("fast cooldown expiration notification")
		}
		if err := emit(FastEffect{Kind: "cooldown_expired"}); err != nil {
			return FastSnapshot{}, err
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if expired {
		state.CooldownUntil = nil
		state.CooldownReason = ""
	}
	if state.CooldownUntil == nil {
		return FastSnapshot{Status: "active"}, nil
	}
	reset := *state.CooldownUntil
	return FastSnapshot{Status: "cooldown", ResetAt: &reset, Reason: state.CooldownReason}, nil
}
func (state *FastState) setCooldown(reset float64, reason string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.CooldownUntil = &reset
	state.CooldownReason = reason
	state.expiryNotified = false
}

// ClearFastModeCooldown is Source's typed-model switch runtime producer. It
// resets only cooldown; org policy and persisted user preference are retained.
func (state *FastState) ClearFastModeCooldown() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.CooldownUntil = nil
	state.CooldownReason = ""
	state.expiryNotified = false
}
func (state *FastState) disableOrg() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.OrgDisabled {
		return false
	}
	state.OrgDisabled = true
	return true
}

type RetryContext struct {
	Model             string          `json:"model"`
	ThinkingConfig    json.RawMessage `json:"thinkingConfig"`
	FastMode          *bool           `json:"fastMode,omitempty"`
	MaxTokensOverride *float64        `json:"maxTokensOverride,omitempty"`
}

type AttemptOptions struct {
	Model                       string
	ThinkingConfig              json.RawMessage
	FastMode                    *bool
	MaximumRetries              *float64
	FallbackModel               string
	QuerySource                 *string
	InitialConsecutive529Errors float64
}

// FastEffect carries an already-decided persistence or notification operation.
// The host must implement it using its real settings/notification boundary.
type FastEffect struct {
	Kind      string
	Reason    string
	Message   string
	Permanent bool
	ResetAt   float64
}

type RetryNotice struct {
	Type          string                   `json:"type"`
	Subtype       string                   `json:"subtype"`
	Level         string                   `json:"level"`
	Cause         error                    `json:"-"`
	Error         *provider.SDKStreamError `json:"-"`
	RetryInMillis float64                  `json:"retryInMs"`
	RetryAttempt  float64                  `json:"retryAttempt"`
	MaxRetries    float64                  `json:"maxRetries"`
	Timestamp     string                   `json:"timestamp"`
	UUID          string                   `json:"uuid"`
}

type AttemptDependencies[C any] struct {
	GetClient func(context.Context) (C, error)
	Now       func() time.Time
	UUID      func() string
	Random    func() float64
	// Yield must wait until the consumer advances, as an async-generator yield
	// does. No sleep or next attempt occurs before this call returns.
	Yield                  func(RetryNotice) error
	Sleep                  func(context.Context, float64) error
	InvalidateAPIKeyHelper func() error
	DisableKeepAlive       func() error
	FastEffect             func(FastEffect) error
}

type CannotRetryError struct {
	Original error
	Context  RetryContext
}

func (failure *CannotRetryError) Error() string {
	if nilFailure(failure.Original) {
		return "undefined"
	}
	return failure.Original.Error()
}
func (failure *CannotRetryError) Unwrap() error { return failure.Original }

type FallbackTriggeredError struct{ OriginalModel, FallbackModel string }

func (failure *FallbackTriggeredError) Error() string {
	return fmt.Sprintf("Model fallback triggered: %s -> %s", failure.OriginalModel, failure.FallbackModel)
}

type AttemptCapabilityError struct{ Boundary string }

func (failure *AttemptCapabilityError) Error() string {
	return "retry attempt capability is not migrated: " + failure.Boundary
}
func missingAttempt(boundary string) error { return &AttemptCapabilityError{Boundary: boundary} }

func apiFailure(failure error) *provider.SDKStreamError {
	sdk, ok := failure.(*provider.SDKStreamError)
	if !ok || sdk == nil {
		return nil
	}
	switch sdk.Class {
	case "APIError", "APIUserAbortError", "APIConnectionError", "APIConnectionTimeoutError", "BadRequestError", "AuthenticationError", "PermissionDeniedError", "NotFoundError", "ConflictError", "UnprocessableEntityError", "RateLimitError", "InternalServerError":
		return sdk
	}
	return nil
}
func statusIs(failure error, status int) bool {
	sdk := apiFailure(failure)
	return sdk != nil && sdk.Status != nil && *sdk.Status == status
}
func is529(failure error) bool {
	sdk := apiFailure(failure)
	return sdk != nil && (statusIs(sdk, 529) || strings.Contains(sdk.Message, `"type":"overloaded_error"`))
}
func revoked(failure error) bool {
	sdk := apiFailure(failure)
	return sdk != nil && statusIs(sdk, 403) && strings.Contains(sdk.Message, "OAuth token has been revoked")
}
func connectionFailure(failure error) bool {
	sdk := apiFailure(failure)
	return sdk != nil && (sdk.Class == "APIConnectionError" || sdk.Class == "APIConnectionTimeoutError")
}

var foreground529 = map[string]bool{
	"repl_main_thread": true, "repl_main_thread:outputStyle:custom": true, "repl_main_thread:outputStyle:Explanatory": true, "repl_main_thread:outputStyle:Learning": true,
	"sdk": true, "agent:custom": true, "agent:default": true, "agent:builtin": true, "compact": true, "hook_agent": true, "hook_prompt": true, "verification_agent": true, "side_question": true, "auto_mode": true,
}
var directOpusModels = map[string]bool{
	"claude-opus-4-20250514": true, "claude-opus-4-1-20250805": true, "claude-opus-4-5-20251101": true,
	"claude-opus-4-7": true, "claude-opus-4-8": true, "claude-opus-5": true, "claude-opus-5-5": true,
}
var overflowPattern = regexp.MustCompile("input length and `max_tokens` exceed context limit: ([0-9]+) \\+ ([0-9]+) > ([0-9]+)")

const jsSpacePattern = "[\\t-\\r \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]"

var unauthorizedOverflow = regexp.MustCompile("prompt is too long|input is too long for requested model|context_length_exceeded|maximum context length|exceeds? the context window|context window exceeded|supports only [0-9]+" + jsSpacePattern + "*k?" + jsSpacePattern + "*(?:tokens?" + jsSpacePattern + "+of" + jsSpacePattern + "+)?context")

func asciiLower(text string) string {
	return strings.Map(func(value rune) rune {
		if value >= 'A' && value <= 'Z' {
			return value + 'a' - 'A'
		}
		return value
	}, text)
}

func contextOverflow(failure error) (input, max, limit float64, ok bool) {
	sdk := apiFailure(failure)
	if sdk == nil || !statusIs(sdk, 400) {
		return
	}
	match := overflowPattern.FindStringSubmatch(sdk.Message)
	if len(match) != 4 {
		return
	}
	return decimalPrefix(match[1]), decimalPrefix(match[2]), decimalPrefix(match[3]), true
}
func retryAfter(failure error) *string {
	sdk := apiFailure(failure)
	if sdk == nil {
		return nil
	}
	if value, exists, err := sdk.HeaderValue("retry-after"); err == nil && exists {
		return &value
	}
	return nil
}
func envTruthy(value string) bool {
	value = strings.ToLower(strings.TrimFunc(value, jsWhitespace))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
func userAbort() error {
	return &provider.SDKStreamError{Kind: "caller_abort", Class: "APIUserAbortError", Name: "Error", Message: "Request was aborted."}
}

func shouldRetryAttempt(failure error, host AttemptHost, invalidate func() error) (bool, error) {
	sdk := apiFailure(failure)
	if sdk == nil {
		return false, nil
	}
	if envTruthy(host.Env["CLAUDE_CODE_REMOTE"]) && (statusIs(sdk, 401) || statusIs(sdk, 403)) {
		return true, nil
	}
	if strings.Contains(sdk.Message, `"type":"overloaded_error"`) {
		return true, nil
	}
	if _, _, _, ok := contextOverflow(sdk); ok {
		return true, nil
	}
	header := sdk.Headers.Get("x-should-retry")
	if sdk.OriginalHeaders != nil {
		header, _, _ = sdk.HeaderValue("x-should-retry")
	}
	if header == "true" {
		return true, nil
	} // Empty OAuth profile is not a subscriber.
	if header == "false" {
		return false, nil
	}
	if connectionFailure(sdk) {
		return true, nil
	}
	if sdk.Status == nil || *sdk.Status == 0 {
		return false, nil
	}
	if statusIs(sdk, 408) || statusIs(sdk, 409) || statusIs(sdk, 429) {
		return true, nil
	}
	if statusIs(sdk, 401) {
		// Source /i without /u does not fold non-ASCII Kelvin sign into K.
		if unauthorizedOverflow.MatchString(asciiLower(sdk.Message)) {
			return false, nil
		}
		if invalidate == nil {
			return false, missingAttempt("API key helper cache invalidation")
		}
		if err := invalidate(); err != nil {
			return false, err
		}
		return true, nil
	}
	return revoked(sdk) || *sdk.Status >= 500, nil
}

func checkAttemptProfile[C any](host AttemptHost, deps AttemptDependencies[C]) error {
	if host.Profile != AttemptDirectRelayProfile {
		return missingAttempt("undeclared host profile")
	}
	for name := range host.Env {
		switch name {
		case "CLAUDE_CODE_MAX_RETRIES", "CLAUDE_CODE_DISABLE_FAST_MODE", "FALLBACK_FOR_ALL_PRIMARY_MODELS", "USER_TYPE", "IS_SANDBOX", "CLAUDE_CODE_REMOTE":
		default:
			return missingAttempt("environment " + name)
		}
	}
	if value := host.Env["USER_TYPE"]; value != "" && value != "external" {
		return missingAttempt("internal mock/build gates")
	}
	if host.Fast == nil {
		return missingAttempt("process fast state")
	}
	if deps.GetClient == nil || deps.Now == nil || deps.UUID == nil || deps.Random == nil || deps.Yield == nil || deps.Sleep == nil {
		return missingAttempt("client/clock/ID/RNG/yield/sleep dependencies")
	}
	return nil
}

// RunAttempts ports withRetry around request creation. It does not retry stream
// consumption or a committed tool operation. The operation receives the same
// mutable retry context on each attempt, including overflow max_tokens changes.
func RunAttempts[C any, T any](ctx context.Context, options AttemptOptions, host AttemptHost, deps AttemptDependencies[C], operation func(C, float64, *RetryContext) (T, error)) (T, error) {
	var zero T
	if err := checkAttemptProfile(host, deps); err != nil {
		return zero, err
	}
	if ctx == nil || operation == nil {
		return zero, missingAttempt("context/operation")
	}
	maximum := DefaultMaximum(host.Env["CLAUDE_CODE_MAX_RETRIES"])
	if options.MaximumRetries != nil {
		maximum = *options.MaximumRetries
	}
	retry := RetryContext{Model: options.Model, ThinkingConfig: append(json.RawMessage(nil), options.ThinkingConfig...)}
	fastEnabled := !envTruthy(host.Env["CLAUDE_CODE_DISABLE_FAST_MODE"])
	if fastEnabled {
		retry.FastMode = options.FastMode
	}
	var client C
	hasClient := false
	consecutive := options.InitialConsecutive529Errors
	var last error
	cannot := func(failure error) error { return &CannotRetryError{Original: failure, Context: retry} }
	for attempt := 1.0; attempt <= maximum+1; attempt++ {
		if ctx.Err() != nil {
			return zero, userAbort()
		}
		wasFast := fastEnabled && retry.FastMode != nil && *retry.FastMode
		if wasFast {
			snapshot, err := ReadFastState(host.Fast, deps.Now(), fastEnabled, deps.FastEffect)
			if err != nil {
				return zero, err
			}
			wasFast = snapshot.Status != "cooldown"
		}
		stale := false
		if connectionFailure(last) {
			code, _ := ConnectionCode(last)
			stale = code == "ECONNRESET" || code == "EPIPE"
		}
		var failure error
		if stale {
			if deps.DisableKeepAlive == nil {
				failure = missingAttempt("sticky keepalive disable")
			} else {
				failure = deps.DisableKeepAlive()
			}
		}
		if failure == nil && (!hasClient || statusIs(last, 401) || revoked(last) || stale) {
			var replacement C
			replacement, failure = deps.GetClient(ctx)
			if failure == nil {
				// A rejected rebuild does not overwrite the previous client in
				// the Source's client = await getClient() assignment.
				client, hasClient = replacement, true
			}
		}
		if failure == nil {
			value, err := operation(client, attempt, &retry)
			if err == nil {
				return value, nil
			}
			failure = err
		}
		if rejected, err := HasPolicyRejection(failure); err != nil {
			return zero, err
		} else if rejected {
			return zero, cannot(failure)
		}
		last = failure
		sdk := apiFailure(failure)
		if wasFast && sdk != nil && (statusIs(sdk, 429) || is529(sdk)) {
			if overage, exists := sdk.Headers["Anthropic-Ratelimit-Unified-Overage-Disabled-Reason"]; exists {
				reason := strings.Join(overage, ", ")
				if deps.FastEffect == nil {
					return zero, missingAttempt("fast overage persistence/notification")
				}
				if err := deps.FastEffect(FastEffect{Kind: "overage", Reason: reason, Message: overageMessage(reason), Permanent: reason != "org_level_disabled_until" && reason != "out_of_credits"}); err != nil {
					return zero, err
				}
				value := false
				retry.FastMode = &value
				continue
			}
			hint := retryAfter(sdk)
			wait := math.NaN()
			if hint != nil && *hint != "" {
				seconds := decimalPrefix(*hint)
				if !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
					wait = math.Max(BaseDelayMillis, seconds*1000)
				}
			}
			if !math.IsNaN(wait) && wait < 20000 {
				if err := deps.Sleep(ctx, wait); err != nil {
					return zero, err
				}
				continue
			}
			if math.IsNaN(wait) {
				wait = 1800000
			}
			reset := float64(deps.Now().UnixMilli()) + math.Max(wait, 600000)
			reason := "rate_limit"
			if is529(sdk) {
				reason = "overloaded"
			}
			host.Fast.setCooldown(reset, reason)
			if deps.FastEffect == nil {
				return zero, missingAttempt("fast cooldown notification")
			}
			if err := deps.FastEffect(FastEffect{Kind: "cooldown", ResetAt: reset, Reason: reason}); err != nil {
				return zero, err
			}
			value := false
			retry.FastMode = &value
			continue
		}
		if wasFast && sdk != nil && statusIs(sdk, 400) && strings.Contains(sdk.Message, "Fast mode is not enabled") {
			if host.Fast.disableOrg() {
				if deps.FastEffect == nil {
					return zero, missingAttempt("fast rejection persistence/notification")
				}
				if err := deps.FastEffect(FastEffect{Kind: "org_disabled", Permanent: true}); err != nil {
					return zero, err
				}
			}
			value := false
			retry.FastMode = &value
			continue
		}
		if is529(failure) && options.QuerySource != nil && !foreground529[*options.QuerySource] {
			return zero, cannot(failure)
		}
		if is529(failure) && (host.Env["FALLBACK_FOR_ALL_PRIMARY_MODELS"] != "" || directOpusModels[options.Model]) {
			consecutive++
			if consecutive >= 3 {
				if options.FallbackModel != "" {
					return zero, &FallbackTriggeredError{OriginalModel: options.Model, FallbackModel: options.FallbackModel}
				}
				if host.Env["USER_TYPE"] == "external" && host.Env["IS_SANDBOX"] == "" {
					return zero, cannot(errors.New("Repeated 529 Overloaded errors"))
				}
			}
		}
		if attempt > maximum {
			return zero, cannot(failure)
		}
		retryable, err := shouldRetryAttempt(failure, host, deps.InvalidateAPIKeyHelper)
		if err != nil {
			return zero, err
		}
		if !retryable {
			return zero, cannot(failure)
		}
		if input, _, limit, ok := contextOverflow(failure); ok {
			available := math.Max(0, limit-input-1000)
			if available < 3000 {
				return zero, failure
			}
			if retry.MaxTokensOverride != nil && available >= *retry.MaxTokensOverride {
				return zero, cannot(failure)
			}
			retry.MaxTokensOverride = &available
			continue
		}
		delay := Delay(attempt, retryAfter(failure), 32000, deps.Random)
		if sdk != nil {
			notice := RetryNotice{Type: "system", Subtype: "api_error", Level: "error", Cause: sdk.Cause, Error: sdk, RetryInMillis: delay, RetryAttempt: attempt, MaxRetries: maximum, Timestamp: deps.Now().UTC().Format("2006-01-02T15:04:05.000Z"), UUID: deps.UUID()}
			if err := deps.Yield(notice); err != nil {
				return zero, err
			}
		}
		if err := deps.Sleep(ctx, delay); err != nil {
			return zero, err
		}
	}
	return zero, cannot(last)
}

func overageMessage(reason string) string {
	switch reason {
	case "out_of_credits":
		return "Fast mode disabled · extra usage credits exhausted"
	case "org_level_disabled", "org_service_level_disabled":
		return "Fast mode disabled · extra usage disabled by your organization"
	case "org_level_disabled_until":
		return "Fast mode disabled · extra usage spending cap reached"
	case "member_level_disabled":
		return "Fast mode disabled · extra usage disabled for your account"
	case "seat_tier_level_disabled", "seat_tier_zero_credit_limit", "member_zero_credit_limit":
		return "Fast mode disabled · extra usage not available for your plan"
	case "overage_not_provisioned", "no_limits_configured":
		return "Fast mode requires extra usage billing · /extra-usage to enable"
	default:
		return "Fast mode disabled · extra usage not available"
	}
}

// SleepAttempt implements the production abort-aware waiting boundary. Replay
// may replace its clock, but scheduler tests still execute the real RunAttempts.
func SleepAttempt(ctx context.Context, milliseconds float64) error {
	if ctx.Err() != nil {
		return userAbort()
	}
	// Bun's timer boundary uses a one-ms minimum for nonfinite, overflow or
	// sub-ms values. Delay itself intentionally retains its original number.
	if math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds < 1 || milliseconds > 2147483647 {
		milliseconds = 1
	}
	timer := time.NewTimer(time.Duration(math.Trunc(milliseconds)) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return userAbort()
	case <-timer.C:
		return nil
	}
}
