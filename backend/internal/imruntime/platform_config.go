package imruntime

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

// PlatformCredential keeps loadConfig's env || file || default result as JSON.
// Transport boundaries apply their own original string operations; credentials
// must not be silently coerced before an SDK writes its subscription payload.
func PlatformCredential(c Config, platform, field, environment, fallback string) json.RawMessage {
	if value := c.Environment[environment]; value != "" {
		return raw(NewText(value))
	}
	value := object(c.Document.Get(platform)).Get(field)
	if credentialTruthy(value) {
		return append(json.RawMessage{}, value...)
	}
	return raw(NewText(fallback))
}

func credentialTruthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" || string(value) == "false" {
		return false
	}
	if value[0] == '"' {
		return len(TextJSON(value)) > 0
	}
	if value[0] == '-' || value[0] >= '0' && value[0] <= '9' {
		n, err := strconv.ParseFloat(string(value), 64)
		return (err == nil || math.IsInf(n, 0)) && n != 0
	}
	return true
}

type WechatCredentials struct {
	AccountID, BotToken, BaseURL, UserID json.RawMessage
}

func WechatConfig(c Config) WechatCredentials {
	return WechatCredentials{
		AccountID: PlatformCredential(c, "wechat", "accountId", "WECHAT_ACCOUNT_ID", ""),
		BotToken:  PlatformCredential(c, "wechat", "botToken", "WECHAT_BOT_TOKEN", ""),
		BaseURL:   PlatformCredential(c, "wechat", "baseUrl", "WECHAT_BASE_URL", "https://ilinkai.weixin.qq.com"),
		UserID:    PlatformCredential(c, "wechat", "userId", "WECHAT_USER_ID", ""),
	}
}

type WecomCredentials struct{ BotID, Secret json.RawMessage }

func WecomConfig(c Config) WecomCredentials {
	return WecomCredentials{
		BotID:  PlatformCredential(c, "wecom", "botId", "WECOM_BOT_ID", ""),
		Secret: PlatformCredential(c, "wecom", "secret", "WECOM_BOT_SECRET", ""),
	}
}

type QQCredentials struct{ AppID, AppSecret json.RawMessage }

func QQConfig(c Config) QQCredentials {
	return QQCredentials{
		AppID:     PlatformCredential(c, "qq", "appId", "QQ_APP_ID", ""),
		AppSecret: PlatformCredential(c, "qq", "appSecret", "QQ_APP_SECRET", ""),
	}
}
