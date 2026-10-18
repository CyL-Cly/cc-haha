//go:build darwin

package clibootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeInspectionReadSharesMemoWithoutPromptContinuationAuthority(t *testing.T) {
	m, _ := promptPendingMain(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var first, repeated *ownerasync.OwnerFuture[protocol.JSONObject]
	read, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		first, err = m.beginNativeInspectionSystemContext(scope)
		if err != nil {
			return err
		}
		repeated, err = m.beginNativeInspectionSystemContext(scope)
		if err != nil {
			return err
		}
		if first != repeated || first != m.systemContext {
			t.Error("inspection replaced the original system-context promise memo")
		}
		if _, err := m.beginNativeUserContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("inspection acquired User producer authority: %v", err)
		}
		if _, err := m.beginNativeDefaultPrompt(scope, nil, "fixture"); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("inspection acquired default producer authority: %v", err)
		}
		_, err = first.Await(scope)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Wait(); err != nil {
		t.Fatal("actual MainPrint inspection callback", err)
	}
}

func TestNativeInspectionMemoRejectsForeignUnboundRetainedAndClosedFactory(t *testing.T) {
	m, _ := promptPendingMain(t)
	other, _ := promptPendingMain(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var retained *cooperative.Scope
	read, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		retained = scope
		original, err := m.beginNativeInspectionSystemContext(scope)
		if err != nil {
			return err
		}
		if _, err := original.Await(scope); err != nil {
			return err
		}
		settings := m.settings
		m.settings = other.settings
		if _, err := m.beginNativeInspectionSystemContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("warm memo accepted a foreign Settings service: %v", err)
		}
		m.settings = settings
		startup := m.Startup
		m.Startup = other.Startup
		if _, err := m.beginNativeInspectionSystemContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("warm memo accepted a foreign Startup service: %v", err)
		}
		m.Startup = startup
		m.nativePromptLife.mu.Lock()
		lanes := m.nativePromptLife.lanes
		m.nativePromptLife.lanes = nil
		m.nativePromptLife.mu.Unlock()
		if _, err := m.beginNativeInspectionSystemContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("unbound Read minted inspection authority: %v", err)
		}
		m.nativePromptLife.mu.Lock()
		m.nativePromptLife.lanes = lanes
		m.nativePromptLife.mu.Unlock()
		if m.systemContext != original {
			t.Error("failed inspection changed the system memo")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.beginNativeInspectionSystemContext(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Errorf("retained scope accepted: %v", err)
	}
	if err := other.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.beginNativeInspectionSystemContext(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Errorf("foreign owner accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.BeginNativePromptClose()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.beginNativeInspectionSystemContext(scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Errorf("closed factory accepted warm memo: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
