package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestCommandAuthLogoutActualFilesAndIndependentSlots(t *testing.T) {
	for _, fileBacked := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared", true: "selected-file"}[fileBacked], func(t *testing.T) {
			m := authCommandFixture(t, map[string]any{"env": map[string]string{}, "global": map[string]any{"oauthAccount": map[string]any{"emailAddress": "private@example.invalid"}, "primaryApiKey": "fake-global", "hasCompletedOnboarding": true, "futureRoot": "preserve"}})
			config := m.Native.Environment["CLAUDE_CONFIG_DIR"]
			credential := filepath.Join(config, ".credentials.json")
			before := []byte(`{"claudeAiOauth":{"accessToken":"fake-claude"},"openaiCodexOauth":{"accessToken":"fake-openai","refreshToken":"fake-refresh","expiresAt":17},"grokOauth":{"future":"preserve"},"futureSecure":true}`)
			m.Native.RunSecurity = nil // Store retains its already-bound unavailable primitive.
			agentsWrite(t, credential, string(before))
			selected := filepath.Join(config, "selected-openai.json")
			if fileBacked {
				m.Native.Environment["OPENAI_CODEX_OAUTH_FILE"] = selected
				agentsWrite(t, selected, `{"accessToken":"fake-openai","refreshToken":"fake-refresh","expiresAt":17}`)
			}
			var result cliauth.Result
			err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
				var err error
				result, err = m.AuthLogout(context.Background(), scope, true)
				return err
			})
			if err != nil || result.ExitCode != 0 {
				t.Fatal(result, err)
			}
			actual, err := os.ReadFile(credential)
			if err != nil {
				t.Fatal(err)
			}
			var value protocol.JSONObject
			if err = value.UnmarshalJSON(actual); err != nil {
				t.Fatal(err)
			}
			if value.Get("claudeAiOauth") == nil || value.Get("grokOauth") == nil || string(value.Get("futureSecure")) != "true" {
				t.Fatal("OpenAI logout removed another slot", string(actual))
			}
			if fileBacked {
				if string(actual) != string(before) {
					t.Fatal("selected-file logout touched shared store")
				}
				if _, err = os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("selected token file remained", err)
				}
				if data, err := os.ReadFile(filepath.Join(config, "cc-haha", "openai-oauth-file-backed")); err != nil || string(data) != "1\n" {
					t.Fatal("file-backed marker missing", err)
				}
			} else if value.Get("openaiCodexOauth") != nil {
				t.Fatal("OpenAI slot remained", string(actual))
			}
			global, err := m.Global.Get()
			if err != nil || global.Get("oauthAccount") == nil || global.Get("primaryApiKey") == nil {
				t.Fatal("OpenAI logout changed Anthropic global state", err)
			}
			remote := filepath.Join(config, "remote-settings.json")
			agentsWrite(t, remote, `{"futurePolicy":true}`)
			err = m.Run(context.Background(), func(scope *cooperative.Scope) error {
				var err error
				result, err = m.AuthLogout(context.Background(), scope, false)
				return err
			})
			if err != nil || result.ExitCode != 0 {
				t.Fatal(result, err)
			}
			if _, err = os.Stat(credential); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("general logout retained secure record", err)
			}
			if _, err = os.Stat(remote); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("remote managed cache retained", err)
			}
			global, err = m.Global.Get()
			if err != nil {
				t.Fatal(err)
			}
			if global.Get("oauthAccount") != nil || global.Get("primaryApiKey") != nil || string(global.Get("hasCompletedOnboarding")) != "true" || string(global.Get("futureRoot")) != `"preserve"` {
				raw, _ := json.Marshal(global)
				t.Fatal("global logout did not preserve original unrelated state", string(raw))
			}
			m.Close()
			if err = m.Run(context.Background(), func(scope *cooperative.Scope) error {
				_, e := m.AuthStatus(context.Background(), scope, true, false)
				return e
			}); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("closed command accepted auth read", err)
			}
		})
	}
}

func TestCommandAuthStatusRejectsForeignOwnerAndCancelledScopeBeforeFiles(t *testing.T) {
	m := authCommandFixture(t, map[string]any{"env": map[string]string{}})
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := m.AuthStatus(context.Background(), scope, false, false)
		return err
	}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign owner accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Run(ctx, func(scope *cooperative.Scope) error { _, err := m.AuthStatus(ctx, scope, false, false); return err }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled command read", err)
	}
}
