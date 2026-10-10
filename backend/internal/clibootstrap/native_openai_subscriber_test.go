package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeModelActiveMatchesActualSourceAuthAndPhysicalSharedCredentials(t *testing.T) {
	t.Parallel()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, env := range []map[string]string{{}, {"CLAUDE_CODE_SIMPLE": "true"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CLAUDE_CODE_OAUTH_TOKEN": "fake-claude-env"}, {"ANTHROPIC_API_KEY": "fake-direct"}, {"CLAUDE_CODE_USE_BEDROCK": "true"}, {"CLAUDE_CODE_USE_VERTEX": "1"}} {
		for _, claude := range []any{nil, map[string]any{"accessToken": "fake-claude", "scopes": []string{"user:profile"}}, map[string]any{"accessToken": "fake-claude", "scopes": []string{"user:inference"}}} {
			for _, primary := range []any{nil, "fake-managed", 17} {
				rows = append(rows, map[string]any{"env": env, "secure": map[string]any{"openaiCodexOauth": map[string]any{"accessToken": "fake-openai", "refreshToken": "fake-refresh", "expiresAt": 1770003600000}, "claudeAiOauth": claude}, "global": map[string]any{"primaryApiKey": primary}, "preferThirdParty": true, "modelActive": true})
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-claude-subscriber.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDE_SUBSCRIBER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source model active: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			initial := map[string]string{}
			if simple := row["env"].(map[string]string)["CLAUDE_CODE_SIMPLE"]; simple != "" {
				initial["CLAUDE_CODE_SIMPLE"] = simple
			}
			m := openAIAuthMainFixtureWithEnv(t, initial)
			binding, err := m.NewNativeOpenAIAuth(nil)
			if err != nil {
				t.Fatal(err)
			}
			var active bool
			if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if err := m.environment.SetProperty(quoteMain("ANTHROPIC_API_KEY"), nil); err != nil {
					return err
				}
				for key, value := range row["env"].(map[string]string) {
					if key != "CLAUDE_CODE_SIMPLE" {
						if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
							return err
						}
					}
				}
				if !m.secure.UpdateSourceJSON(context.Background(), rawMain(row["secure"])).Success {
					return errors.New("isolated credentials write failed")
				}
				if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					copy := current.Clone()
					for key, value := range row["global"].(map[string]any) {
						copy.Set(key, rawMain(value))
					}
					return &copy, nil
				}); err != nil {
					return err
				}
				var e error
				active, e = binding.ModelActive(context.Background(), scope, true)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			var want struct{ Active bool }
			if err = json.Unmarshal(expected[i], &want); err != nil {
				t.Fatal(err)
			}
			if active != want.Active {
				t.Errorf("native active%v Source%s", active, expected[i])
			}
		})
	}
}

func TestNativeClaudeSubscriberReadsActualSharedStateRetainsMemoAndRejectsForeignOwner(t *testing.T) {
	m := openAIAuthMainFixture(t)
	binding, err := m.NewNativeOpenAIAuth(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	observed := []bool{}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if e := m.environment.SetProperty(quoteMain("ANTHROPIC_API_KEY"), nil); e != nil {
			return e
		}
		for i, scopes := range []string{`["user:inference"]`, `["user:profile"]`} {
			raw := []byte(`{"claudeAiOauth":{"accessToken":"fake-only","scopes":` + scopes + `}}`)
			if !m.secure.UpdateSourceJSON(ctx, raw).Success {
				return errors.New("isolated credentials update failed")
			}
			value, e := binding.IsClaudeSubscriber(ctx, scope, true)
			if e != nil {
				return e
			}
			observed = append(observed, value)
			if i == 1 {
				if e = binding.ClearClaudeTokenCache(scope); e != nil {
					return e
				}
				value, e = binding.IsClaudeSubscriber(ctx, scope, true)
				if e != nil {
					return e
				}
				observed = append(observed, value)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed, []bool{true, true, false}) {
		t.Fatal("shared credential/memo cache behavior", observed)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, e := binding.ModelActive(ctx, scope, true)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign accepted: %v", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
