package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeClaudeCheckPreservesProducerFailureAndCurrentScope(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	calls := 0
	failure := errors.New("actual private credential stat failure")
	var saved *NativeClaudeAuth
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		subscriber := func(context.Context) (bool, error) { return false, nil }
		token := func(context.Context) (*string, error) { return nil, nil }
		if _, err := NewNativeClaudeAuth(owner, scope, subscriber, token, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("nil selected stat producer accepted")
		}
		var err error
		saved, err = NewNativeClaudeAuth(owner, scope, subscriber, token, func(context.Context) error { calls++; return failure })
		if err != nil {
			return err
		}
		if err := saved.Check(context.Background()); err != failure || calls != 1 {
			return errors.New("stat failure was replaced, swallowed, or repeated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := saved.Check(context.Background()); !errors.Is(err, cooperative.ErrRestricted) || calls != 1 {
		t.Fatal("closed scope invoked credential producer", err, calls)
	}
}
