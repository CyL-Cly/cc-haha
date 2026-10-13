package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkbetas"
	"reflect"
	"testing"
)

func TestCLIAllowedBetasUseActualPrivateAuthIdentityAndWarnSubscribers(t *testing.T) {
	for _, subscriber := range []bool{false, true} {
		m := openAIAuthMainFixture(t)
		auth, err := m.NewNativeOpenAIAuth(nil)
		if err != nil {
			t.Fatal(err)
		}
		m.openAIAuth = auth
		var output bytes.Buffer
		m.options.Error = &output
		m.options.Arguments.Betas = []string{"not-allowed", sdkbetas.Context1M, sdkbetas.Context1M}
		if subscriber {
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if err := m.environment.SetProperty(quoteMain("ANTHROPIC_API_KEY"), nil); err != nil {
					return err
				}
				if !m.secure.UpdateSourceJSON(context.Background(), rawMain(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-only-cli-beta", "scopes": []string{"user:inference"}}})).Success {
					return errors.New("private credential setup failed")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err = m.initializeNativeCLIBetas(); err != nil {
			t.Fatal(err)
		}
		values, warnings := sdkbetas.Filter(m.options.Arguments.Betas, subscriber)
		if !reflect.DeepEqual(values, m.nativeSDKBetas) {
			t.Fatal("startup beta filter used a guessed subscriber identity", subscriber, m.nativeSDKBetas)
		}
		if output.String() != warnings[0]+"\n" {
			t.Fatal("original subscriber/allowlist warning missing", subscriber, output.String())
		}
	}
}
