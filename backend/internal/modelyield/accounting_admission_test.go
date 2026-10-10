package modelyield

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestAccountingOriginalPricingAdmissionBeforeConsumerEffects(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	session, _ := modelaccounting.NewOwnedSession(owner, nil)
	main := mustMainAccountingContext(owner)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, original := range []string{"known-model", "missing-auth", "active-fast"} {
			input := queuedInput("http://127.0.0.1:1")
			switch original {
			case "known-model":
				input.Context.Options.Model = "claude-sonnet-4"
			case "missing-auth":
				delete(input.Context.Host.Env, "ANTHROPIC_API_KEY")
			case "active-fast":
				active := true
				input.ResolvedFastMode = &active
			}
			starts, allocations := 0, 0
			deps := QueuedDependencies{Session: session, AgentContext: main, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { allocations++; return "must-not-allocate" }}, Start: func(next *cooperative.Scope, it *QueryIterator, control *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
				starts++
				return nil, errors.New("consumer must not run before original accounting admission")
			}}
			_, e := RunQueued(context.Background(), input, deps)
			var cap *modelaccounting.CapabilityError
			if !errors.As(e, &cap) || starts != 0 || allocations != 0 {
				return errors.New("original pricing/context admission allowed consumer or Prepare effects")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
