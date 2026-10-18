package clibootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestAgentAuthenticationCannotBeBoundByRootForeignOrUnconstructedChild(t *testing.T) {
	x := newTerminalMCPAuthFixture(t, false)
	ctx := context.Background()
	var retained *cooperative.Scope
	if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		binding, err := x.m.Controller.BorrowNativeCopyBinding(x.m.Owner)
		if err != nil {
			return err
		}
		if err := x.m.mcpTools.BindNativeAgentAuthentication(scope, binding, func() bool { return true }); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("root catalogue minted Agent write target", err)
		}
		fake := &MainPrint{Owner: x.m.Owner, Controller: x.m.Controller, mcpTools: x.m.mcpTools, options: MainPrintOptions{DetachedAgent: &NativeDetachedAgentOptions{authParent: x.m}}}
		if err := fake.bindNativeAgentAuthentication(scope, binding); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("unconstructed child supplied parent AppState target", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.m.mcpTools.BindNativeAgentAuthentication(retained, nil, func() bool { return true }); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained scope admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := x.m.mcpTools.BindNativeAgentAuthentication(scope, nil, func() bool { return true }); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign scope admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if x.tokens.Load() != 0 || x.connects.Load() != 0 || x.models.Load() != 0 {
		t.Fatal("binding negatives performed auth/model work")
	}
}
