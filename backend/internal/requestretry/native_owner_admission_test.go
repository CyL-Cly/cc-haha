package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func TestNativeFactoryRejectsStaleAndForeignScopeBeforePreparation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	var stale *providernative.Factory
	// Return assertion failures through the owner receipt. t.Fatal here would
	// Goexit the owner goroutine before Run can join, hiding this witness in a timeout.
	assertRejected := func(factory NativeOpenIOFactory) error {
		input, deps := standardInput(), standardDeps()
		allocations, preparations, clients, keys, requests := 0, 0, 0, 0, 0
		deps.NativeOpenIO = factory
		deps.UUID = func() string { allocations++; return "unexpected" }
		deps.OnPreparation = func([]protocol.JSONObject) { preparations++ }
		deps.OnClient = func() { clients++ }
		deps.OnKeyLookup = func() { keys++ }
		deps.ObserveRequest = func(string, http.Header, json.RawMessage) { requests++ }
		_, err := Open(context.Background(), input, deps)
		if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("administrative admission became SDK timeout or other error: %T %v", err, err)
		}
		if allocations+preparations+clients+keys+requests != 0 {
			return fmt.Errorf("unadmitted effects: %d %d %d %d %d", allocations, preparations, clients, keys, requests)
		}
		return nil
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = providernative.NewFactory(owner, scope, nil)
		return assertRejected(providernative.NewFactory(foreign, scope, nil))
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return assertRejected(stale) }); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFactoryMutualExclusionPrecedesScopeAndPreparation(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	deps.NativeOpenIO = providernative.NewFactory(nil, nil, nil)
	deps.OpenIOAwait = func(provider.OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		t.Fatal("ambiguous await ran")
		return nil, nil
	}
	deps.OnPreparation = func([]protocol.JSONObject) { t.Fatal("ambiguous Prepare ran") }
	_, err := Open(context.Background(), input, deps)
	var cap *CapabilityError
	if !errors.As(err, &cap) || cap.Boundary != "ambiguous original native/legacy owner bridge" {
		t.Fatal(err)
	}
}

func TestNativeFactoryCurrentScopeBuildsClientOnlyAfterOnePreparation(t *testing.T) {
	server := startFixtureServer(t, "success", func(int) (int, string) { return 200, responseWire })
	owner := cooperative.New()
	defer owner.Close()
	for round := 0; round < 2; round++ {
		var phases []string
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			deps.NativeOpenIO = providernative.NewFactory(owner, scope, func(event provider.OwnerNativeEvent) {
				if event.Kind == "timer-arm" {
					phases = append(phases, "arm")
				}
			})
			deps.OnPreparation = func([]protocol.JSONObject) { phases = append(phases, "Prepare") }
			deps.OnKeyLookup = func() { phases = append(phases, "key") }
			deps.OnClient = func() { phases = append(phases, "client") }
			stream, err := Open(context.Background(), input, deps)
			if err != nil {
				return err
			}
			return stream.Close()
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(phases, []string{"Prepare", "key", "client", "arm"}) {
			t.Fatal(phases)
		}
	}
	if server.requests.Load() != 2 {
		t.Fatal("different ASK scope reused or duplicated request", server.requests.Load())
	}
}

// This bad host implementation still validates an actual current scope; it then
// returns an absent/expired real client. These are explicit administration
// failures, never default identity await or fake success callbacks.
type badClientFactory struct {
	admission *providernative.Factory
	retained  *provider.OwnerNativeIO
	fail      bool
}

func (factory *badClientFactory) CheckOwner() error { return factory.admission.CheckOwner() }
func (factory *badClientFactory) NewNativeOpenIO() (*provider.OwnerNativeIO, error) {
	if factory.fail {
		return providernative.NewFactory(nil, nil, nil).NewNativeOpenIO()
	}
	return factory.retained, nil
}
func TestNativeFactoryFailedClientCreationCannotBecomeSDKTimeoutOrRetry(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var oldClient *provider.OwnerNativeIO
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		oldClient, err = providernative.New(owner, scope, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil client", "factory administration failure", "expired client"} {
		t.Run(mode, func(t *testing.T) {
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				input, deps := standardInput(), standardDeps()
				factory := &badClientFactory{admission: providernative.NewFactory(owner, scope, nil), fail: mode == "factory administration failure"}
				if mode == "expired client" {
					factory.retained = oldClient
				}
				deps.NativeOpenIO = factory
				preparations, clients, keys, notices, failures := 0, 0, 0, 0, 0
				deps.OnPreparation = func([]protocol.JSONObject) { preparations++ }
				deps.OnClient = func() { clients++ }
				deps.OnKeyLookup = func() { keys++ }
				deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) { notices++; return Resume, nil }
				deps.ObserveFailure = func(error) { failures++ }
				_, err := Open(context.Background(), input, deps)
				if mode == "nil client" {
					var cap *CapabilityError
					if !errors.As(err, &cap) || cap.Boundary != "missing original native owner bridge" {
						t.Fatal(err)
					}
				} else if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrRestricted) {
					t.Fatal("client admin fault became SDK timeout", err)
				}
				if preparations != 1 || clients+keys+notices+failures != 0 {
					t.Fatal("client creation failure changed Source prepare/order or tried request", preparations, clients, keys, notices, failures)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
