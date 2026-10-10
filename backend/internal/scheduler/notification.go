package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NotificationOptions consumes the ordinary private adapter config producer.
// It never starts an IM adapter, searches accounts, or substitutes a masked
// settings response for the original raw credentials.
type NotificationOptions struct {
	GetRawConfig func(context.Context) (protocol.JSONObject, error)
	Do           func(*http.Request) (*http.Response, error)
	Log          func(level, message string, details any)
}
type Notifier struct{ options NotificationOptions }

func NewNotifier(o NotificationOptions) (*Notifier, error) {
	if o.GetRawConfig == nil || o.Do == nil || o.Log == nil {
		return nil, &CapabilityError{"actual raw adapter config / HTTP / notification log producers"}
	}
	return &Notifier{o}, nil
}
func jsString(value json.RawMessage) json.RawMessage {
	out, e := protocol.ExactJavaScriptStringJSON(value)
	if e != nil {
		return quoted("undefined")
	}
	return out
}
func decodedUnits(v json.RawMessage) []uint16 {
	units, _ := protocol.DecodeJavaScriptJSONString(v)
	return units
}
func concatText(parts ...json.RawMessage) json.RawMessage {
	out := []uint16{}
	for _, part := range parts {
		out = append(out, decodedUnits(part)...)
	}
	return protocol.QuoteJavaScriptUTF16(out)
}
func joinText(parts []json.RawMessage, separator string) json.RawMessage {
	out := []uint16{}
	for n, part := range parts {
		if n > 0 {
			out = append(out, utf16.Encode([]rune(separator))...)
		}
		out = append(out, decodedUnits(part)...)
	}
	return protocol.QuoteJavaScriptUTF16(out)
}
func cappedText(v json.RawMessage, limit int) json.RawMessage {
	units := decodedUnits(v)
	if len(units) > limit {
		units = append(append([]uint16{}, units[:limit]...), 0x2026)
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func durationText(ms float64) string {
	seconds := math.Floor(ms/1000 + 0.5)
	if ms < 60000 {
		return strconv.FormatFloat(seconds, 'f', 0, 64) + "s"
	}
	minutes := math.Floor(ms / 60000)
	seconds = math.Floor(math.Mod(ms, 60000)/1000 + 0.5)
	if seconds > 0 {
		return fmt.Sprintf("%.0fm %.0fs", minutes, seconds)
	}
	return fmt.Sprintf("%.0fm", minutes)
}
func statusParts(status string) (string, string) {
	switch status {
	case "completed":
		return "✅", "Completed"
	case "failed":
		return "❌", "Failed"
	case "timeout":
		return "⏰", "Timeout"
	}
	return "ℹ️", status
}
func notificationMeta(run protocol.JSONObject) []json.RawMessage {
	_, status := statusParts(run.String("status"))
	meta := []json.RawMessage{quoted("**Status**: " + status)}
	if duration := nullableNumber(run, "durationMs"); duration != nil {
		meta = append(meta, quoted("**Duration**: "+durationText(duration.(float64))))
	}
	return meta
}
func notificationMarkdown(run protocol.JSONObject) json.RawMessage {
	emoji, _ := statusParts(run.String("status"))
	lines := []json.RawMessage{concatText(quoted(emoji+" **"), jsString(run.Get("taskName")), quoted("**")), quoted("")}
	lines = append(lines, notificationMeta(run)...)
	if run.String("status") == "failed" && jsTruthy(run.Get("error")) {
		lines = append(lines, quoted(""), quoted("**Error**:"), cappedText(run.Get("error"), 500))
	}
	if jsTruthy(run.Get("output")) {
		lines = append(lines, quoted(""), quoted("**Result**:"), cappedText(run.Get("output"), 2000))
	}
	return joinText(lines, "\n")
}
func notificationCard(run protocol.JSONObject) protocol.JSONObject {
	emoji, _ := statusParts(run.String("status"))
	template := "red"
	if run.String("status") == "completed" {
		template = "green"
	}
	parts := []json.RawMessage{}
	if run.String("status") == "failed" && jsTruthy(run.Get("error")) {
		parts = append(parts, concatText(quoted("**Error**:\n"), cappedText(run.Get("error"), 500)))
	}
	if jsTruthy(run.Get("output")) {
		parts = append(parts, cappedText(run.Get("output"), 3000))
	}
	element := func(content json.RawMessage) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "tag", Value: quoted("markdown")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "text_align", Value: quoted("left")})
	}
	elements := []protocol.JSONObject{element(joinText(notificationMeta(run), "　　"))}
	if len(parts) > 0 {
		elements = append(elements, protocol.NewJSONObject(protocol.JSONField{Name: "tag", Value: quoted("hr")}), element(joinText(parts, "\n\n")))
	}
	title := protocol.NewJSONObject(protocol.JSONField{Name: "tag", Value: quoted("plain_text")}, protocol.JSONField{Name: "content", Value: concatText(quoted(emoji+" "), jsString(run.Get("taskName")))})
	return protocol.NewJSONObject(protocol.JSONField{Name: "schema", Value: quoted("2.0")}, protocol.JSONField{Name: "header", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "template", Value: quoted(template)}, protocol.JSONField{Name: "title", Value: raw(title)}))}, protocol.JSONField{Name: "body", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "elements", Value: raw(elements)}))})
}
func (n *Notifier) post(ctx context.Context, url string, payload protocol.JSONObject, token string) (*http.Response, error) {
	b, e := payload.MarshalJSON()
	if e != nil {
		return nil, e
	}
	b = protocol.StringifyValidatedJavaScriptJSON(b)
	request, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return n.options.Do(request)
}
func (n *Notifier) handleStatus(response *http.Response, channel string) {
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return
	}
	body, _ := io.ReadAll(response.Body)
	n.options.Log("error", fmt.Sprintf("[Notification] %s send failed (%d):", channel, response.StatusCode), string(protocol.DecodeJavaScriptUTF8(body)))
}
func (n *Notifier) feishuToken(ctx context.Context, config protocol.JSONObject) string {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "app_id", Value: config.Get("appId")}, protocol.JSONField{Name: "app_secret", Value: config.Get("appSecret")})
	response, e := n.post(ctx, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", payload, "")
	if e != nil {
		n.options.Log("error", "[Notification] Feishu token error:", e)
		return ""
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	var body protocol.JSONObject
	if e != nil || body.UnmarshalJSON(data) != nil {
		n.options.Log("error", "[Notification] Feishu token error:", errors.New("invalid Feishu token response"))
		return ""
	}
	if string(body.Get("code")) == "0" && jsTruthy(body.Get("tenant_access_token")) {
		return string(utf16.Decode(decodedUnits(jsString(body.Get("tenant_access_token")))))
	}
	n.options.Log("error", "[Notification] Feishu token failed:", body)
	return ""
}
func usersFor(config protocol.JSONObject) ([]json.RawMessage, error) {
	var paired, allowed []json.RawMessage
	for key, target := range map[string]*[]json.RawMessage{"pairedUsers": &paired, "allowedUsers": &allowed} {
		value := config.Get(key)
		if value == nil || string(value) == "null" {
			continue
		}
		if json.Unmarshal(value, target) != nil {
			return nil, &CapabilityError{"original malformed notification " + key + " iterable"}
		}
	}
	users := []json.RawMessage{}
	for _, v := range paired {
		var row protocol.JSONObject
		if row.UnmarshalJSON(v) != nil || !row.IsObject() {
			return nil, &CapabilityError{"original malformed paired user property lookup"}
		}
		users = append(users, row.Get("userId"))
	}
	users = append(users, allowed...)
	return users, nil
}
func (n *Notifier) Send(ctx context.Context, run, notification protocol.JSONObject) error {
	if !jsTruthy(notification.Get("enabled")) {
		return nil
	}
	var channels []string
	if json.Unmarshal(notification.Get("channels"), &channels) != nil {
		return &CapabilityError{"original notification channels.filter"}
	}
	active := []string{}
	for _, channel := range channels {
		if channel == "telegram" || channel == "feishu" {
			active = append(active, channel)
		}
	}
	if len(active) == 0 {
		return nil
	}
	config, e := n.options.GetRawConfig(ctx)
	if e != nil {
		n.options.Log("error", "[Notification] Failed to read adapter config:", e)
		return nil
	}
	if !config.IsObject() {
		return &CapabilityError{"original null adapter config property lookup"}
	}
	if _, e := protocol.ExactJavaScriptStringJSON(run.Get("taskName")); e != nil {
		return &CapabilityError{"original taskName template coercion"}
	}
	for _, key := range []string{"error", "output"} {
		if value := run.Get(key); jsTruthy(value) {
			if _, e := protocol.DecodeJavaScriptJSONString(value); e != nil {
				return &CapabilityError{"original non-string notification " + key + " truncation"}
			}
		}
	}
	if v := run.Get("durationMs"); v != nil && string(v) != "null" && nullableNumber(run, "durationMs") == nil {
		return &CapabilityError{"original non-number notification duration coercion"}
	}
	markdown := notificationMarkdown(run)
	for _, channel := range active {
		e := func() error {
			var selected protocol.JSONObject
			if e := selected.UnmarshalJSON(config.Get(channel)); e != nil || !selected.IsObject() {
				selected = protocol.NewJSONObject()
			}
			if channel == "telegram" && !jsTruthy(selected.Get("botToken")) {
				n.options.Log("warn", "[Notification] Telegram botToken not configured, skipping", nil)
				return nil
			}
			if channel == "feishu" && (!jsTruthy(selected.Get("appId")) || !jsTruthy(selected.Get("appSecret"))) {
				n.options.Log("warn", "[Notification] Feishu credentials not configured, skipping", nil)
				return nil
			}
			token := ""
			if channel == "feishu" {
				token = n.feishuToken(ctx, selected)
				if token == "" {
					return nil
				}
			}
			users, e := usersFor(selected)
			if e != nil {
				return e
			}
			for _, user := range users {
				var response *http.Response
				if channel == "telegram" {
					payload := protocol.NewJSONObject(protocol.JSONField{Name: "chat_id", Value: user}, protocol.JSONField{Name: "text", Value: cappedText(markdown, 4000)}, protocol.JSONField{Name: "parse_mode", Value: quoted("Markdown")})
					response, e = n.post(ctx, "https://api.telegram.org/bot"+string(utf16.Decode(decodedUnits(jsString(selected.Get("botToken")))))+"/sendMessage", payload, "")
				} else {
					card, _ := notificationCard(run).MarshalJSON()
					content := quoted(string(protocol.StringifyValidatedJavaScriptJSON(card)))
					payload := protocol.NewJSONObject(protocol.JSONField{Name: "receive_id", Value: jsString(user)}, protocol.JSONField{Name: "msg_type", Value: quoted("interactive")}, protocol.JSONField{Name: "content", Value: content})
					response, e = n.post(ctx, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id", payload, token)
				}
				if e != nil {
					return e
				}
				label := "Telegram"
				if channel == "feishu" {
					label = "Feishu"
				}
				n.handleStatus(response, label)
			}
			return nil
		}()
		if e != nil {
			n.options.Log("error", "[Notification] "+channel+" notification error:", e)
		}
	}
	return nil
}
