package providernative

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func originalMalformedCAResolver(t *testing.T) func() (*tls.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "original-ca.pem")
	if err := os.WriteFile(path, []byte("private-invalid-pem-input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := tlsconfig.NewState(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	certificates, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": path})
	if err != nil {
		t.Fatal(err)
	}
	return func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certificates) }
}

// Original env/file recipes produce the same 145 pinned roots plus the malformed
// file. No Source-rendered pool/failure, timer schedule or expected input enters
// the Provider. The actual query/retry oracle separately proves all 17 profiles.
func TestOriginalMalformedCAExpiryResumesOwnerBeforeSDKFinally(t *testing.T) {
	for _, delay := range []time.Duration{time.Millisecond, time.Hour} {
		t.Run(delay.String(), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			resolver := originalMalformedCAResolver(t)
			var phases []string
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) { phases = append(phases, event.Kind) })
				if err != nil {
					return err
				}
				config := nativeConfig("https://127.0.0.1:1")
				config.Timeout = delay
				config.OwnerNativeIO = bridge
				config.HTTPExchange = &provider.RawHTTP1Exchange{ResolveVerifiedTLS: resolver}
				_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
				var failure *provider.SDKStreamError
				if !errors.As(err, &failure) {
					return fmt.Errorf("native creation fault left original SDK error domain: %v", err)
				}
				if delay == time.Millisecond {
					if failure.Class != "APIConnectionTimeoutError" || failure.Cause != nil {
						return fmt.Errorf("due malformed CA became connection failure: %s %v", failure.Class, failure.Cause)
					}
				} else if failure.Class != "APIConnectionError" || failure.Cause == nil {
					return fmt.Errorf("early malformed CA lost its original cause: %s %v", failure.Class, failure.Cause)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"timer-arm", "fetch-rejected", "timer-clear"}
			if delay == time.Millisecond {
				want = []string{"timer-arm", "timer-fire", "fetch-rejected", "timer-clear"}
			}
			if !reflect.DeepEqual(phases, want) {
				t.Fatal("original malformed CA rejection policy order", phases)
			}
		})
	}
}

func TestOriginalCallerAbortDuringCAParseCannotBecomeTimeout(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	resolver := originalMalformedCAResolver(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	var phases []string
	err := owner.Run(caller, func(scope *cooperative.Scope) error {
		bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) { phases = append(phases, event.Kind) })
		if err != nil {
			return err
		}
		config := nativeConfig("https://127.0.0.1:1")
		config.Timeout = time.Millisecond
		config.OwnerNativeIO = bridge
		config.HTTPExchange = &provider.RawHTTP1Exchange{ResolveVerifiedTLS: func() (*tls.Config, error) { value, err := resolver(); cancel(); return value, err }}
		_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(caller, originalPrepared())
		var failure *provider.SDKStreamError
		if !errors.As(err, &failure) || failure.Class != "APIUserAbortError" {
			return fmt.Errorf("original caller abort became timer/native rejection: %v", err)
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(phases, []string{"timer-arm", "fetch-rejected", "timer-clear"}) {
		t.Fatalf("caller abort phases=%v err=%v", phases, err)
	}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal("request cancellation consumed owner lifetime", err)
	}
}

func TestOriginalNestedCARejectionIsAdministrativeNotSDKTimeout(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	resolver := originalMalformedCAResolver(t)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		config := nativeConfig("https://127.0.0.1:1")
		config.Timeout = time.Millisecond
		config.OwnerNativeIO = bridge
		config.HTTPExchange = &provider.RawHTTP1Exchange{ResolveVerifiedTLS: resolver}
		return scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
			_, _, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
			if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrRestricted) {
				return fmt.Errorf("nested host administration became SDK timeout: %v", err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOriginalRejectionBridgeRejectsForeignAndStaleScopes(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	failure := errors.New("original native creation failure")
	var absent, stale *Boundary
	if !errors.Is(absent.RejectFetch(failure), cooperative.ErrRestricted) {
		t.Fatal("absent original rejection bridge admitted")
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = &Boundary{owner, scope}
		if !errors.Is((&Boundary{foreign, scope}).RejectFetch(failure), cooperative.ErrRestricted) {
			return errors.New("foreign owner rejection continuation admitted")
		}
		return nil
	})
	if err != nil || !errors.Is(stale.RejectFetch(failure), cooperative.ErrRestricted) {
		t.Fatalf("rejection scope lifetime err=%v", err)
	}
}
