package imruntime

import (
	"bytes"
	"encoding/json"
	"net/url"
)

const slackManifest = `{"display_information":{"name":"Claude Code Haha","description":"Drive a local Claude Code session from Slack direct messages.","background_color":"#1a1a1a"},"features":{"bot_user":{"display_name":"Claude Code Haha","always_online":false},"app_home":{"home_tab_enabled":false,"messages_tab_enabled":true,"messages_tab_read_only_enabled":false}},"oauth_config":{"scopes":{"bot":["chat:write","im:history","files:write","files:read","users:read"]}},"settings":{"event_subscriptions":{"bot_events":["message.im"]},"interactivity":{"is_enabled":false},"org_deploy_enabled":false,"socket_mode_enabled":true,"token_rotation_enabled":false}}`

func SlackManifestJSON() string {
	var b bytes.Buffer
	_ = json.Indent(&b, []byte(slackManifest), "", "  ")
	return b.String()
}
func SlackCreateAppURL() string {
	return "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(SlackManifestJSON())
}
