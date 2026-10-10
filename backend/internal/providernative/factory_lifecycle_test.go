package providernative

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestTypedNativeFactoryAndClientRejectExpiredCapability(t *testing.T) {
	var absent *Factory
	if err := absent.CheckOwner(); !errors.Is(err, provider.ErrHTTPExchangeProfile) {
		t.Fatal("nil factory admitted", err)
	}
	if driver, err := absent.NewNativeOpenIO(); driver != nil || !errors.Is(err, provider.ErrHTTPExchangeProfile) {
		t.Fatal("nil factory constructed client", driver, err)
	}
	var missing *provider.OwnerNativeIO
	if err := missing.CheckOwner(); !errors.Is(err, provider.ErrHTTPExchangeProfile) {
		t.Fatal("nil retained client admitted", err)
	}
	owner := cooperative.New()
	defer owner.Close()
	var driver *provider.OwnerNativeIO
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		factory := NewFactory(owner, scope, nil)
		if err := factory.CheckOwner(); err != nil {
			return err
		}
		var err error
		driver, err = factory.NewNativeOpenIO()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error {
		err := driver.CheckOwner()
		if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("expired client became SDK timeout or fresh bridge", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
