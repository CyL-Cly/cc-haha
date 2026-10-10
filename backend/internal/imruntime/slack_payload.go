package imruntime

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"regexp"
	"strings"
)

type SlackInboundPayload struct {
	ChatID   string                `json:"chatId"`
	UserID   string                `json:"userId"`
	Text     string                `json:"text"`
	DedupKey string                `json:"dedupKey"`
	ThreadTs *string               `json:"threadTs,omitempty"`
	Files    []protocol.JSONObject `json:"files"`
}

var slackMention = regexp.MustCompile(`<@([A-Z0-9]+)(\|[^>]*)?>`)
var slackChannel = regexp.MustCompile(`<#([A-Z0-9]+)\|([^>]*)>`)
var slackLabeled = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]*)>`)
var slackBare = regexp.MustCompile(`<(https?://[^|>]+)>`)

func ExtractSlackPayload(event protocol.JSONObject, botUserID string) *SlackInboundPayload {
	if event.String("type") != "message" || truthy(event.Get("subtype")) && event.String("subtype") != "file_share" || truthy(event.Get("hidden")) || truthy(event.Get("bot_id")) || event.String("channel_type") != "im" {
		return nil
	}
	chat, user := protocol.TrimJavaScriptString(event.String("channel")), protocol.TrimJavaScriptString(event.String("user"))
	if chat == "" || user == "" || botUserID != "" && user == botUserID {
		return nil
	}
	files := []protocol.JSONObject{}
	for _, file := range collectJSONArray(event.Get("files")) {
		if truthy(file.Get("url_private_download")) || truthy(file.Get("url_private")) {
			files = append(files, file)
		}
	}
	text := slackMention.ReplaceAllString(event.String("text"), "")
	text = slackChannel.ReplaceAllString(text, "#$2")
	text = slackLabeled.ReplaceAllString(text, "$2")
	text = slackBare.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(text, "&lt;", "<"), "&gt;", ">"), "&amp;", "&")
	text = protocol.TrimJavaScriptString(text)
	if text == "" && len(files) == 0 {
		return nil
	}
	key := protocol.TrimJavaScriptString(event.String("client_msg_id"))
	if key == "" {
		key = chat + ":" + event.String("ts")
	}
	var thread *string
	if v := event.Get("thread_ts"); len(v) > 0 && string(v) != "null" {
		s := event.String("thread_ts")
		thread = &s
	}
	return &SlackInboundPayload{chat, user, text, key, thread, files}
}
