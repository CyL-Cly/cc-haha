package sdkhost

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestAgentRestartRejectsUnmintedPromptAndForeignOwner(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	h := &Host{options: Options{Owner: owner, Print: &PrintOptions{AgentID: "actual-agent", AgentRestartPrompt: &NativeAgentRestartPrompt{}}}}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := h.consumeAgentRestartPrompt(scope, []byte(`"not an actual original prompt"`))
		if !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("unminted prepared history admitted", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := h.ExportAgentRestartPrompt(scope)
		if !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign owner exported history", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if h.agentRestartPromptConsumed {
		t.Fatal("rejected proof consumed original slot")
	}
}
