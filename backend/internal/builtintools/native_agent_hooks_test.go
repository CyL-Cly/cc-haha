package builtintools

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// A configured Agent hook requires the same concrete parent graph and original
// runner as the existing native preset path. A callback alone grants no proof.
// The public foreground/background physical Source comparator tests exercise
// the supported, canonical MainPrint route; these finite services must reject.
func TestAgentConfiguredHooksRejectFiniteCallbacksWithoutNativePresetBinding(t *testing.T) {
	for _, forged := range []bool{false, true} {
		for _, background := range []bool{false, true} {
			t.Run(fmt.Sprintf("forged-%t-background-%t", forged, background), func(t *testing.T) {
				service, owner, _, _ := fixture(t)
				calls := 0
				runner := func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error) {
					calls++
					return nil, fmt.Errorf("finite callback acquired native hooks lifecycle")
				}
				service.options.RunAgent = runner
				service.options.RunBackgroundAgent = runner
				service.options.AgentDefinitions = func(*cooperative.Scope) ([]agentcatalog.Definition, error) {
					return []agentcatalog.Definition{{Name: "configured", SystemPrompt: "controlled definition", Fields: input(`{"tools":["Read"],"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"controlled","once":true}]}]}}`)}}, nil
				}
				if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					if forged {
						service.nativeAgentMCP = &nativeAgentMCPPreset{controller: new(sessioncontroller.Controller), foreground: runner, background: runner}
					}
					future, err := service.startAgent(context.Background(), input(fmt.Sprintf(`{"subagent_type":"configured","prompt":"read","run_in_background":%t}`, background)), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote("finite-hook")})
					var capability *CapabilityError
					if future != nil || !errors.As(err, &capability) || capability.Boundary != "Agent configured hooks lifecycle" {
						return fmt.Errorf("unbound or forged hook lifecycle admitted: future=%v err=%v", future, err)
					}
					if calls != 0 {
						return fmt.Errorf("unbound runner was called %d times", calls)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
