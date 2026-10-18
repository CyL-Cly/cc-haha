package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func TestOpenFailureObserverIsOnceReturnsSameErrorAndPreservesAttemptObserver(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	var observed []error
	deps.ObserveOpenFailure = func(err error) { observed = append(observed, err) }
	_, err := Open(nil, input, deps)
	if err == nil || len(observed) != 1 || observed[0] != err {
		t.Fatal("original admission error changed", err, observed)
	}
	server := startFixtureServer(t, "success", func(int) (int, string) {
		return 400, `{"error":{"type":"invalid_request_error","message":"private failure"}}`
	})
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "0"
	observed = nil
	var attempts []error
	deps.ObserveFailure = func(err error) { attempts = append(attempts, err) }
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		deps.NativeOpenIO = providernative.NewFactory(owner, scope, nil)
		_, err := Open(context.Background(), input, deps)
		return err
	})
	var exhausted *retrypolicy.CannotRetryError
	if !errors.As(err, &exhausted) || len(observed) != 1 || observed[0] != err || len(attempts) != 1 || attempts[0] != exhausted.Original {
		t.Fatal("terminal or perattempt pointer replaced", err, observed, attempts)
	}
}

func TestOpenFailureObserverIsSilentOnRealSuccessfulPhysicalRequest(t *testing.T) {
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	deps.ObserveOpenFailure = func(error) { t.Error("successful Open emitted failure") }
	owner := cooperative.New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		deps.NativeOpenIO = providernative.NewFactory(owner, scope, nil)
		stream, err := Open(context.Background(), input, deps)
		if err != nil {
			return err
		}
		return stream.Close()
	}); err != nil {
		t.Fatal(err)
	}
}
