package clibootstrap

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestFreshActivationCacheConsumerClearsPriorNativeReadValues(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, nil)
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.nativeFileCache.Set(scope, "/unrelated-session", builtinwrite.FileState{Content: quoteMain("prior"), Timestamp: math.NaN()}); err != nil {
			return err
		}
		if err := m.restoreNativeFileCache(scope); err != nil {
			return err
		}
		rows, err := m.nativeFileCache.Export(scope)
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return fmt.Errorf("fresh Controller producer retained old native cache: %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
