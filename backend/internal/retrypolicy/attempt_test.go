package retrypolicy

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func attemptDependencies() AttemptDependencies[int] {
	return AttemptDependencies[int]{GetClient: func(context.Context) (int, error) { return 1, nil }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, UUID: func() string { return "test-id" }, Random: func() float64 { return 0 }, Yield: func(RetryNotice) error { return nil }, Sleep: func(context.Context, float64) error { return nil }, InvalidateAPIKeyHelper: func() error { return nil }, DisableKeepAlive: func() error { return nil }, FastEffect: func(FastEffect) error { return nil }}
}
func attemptOptions() AttemptOptions {
	return AttemptOptions{Model: "deepseek-flash", ThinkingConfig: json.RawMessage(`{"type":"disabled"}`)}
}
func attemptHost() AttemptHost {
	return AttemptHost{Profile: AttemptDirectRelayProfile, Fast: &FastState{}, Env: map[string]string{}}
}
func statusFailure(status int) *provider.SDKStreamError {
	return &provider.SDKStreamError{Class: "APIError", Status: &status, Message: "upstream"}
}

func TestAttemptReadsMutatedOriginalHeadersInsteadOfPolicyMap(t *testing.T) {
	headers, _ := provider.NewConstructorHeaders([]provider.HeaderPair{{Name: "retry-after", Value: "9"}, {Name: "x-should-retry", Value: "true"}})
	status := 500
	var failure *provider.SDKStreamError
	if !errors.As(provider.NewSDKResponseError(&status, `{"message":"upstream"}`, headers, true), &failure) {
		t.Fatal("missing SDK error")
	}
	_ = headers.Set("retry-after", "19")
	operations := 0
	deps := attemptDependencies()
	deps.Sleep = func(_ context.Context, value float64) error {
		if value != 19000 {
			t.Fatalf("stale retry hint: %v", value)
		}
		return nil
	}
	deps.Yield = func(notice RetryNotice) error {
		if notice.RetryInMillis != 19000 {
			t.Fatalf("stale notice hint: %v", notice.RetryInMillis)
		}
		return nil
	}
	value, err := RunAttempts(context.Background(), attemptOptions(), attemptHost(), deps, func(int, float64, *RetryContext) (string, error) {
		operations++
		if operations == 1 {
			return "", failure
		}
		return "ok", nil
	})
	if err != nil || value != "ok" || operations != 2 {
		t.Fatalf("actual attempt %q %v %d", value, err, operations)
	}
	_ = headers.Set("x-should-retry", "false")
	if retry, err := shouldRetryAttempt(failure, attemptHost(), func() error { return nil }); err != nil || retry {
		t.Fatal("mutated stop header ignored", retry, err)
	}
	_ = headers.Delete("retry-after")
	if retryAfter(failure) != nil {
		t.Fatal("deleted original resurrected from map snapshot")
	}
}

