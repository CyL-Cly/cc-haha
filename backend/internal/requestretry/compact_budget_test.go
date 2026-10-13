package requestretry

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"testing"
)

func TestCompactSharedBudgetSpentOnlyByPhysicalAttemptsAndNeverReset(t *testing.T) {
	server := startFixtureServer(t, "budget", func(int) (int, string) { return 503, `{"error":{"type":"api_error","message":"temporary failure"}}` })
	owner := cooperative.New()
	defer owner.Close()
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		proof, e := cooperative.NewNativeCompactModels(scope, owner)
		if e != nil {
			return e
		}
		input, deps := standardInput(), standardDeps()
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
		input.Context.Host.Env["CLAUDE_CODE_MAX_RETRIES"] = "10"
		input.Context.Options.QuerySource = "compact"
		deps.StreamOwner = owner
		deps.StreamScope = scope
		deps.NativeCompactSource = proof
		deps.APIAttemptBudget = NewCompactAttemptBudget(map[string]string{"CLAUDE_CODE_MAX_RETRIES": "0"})
		deps.NativeOpenIO = providernative.NewFactory(owner, scope, nil)
		deps.BodyIOAwait = func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		}
		for i := range 2 {
			stream, e := Open(context.Background(), input, deps)
			if stream != nil {
				stream.Close()
			}
			var stopped *retrypolicy.CannotRetryError
			if !errors.As(e, &stopped) {
				return fmt.Errorf("compact must stop at shared budget: %v", e)
			}
			if i == 0 {
				var original *provider.SDKStreamError
				if !errors.As(stopped, &original) || original.Status == nil || *original.Status != 503 {
					return fmt.Errorf("budget lost last upstream failure: %v", e)
				}
			} else if !errors.Is(e, ErrCompactBudgetExhausted) {
				return fmt.Errorf("fresh exhausted loop must use no-attempt error: %v", e)
			}
		}
		if server.requests.Load() != 1 || deps.APIAttemptBudget.Remaining() != 0 {
			return fmt.Errorf("physical attempt exceeded shared budget: requests%d remaining%f", server.requests.Load(), deps.APIAttemptBudget.Remaining())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestCompactBudgetInvalidValuesUseSourceDefault(t *testing.T) {
	for _, value := range []string{"NaN", "-1", "Infinity"} {
		budget := NewCompactAttemptBudget(map[string]string{"CLAUDE_CODE_MAX_RETRIES": value})
		if budget.Remaining() != 11 {
			t.Fatal(value, budget.Remaining())
		}
	}
}
