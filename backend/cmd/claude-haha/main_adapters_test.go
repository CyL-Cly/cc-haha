package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopAdapterModeDispatchesBeforeCLIParserWithPrivateLegacyConfig(t *testing.T) {
	for _, tc := range []struct {
		name, config, stderr string
		args                 []string
		code                 int
	}{
		{"missing", `{"future":{"keep":true}}`, "[claude-sidecar] --telegram requested but TELEGRAM_BOT_TOKEN missing in env or ~/.claude/adapters.json — skipping\n[claude-sidecar] no adapter could be started — check credentials in env or ~/.claude/adapters.json\n", []string{"adapters", "--app-root", "/private/fixture/app.asar", "--telegram"}, 1},
		{"missing-feishu-secret", `{"feishu":{"appId":"fake-only-no-platform-start"},"future":{"keep":true}}`, "[claude-sidecar] --feishu requested but FEISHU_APP_ID / FEISHU_APP_SECRET missing in env or ~/.claude/adapters.json — skipping\n[claude-sidecar] no adapter could be started — check credentials in env or ~/.claude/adapters.json\n", []string{"adapters", "--app-root", "/private/fixture/app.asar", "--feishu"}, 1},
		{"consumed-flag", `{}`, "claude-sidecar adapters: must enable at least one of --feishu / --telegram / --wechat / --dingtalk / --whatsapp / --wecom / --qq / --slack\n", []string{"adapters", "--app-root", "--telegram"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config")
			for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
				t.Setenv(key, root)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			// This route precedes recovery selection and uses only its own gates.
			t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
			for _, key := range []string{"TELEGRAM_BOT_TOKEN", "FEISHU_APP_ID", "FEISHU_APP_SECRET", "WECHAT_ACCOUNT_ID", "WECHAT_BOT_TOKEN", "DINGTALK_CLIENT_ID", "DINGTALK_CLIENT_SECRET", "WECOM_BOT_ID", "WECOM_BOT_SECRET", "QQ_APP_ID", "QQ_APP_SECRET", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "WHATSAPP_AUTH_DIR"} {
				t.Setenv(key, "")
			}
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(config, "adapters.json")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			previous := os.Stderr
			os.Stderr = write
			defer func() { os.Stderr = previous; _ = write.Close() }()
			code := run(context.Background(), tc.args)
			_ = write.Close()
			os.Stderr = previous
			stderr, err := io.ReadAll(read)
			if err != nil || code != tc.code || string(stderr) != tc.stderr {
				t.Fatalf("public adapter dispatch: code=%d stderr=%q err=%v", code, stderr, err)
			}
			persisted, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(persisted, []byte(tc.config)) {
				t.Fatalf("launcher changed legacy config: %s %v", persisted, err)
			}
			entries, err := os.ReadDir(config)
			if err != nil || len(entries) != 1 || entries[0].Name() != "adapters.json" {
				t.Fatalf("adapter gate created hidden state: %v %v", entries, err)
			}
		})
	}
}
