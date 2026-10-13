package requestcontext

import (
	"encoding/json"
	"testing"
)

func TestOAuthMetadataGuardAndNativeResolvedEmpty(t *testing.T) {
	account, empty := "global-account", ""
	for _, row := range []struct {
		name     string
		env      map[string]string
		resolved *string
		want     string
	}{
		{"host account wins over unrelated CLI global", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "CLAUDE_CODE_ACCOUNT_UUID": "host-account"}, nil, "host-account"},
		{"third party direct key", map[string]string{"ANTHROPIC_API_KEY": "fake", "CLAUDE_CODE_ACCOUNT_UUID": "host-account"}, nil, ""},
		{"third party bearer", map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake", "CLAUDE_CODE_ACCOUNT_UUID": "host-account"}, nil, ""},
		{"bare disables OAuth", map[string]string{"CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ACCOUNT_UUID": "host-account"}, nil, ""},
		{"resolved global account", map[string]string{}, &account, "global-account"},
		{"native helper explicitly disabled", map[string]string{"CLAUDE_CODE_ACCOUNT_UUID": "wrong"}, &empty, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			raw, err := metadata(Context{Host: Host{Env: row.env, DeviceID: "device", SessionID: "session", OAuthAccountUUID: row.resolved}})
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				UserID string `json:"user_id"`
			}
			_ = json.Unmarshal(raw, &value)
			var decoded map[string]string
			_ = json.Unmarshal([]byte(value.UserID), &decoded)
			if decoded["account_uuid"] != row.want || decoded["device_id"] != "device" || decoded["session_id"] != "session" {
				t.Fatal(decoded, row.want)
			}
		})
	}
}
