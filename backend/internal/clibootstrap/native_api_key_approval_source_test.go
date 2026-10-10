package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeInteractiveAPIKeySourceReadsOriginalApprovalAndGlobalFallback(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, key := range []string{"private-short", "private-long-prefix-12345678901234567890", "private-😀😀😀😀😀😀😀😀😀😀X"} {
		units, _ := protocol.DecodeJavaScriptJSONString(quoteMain(key))
		if len(units) > 20 {
			units = units[len(units)-20:]
		}
		normalized := json.RawMessage(protocol.QuoteJavaScriptUTF16(units))
		for _, approved := range []any{nil, []any{}, []any{key}, []any{normalized}, []any{"private-other"}, []any{false, 17, normalized}} {
			for _, primary := range []any{nil, "private-managed"} {
				rows = append(rows, map[string]any{"env": map[string]string{"ANTHROPIC_API_KEY": key}, "global": map[string]any{"customApiKeyResponses": map[string]any{"approved": approved, "rejected": []any{key}, "future": true}, "primaryApiKey": primary}, "preferThirdParty": false, "apiKeySource": true})
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-claude-subscriber.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDE_SUBSCRIBER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual approval Source %v %s", err, stderr.String())
	}
	var original []struct {
		Source string
		Error  any
	}
	if err := json.Unmarshal(data, &original); err != nil || len(original) != len(rows) {
		t.Fatalf("original approval output %v %s", err, data)
	}
	for index, row := range rows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if original[index].Error != nil {
				t.Fatalf("unresolved original approval case %v", original[index].Error)
			}
			m := openAIAuthMainFixture(t)
			var got string
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				globalFields := row["global"].(map[string]any)
				if err := m.Global.Save(func(global *protocol.JSONObject) (*protocol.JSONObject, error) {
					copy := global.Clone()
					for key, value := range globalFields {
						copy.Set(key, rawMain(value))
					}
					return &copy, nil
				}); err != nil {
					return err
				}
				owned, err := m.openAIAuth.owned(context.Background(), scope)
				if err != nil {
					return err
				}
				got, err = m.openAIAuth.apiKeySource(owned, row["env"].(map[string]string), false, false)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if got != original[index].Source {
				t.Fatalf("actual stored approval source %q want%q", got, original[index].Source)
			}
		})
	}
}
