package sessioncontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestRemoteCLIStartupProfileRequiresConsumerAndKeepsAsyncActivation(t *testing.T) {
	c, _, owner := boundController(t)
	c.options.ResumeOptions.Environment["CLAUDE_CODE_SIMPLE"] = "0"
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := c.BeginActivateFreshOwned(scope, FreshRemoteCLIProfile); err == nil {
			return fmt.Errorf("ordinary session accepted remote startup profile")
		}
		c.options.NativeRemoteCLI = true
		future, err := c.BeginActivateFreshOwned(scope, FreshRemoteCLIProfile)
		if err != nil {
			return err
		}
		if c.active {
			return fmt.Errorf("remote startup skipped owner async loadInitialMessages")
		}
		if _, err := future.Await(scope); err != nil {
			return err
		}
		if !c.active || !c.freshOwned || len(c.History()) != 0 {
			return fmt.Errorf("remote staging activated reducer/resume messages")
		}
		if _, err := c.BeginActivateFreshOwned(scope, FreshRemoteCLIProfile); err == nil {
			return fmt.Errorf("remote startup repeated activation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