func TestAttemptYieldIsAConsumerBarrier(t *testing.T) {
	deps := attemptDependencies()
	entered, resume := make(chan struct{}), make(chan struct{})
	complete := make(chan error, 1)
	operations, sleeps := 0, 0
	deps.Yield = func(RetryNotice) error { close(entered); <-resume; return nil }
	deps.Sleep = func(context.Context, float64) error { sleeps++; return nil }
	go func() {
		_, err := RunAttempts(context.Background(), attemptOptions(), attemptHost(), deps, func(int, float64, *RetryContext) (string, error) {
			operations++
			if operations == 1 {
				return "", statusFailure(500)
			}
			return "ok", nil
		})
		complete <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("retry did not yield")
	}
	if operations != 1 || sleeps != 0 {
		t.Fatalf("advanced through suspended yield: operations=%d sleeps=%d", operations, sleeps)
	}
	close(resume)
	select {
	case err := <-complete:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not complete")
	}
	if operations != 2 || sleeps != 1 {
		t.Fatalf("operations=%d sleeps=%d", operations, sleeps)
	}
}
func TestActualSleepCancelsAfterNoticeWithoutResubmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := attemptDependencies()
	deps.Sleep = SleepAttempt
	deps.Yield = func(RetryNotice) error { cancel(); return nil }
	operations := 0
	_, err := RunAttempts(ctx, attemptOptions(), attemptHost(), deps, func(int, float64, *RetryContext) (string, error) { operations++; return "", statusFailure(500) })
	if sdk, ok := err.(*provider.SDKStreamError); !ok || sdk.Class != "APIUserAbortError" {
		t.Fatalf("error=%T %v", err, err)
	}
	if operations != 1 {
		t.Fatalf("resubmitted after abort: %d", operations)
	}
}
func TestActualSleepEndsOrAbortsAtTheHostTimerBoundary(t *testing.T) {
	for _, value := range []float64{0, -1, 0.5, 1, math.NaN(), math.Inf(1), math.Inf(-1), 2147483648} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := SleepAttempt(ctx, value); err != nil {
			t.Errorf("timer %v: %v", value, err)
		}
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepAttempt(ctx, 10); err == nil {
		t.Fatal("aborted sleep completed")
	}
	ctx, cancel = context.WithCancel(context.Background())
	entered := make(chan struct{})
	go func() { close(entered); cancel() }()
	<-entered
	if err := SleepAttempt(ctx, 10000); err == nil {
		t.Fatal("cancel during sleep completed")
	}
}
func TestAttemptCapabilityChecksDoNotCallTheClient(t *testing.T) {
	for _, mutate := range []func(*AttemptHost, *AttemptDependencies[int]){
		func(host *AttemptHost, _ *AttemptDependencies[int]) { host.Profile = "" },
		func(host *AttemptHost, _ *AttemptDependencies[int]) { host.Fast = nil },
		func(host *AttemptHost, _ *AttemptDependencies[int]) { host.Env["CLAUDE_CODE_USE_BEDROCK"] = "1" },
		func(host *AttemptHost, _ *AttemptDependencies[int]) { host.Env["USER_TYPE"] = "ant" },
		func(_ *AttemptHost, deps *AttemptDependencies[int]) { deps.UUID = nil },
	} {
		host, deps := attemptHost(), attemptDependencies()
		called := false
		deps.GetClient = func(context.Context) (int, error) { called = true; return 1, nil }
		mutate(&host, &deps)
		_, err := RunAttempts(context.Background(), attemptOptions(), host, deps, func(int, float64, *RetryContext) (string, error) { return "", nil })
		var capability *AttemptCapabilityError
		if !errors.As(err, &capability) || called {
			t.Fatalf("capability=%v client called=%v", err, called)
		}
	}
	for _, invalidContext := range []bool{true, false} {
		ctx := context.Background()
		var operation func(int, float64, *RetryContext) (string, error)
		if invalidContext {
			ctx = nil
			operation = func(int, float64, *RetryContext) (string, error) { return "", nil }
		}
		if _, err := RunAttempts(ctx, attemptOptions(), attemptHost(), attemptDependencies(), operation); err == nil {
			t.Fatal("invalid operation/context accepted")
		}
	}
}
func TestAttemptCallbackFailureIsNotHiddenByAnotherRequest(t *testing.T) {
	boundary := errors.New("host callback failed")
	for _, kind := range []string{"yield", "sleep", "invalidate", "cooldown", "overage", "rejection"} {
		t.Run(kind, func(t *testing.T) {
			deps := attemptDependencies()
			options := attemptOptions()
			failure := statusFailure(500)
			switch kind {
			case "yield":
				deps.Yield = func(RetryNotice) error { return boundary }
			case "sleep":
				deps.Sleep = func(context.Context, float64) error { return boundary }
			case "invalidate":
				failure = statusFailure(401)
				deps.InvalidateAPIKeyHelper = func() error { return boundary }
			default:
				value := true
				options.FastMode = &value
				deps.FastEffect = func(FastEffect) error { return boundary }
				failure = statusFailure(429)
				if kind == "overage" {
					failure.Headers = map[string][]string{"Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": {"out_of_credits"}}
				}
				if kind == "rejection" {
					failure = statusFailure(400)
					failure.Message = "Fast mode is not enabled"
				}
			}
			operations := 0
			_, err := RunAttempts(context.Background(), options, attemptHost(), deps, func(int, float64, *RetryContext) (string, error) { operations++; return "", failure })
			if !errors.Is(err, boundary) || operations != 1 {
				t.Fatalf("error=%v operations=%d", err, operations)
			}
		})
	}
}
func TestNativeSDKRepresentationDoesNotPromoteUnknownErrors(t *testing.T) {
	for _, failure := range []error{nil, errors.New("503 overloaded_error"), (*provider.SDKStreamError)(nil), &provider.SDKStreamError{Class: "UnknownFutureSDKError", Message: `{"type":"overloaded_error"}`}} {
		if apiFailure(failure) != nil || is529(failure) {
			t.Fatalf("promoted %T", failure)
		}
	}
	if (&CannotRetryError{}).Error() != "undefined" {
		t.Fatal("zero-attempt terminal mismatch")
	}
	if (&CannotRetryError{Original: errors.New("detail")}).Unwrap() == nil {
		t.Fatal("lost original error")
	}
}
