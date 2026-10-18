package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestCommandLegacyAPIKeyMemoCachesActualPrimitiveAndConfigFallbackUntilRemove(t *testing.T) {
	for _, initial := range []string{"", "private-command-legacy"} {
		t.Run(fmt.Sprint(initial != ""), func(t *testing.T) {
			row := map[string]any{"env": map[string]string{}, "legacy": initial}
			m := authCommandFixture(t, row)
			defer m.Close()
			err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
				first, present, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
				if err != nil || first != initial || present != (initial != "") {
					return fmt.Errorf("first original legacy getter key%q present%v: %w", first, present, err)
				}
				row["legacy"] = "private-later-key"
				if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					next.Set("primaryApiKey", quoteMain("private-later-fallback"))
					return &next, nil
				}); err != nil {
					return err
				}
				second, secondPresent, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
				if err != nil || second != first || secondPresent != present {
					return fmt.Errorf("memo resampled native/store fallback key%q present%v: %w", second, secondPresent, err)
				}
				result, err := m.AuthLogout(context.Background(), scope, false)
				if err != nil || result.ExitCode != 0 || m.commandLegacyAPIKey != nil {
					return fmt.Errorf("actual removeApiKey did not invalidate command memo: %+v %w", result, err)
				}
				third, _, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
				if err != nil || third != "private-later-key" {
					return fmt.Errorf("post-remove original primitive not re-read: %q %w", third, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommandLegacyAPIKeyMemoRejectsForeignExpiredCanceledAndSubstitutedStore(t *testing.T) {
	m := authCommandFixture(t, map[string]any{"env": map[string]string{}, "legacy": "private-original"})
	defer m.Close()
	var retained *cooperative.Scope
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := m.nativeCommandLegacyAPIKey(ctx, scope); !errors.Is(err, context.Canceled) || m.commandLegacyAPIKey != nil {
			return fmt.Errorf("cancellation became a cached absence: %v", err)
		}
		_, _, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.nativeCommandLegacyAPIKey(context.Background(), retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained scope unlocked legacy memo", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign owner unlocked legacy memo: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store := m.Secure
	defer func() { m.Secure = store }()
	m.Secure = nil
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, err := m.nativeCommandLegacyAPIKey(context.Background(), scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("substituted storage unlocked legacy memo: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
