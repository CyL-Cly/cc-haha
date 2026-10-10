package sdkhost

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestSDKRequestDependenciesHaveFreshSeparateNativeBodyScope(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	host := &Host{options: Options{Owner: owner}}
	var old requestretry.Dependencies
	worked := 0
	work := func(context.Context) ([]byte, error) { worked++; return []byte("original physical bytes"), nil }
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		old = host.requestDependencies(scope)
		if old.NativeOpenIO == nil || old.BodyIOAwait == nil || old.OpenIOAwait != nil {
			return errors.New("SDK still selected legacy header/body driver")
		}
		if err := old.NativeOpenIO.CheckOwner(); err != nil {
			return err
		}
		if _, err := old.BodyIOAwait(provider.OwnerNetworkOpen, work); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("body seam performed header Open")
		}
		raw, err := old.BodyIOAwait(provider.OwnerBodyRead, work)
		if err != nil || string(raw) != "original physical bytes" {
			return errors.New("current ASK body wait did not use existing owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := old.NativeOpenIO.CheckOwner(); !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("previous ASK native factory reused")
		}
		if _, err := old.BodyIOAwait(provider.OwnerBodyRead, work); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("previous ASK body scope reused")
		}
		fresh := host.requestDependencies(scope)
		if err := fresh.NativeOpenIO.CheckOwner(); err != nil {
			return err
		}
		raw, err := fresh.BodyIOAwait(provider.OwnerErrorBody, work)
		if err != nil || string(raw) != "original physical bytes" {
			return errors.New("fresh ASK error body wait was not admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if worked != 2 {
		t.Fatal("unadmitted worker/body duplicated", worked)
	}
}
