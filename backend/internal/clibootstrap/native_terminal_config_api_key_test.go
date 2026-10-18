package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestTerminalCustomAPIKeyPreferencePhysicalUnknownFieldsAndActualAuthSource(t *testing.T) {
	for _, key := range []string{"private-short", "private-long-prefix-12345678901234567890", "private-😀😀😀😀😀😀😀😀😀😀X"} {
		t.Run(key, func(t *testing.T) {
			m := openAIAuthMainFixtureWithEnv(t, map[string]string{"ANTHROPIC_API_KEY": key, "NODE_ENV": "production"})
			if err := m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					next.Set("futureGlobal", rawMain(map[string]any{"keep": true}))
					next.Set("primaryApiKey", quoteMain("private-fallback"))
					next.Set("customApiKeyResponses", rawMain(map[string]any{"approved": []any{"other-approved"}, "rejected": []any{"other-rejected"}, "future": 17}))
					return &next, nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := m.TerminalConfigState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			source := func() string {
				t.Helper()
				var value string
				if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					owned, err := m.openAIAuth.owned(context.Background(), scope)
					if err != nil {
						return err
					}
					value, err = m.openAIAuth.apiKeySource(owned, m.nativeOpenAIEnvironment(), false, false)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return value
			}
			if source() != "/login managed key" {
				t.Fatal("initial actual auth fallback", source())
			}
			if err = m.SetTerminalConfigSetting(context.Background(), "apiKey", []byte("true")); err != nil {
				t.Fatal(err)
			}
			if source() != "ANTHROPIC_API_KEY" {
				t.Fatal("next actual auth source did not consume approved custom key", source())
			}
			after, err := m.TerminalConfigState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			global := mainObj(after.Get("global"))
			responses := mainObj(global.Get("customApiKeyResponses"))
			if global.Get("futureGlobal") == nil || string(responses.Get("future")) != "17" {
				t.Fatal(string(rawMain(global)))
			}
			var approved []json.RawMessage
			_ = json.Unmarshal(responses.Get("approved"), &approved)
			if len(approved) != 2 {
				t.Fatal(string(rawMain(global)))
			}
			normalized, _ := protocol.DecodeJavaScriptJSONString(terminalNormalizedAPIKey(key))
			last, _ := protocol.DecodeJavaScriptJSONString(approved[1])
			if len(normalized) != len(last) {
				t.Fatal("UTF16 approval was replaced")
			}
			if err = m.SetTerminalConfigSetting(context.Background(), "apiKey", []byte("false")); err != nil {
				t.Fatal(err)
			}
			if source() != "/login managed key" {
				t.Fatal("rejected custom key kept actual auth priority", source())
			}
			if err = m.RestoreTerminalConfig(context.Background(), snapshot, []string{"apiKey"}); err != nil {
				t.Fatal(err)
			}
			restored, err := m.TerminalConfigState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if string(mainObj(restored.Get("global")).Get("customApiKeyResponses")) != string(mainObj(snapshot.Get("global")).Get("customApiKeyResponses")) {
				t.Fatal("Escape did not restore exact original approval arrays")
			}
		})
	}
}
