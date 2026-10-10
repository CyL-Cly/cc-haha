package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"path/filepath"
	"testing"
)

func TestTypedModelActualSourceSubscriberOneMAccessAndExtraUsageBilling(t *testing.T) {
	for _, tier := range []string{"pro", "max", "team"} {
		for _, extra := range []any{nil, "out_of_credits", "member_level_disabled"} {
			t.Run(tier+"-"+string(raw(extra)), func(t *testing.T) {
				tokens := map[string]any{"accessToken": "owned-fake-claude-token", "refreshToken": "owned-fake-refresh", "expiresAt": 2000000000000, "subscriptionType": tier, "rateLimitTier": "default_claude_max_5x", "scopes": []string{"user:profile", "user:inference"}}
				secure := map[string]any{"claudeAiOauth": tokens, "future": true}
				env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6"}
				global := map[string]any{"cachedExtraUsageDisabledReason": extra, "penguinModeOrgEnabled": true}
				rows := []typedModelSourceRow{}
				for i, arg := range []string{"sonnet[1m]", "opus[1m]", "opus", "sonnet"} {
					rows = append(rows, typedModelSourceRow{Name: arg, Args: arg, Settings: map[string]any{"model": "opus", "fastMode": true, "future": true}, State: map[string]any{"mainLoopModel": "opus", "mainLoopModelForSession": nil}, Env: env, Global: global, InitializeFast: true, ContinueStore: i > 0})
				}
				want := runTypedModelSourceCredentials(t, rows, "", secure)
				o := nativeSessionOptions(t, "http://127.0.0.1:43210")
				o.Interactive = true
				o.Arguments.Model = "opus"
				for _, e := range []map[string]string{o.Environment, o.Native.Environment} {
					delete(e, "CLAUDE_CODE_DISABLE_FAST_MODE")
					for k, v := range env {
						e[k] = v
					}
				}
				secureRaw, _ := json.Marshal(secure)
				o.Native.RunSecurity = func(ctx context.Context, request securestorage.Command) ([]byte, error) {
					if len(request.Args) == 0 || request.Args[0] != "find-generic-password" {
						t.Errorf("unexpected credential side effect %v", request.Args)
					}
					return secureRaw, nil
				}
				config := o.Environment["CLAUDE_CONFIG_DIR"]
				if e := os.MkdirAll(config, 0700); e != nil {
					t.Fatal(e)
				}
				settings, _ := json.Marshal(rows[0].Settings)
				g, _ := json.Marshal(global)
				if e := os.WriteFile(filepath.Join(config, "settings.json"), settings, 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(config, ".claude.json"), g, 0600); e != nil {
					t.Fatal(e)
				}
				s, e := NewSession(context.Background(), o)
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close(context.Background())
				for i, row := range rows {
					var notice string
					if e = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(event tui.Event) error {
						if event.Kind == tui.Notice {
							notice = event.Text
						}
						return nil
					}); e != nil {
						t.Fatal(e)
					}
					if notice != want[i].Notice {
						t.Fatal("Source subscriber notice", i, notice, want[i].Notice)
					}
					actual, e := os.ReadFile(filepath.Join(config, "settings.json"))
					if e != nil {
						t.Fatal(e)
					}
					var saved map[string]any
					if e = json.Unmarshal(actual, &saved); e != nil {
						t.Fatal(e)
					}
					if string(raw(saved)) != string(raw(want[i].Settings)) {
						t.Fatal("Source saved fields", i, saved, want[i].Settings)
					}
				}
			})
		}
	}
}
