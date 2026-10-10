package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func runPlatformStageSource(t *testing.T, fixture, gate string, input any, home string) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Source Bun required for platform stage comparison")
	}
	fixturePath, _ := filepath.Abs("../../testdata/" + fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), gate + "=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source: %v %s", err, stderr.Bytes())
	}
	if !json.Valid(out) {
		t.Fatalf("actual Source JSON: %s", out)
	}
	return out
}

func TestActualSourceWechatWecomQQCredentialAuthorityRawTypesAndDefaults(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	profiles := []map[string]any{}
	fields := map[string][]string{"wechat": {"accountId", "botToken", "baseUrl", "userId"}, "wecom": {"botId", "secret"}, "qq": {"appId", "appSecret"}}
	values := []json.RawMessage{raw("file"), raw(""), raw(nil), raw(false), raw(true), raw(0), json.RawMessage(`-0`), json.RawMessage(`0.0`), json.RawMessage(`1e400`), json.RawMessage(`-1e400`), raw(7), raw([]any{}), raw([]any{1, "x"}), raw(map[string]string{"k": "v"}), json.RawMessage(`"raw-\ud800-\udfff"`)}
	for _, value := range values {
		file := map[string]map[string]json.RawMessage{}
		for platform, names := range fields {
			file[platform] = map[string]json.RawMessage{}
			for _, name := range names {
				file[platform][name] = value
			}
		}
		profiles = append(profiles, map[string]any{"file": string(raw(file)), "env": map[string]string{}})
	}
	profiles = append(profiles, map[string]any{"file": "{}", "env": map[string]string{}})
	for _, value := range []string{"", "env", "  ", "false", "0"} {
		env := map[string]string{}
		for _, key := range []string{"WECHAT_ACCOUNT_ID", "WECHAT_BOT_TOKEN", "WECHAT_BASE_URL", "WECHAT_USER_ID", "WECOM_BOT_ID", "WECOM_BOT_SECRET", "QQ_APP_ID", "QQ_APP_SECRET"} {
			env[key] = value
		}
		profiles = append(profiles, map[string]any{"file": `{"wechat":{"accountId":"file","botToken":"file","baseUrl":"file","userId":"file"},"wecom":{"botId":"file","secret":"file"},"qq":{"appId":"file","appSecret":"file"}}`, "env": env})
	}
	want := runPlatformStageSource(t, "native-im-platform-credentials.fixture.ts", "CC_HAHA_IM_PLATFORM_CREDENTIAL_FIXTURE", map[string]any{"profiles": profiles}, home)
	rows := []any{}
	for _, profile := range profiles {
		if err := os.WriteFile(filepath.Join(configDir, "adapters.json"), []byte(profile["file"].(string)), 0600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"CLAUDE_CONFIG_DIR": configDir}
		for key, value := range profile["env"].(map[string]string) {
			env[key] = value
		}
		c := LoadPlatformConfig("wechat", home, home, env, func(string) {})
		wc, wecom, qq := WechatConfig(c), WecomConfig(c), QQConfig(c)
		rows = append(rows, map[string]any{"wechat": map[string]any{"accountId": wc.AccountID, "botToken": wc.BotToken, "baseUrl": wc.BaseURL, "userId": wc.UserID}, "wecom": map[string]any{"botId": wecom.BotID, "secret": wecom.Secret}, "qq": map[string]any{"appId": qq.AppID, "appSecret": qq.AppSecret}})
	}
	exactJSON(t, protocol.StringifyValidatedJavaScriptJSON(raw(rows)), want, "actual Source platform credential shape")
}
