package imregistration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

const WecomRequestTimeout = 10 * time.Second
const wecomQRMillis = 5 * 60 * 1000
const WecomPollIntervalMillis = 3000

type WecomOptions struct {
	Client   *http.Client
	Platform string
	Now      func() time.Time
	UUID     func() string
}
type wecomLogin struct {
	scode, verificationURL string
	startedAt              int64
}
type WecomService struct {
	options WecomOptions
	mu      sync.Mutex
	logins  map[string]*wecomLogin
}

func NewWecom(o WecomOptions) (*WecomService, error) {
	if o.Client == nil {
		return nil, fmt.Errorf("WeCom registration requires actual HTTP client")
	}
	client := *o.Client
	// Original fetch redirect:error forbids every redirect, including same-origin.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return fmt.Errorf("WeCom QR service redirect is forbidden")
	}
	o.Client = &client
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	if o.Platform == "windows" {
		o.Platform = "win32"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	return &WecomService{options: o, logins: map[string]*wecomLogin{}}, nil
}
func (s *WecomService) purgeLocked() {
	now := s.options.Now().UnixMilli()
	for key, session := range s.logins {
		if now-session.startedAt >= wecomQRMillis {
			delete(s.logins, key)
		}
	}
}
func wecomCleanRaw(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return nil
	}
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	if len(units) == 0 {
		return nil
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func wecomClean(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	_, text, e := trimString(raw)
	if e != nil {
		return ""
	}
	return text
}
func wecomVerificationURL(raw json.RawMessage) string {
	text := wecomClean(raw)
	if text == "" {
		return ""
	}
	u, e := whatwg.Parse(text)
	if e != nil {
		return ""
	}
	if u.Protocol() != "https:" || u.Hostname() != "work.weixin.qq.com" || (u.Port() != "" && u.Port() != "443") || u.Username() != "" || u.Password() != "" {
		return ""
	}
	return u.Href(false)
}
func (s *WecomService) request(ctx context.Context, path string, pairs ...[2]string) (protocol.JSONObject, error) {
	u, _ := whatwg.Parse("https://work.weixin.qq.com/ai/qc/" + path)
	params := u.SearchParams()
	for _, pair := range pairs {
		params.Set(pair[0], pair[1])
	}
	target := feishuFormURL(u)
	timeout, cancel := context.WithTimeout(ctx, WecomRequestTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(timeout, "GET", target, nil)
	if e != nil {
		return record(), e
	}
	req.Header.Set("accept", "application/json")
	response, e := s.options.Client.Do(req)
	if e != nil {
		return record(), e
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return record(), fmt.Errorf("Enterprise WeChat QR service returned HTTP %d", response.StatusCode)
	}
	raw, e := io.ReadAll(response.Body)
	raw = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(raw), []byte{0xef, 0xbb, 0xbf})
	var body protocol.JSONObject
	if e != nil || body.UnmarshalJSON(raw) != nil || !body.IsObject() {
		return record(), fmt.Errorf("Enterprise WeChat QR service returned a non-JSON response")
	}
	return body, nil
}
func (s *WecomService) Start(ctx context.Context, opts protocol.JSONObject) (protocol.JSONObject, error) {
	s.mu.Lock()
	s.purgeLocked()
	s.mu.Unlock()
	keyRaw := opts.Get("sessionKey")
	if !truthy(keyRaw) {
		keyRaw = encoded(s.options.UUID())
	}
	key, e := feishuKey(keyRaw)
	if e != nil {
		return record(), e
	}
	s.mu.Lock()
	existing := s.logins[key]
	if !truthy(opts.Get("force")) && existing != nil && s.options.Now().UnixMilli()-existing.startedAt < wecomQRMillis {
		result := wecomStartResult(keyRaw, existing, "二维码已就绪，请使用企业微信扫描。")
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()
	source := opts.Get("source")
	if !truthy(source) {
		source = encoded("claude-code-haha")
	}
	sourceText, e := feishuString(source)
	if e != nil {
		return record(), e
	}
	plat := "1"
	if s.options.Platform == "win32" {
		plat = "2"
	} else if s.options.Platform == "linux" {
		plat = "3"
	}
	body, e := s.request(ctx, "generate", [2]string{"source", sourceText}, [2]string{"plat", plat})
	if e != nil {
		return record(), e
	}
	scode := wecomClean(feishuProperty(body.Get("data"), "scode"))
	verification := wecomVerificationURL(feishuProperty(body.Get("data"), "auth_url"))
	if scode == "" || verification == "" {
		return record(), fmt.Errorf("Enterprise WeChat QR service returned invalid data")
	}
	session := &wecomLogin{scode: scode, verificationURL: verification, startedAt: s.options.Now().UnixMilli()}
	s.mu.Lock()
	s.logins[key] = session
	s.mu.Unlock()
	return wecomStartResult(keyRaw, session, "使用企业微信扫描二维码，创建并授权智能机器人。"), nil
}
func wecomStartResult(key json.RawMessage, session *wecomLogin, message string) protocol.JSONObject {
	return record(protocol.JSONField{Name: "sessionKey", Value: key}, field("verificationUrl", session.verificationURL), field("expiresAt", session.startedAt+wecomQRMillis), field("pollIntervalMs", WecomPollIntervalMillis), field("message", message))
}
func (s *WecomService) StartDesktop(ctx context.Context) (protocol.JSONObject, error) {
	result, e := s.Start(ctx, record(field("force", true)))
	if e == nil {
		if qr, e := qrDataURL(result.String("verificationUrl")); e == nil {
			result.Set("qrDataUrl", encoded(qr))
		}
	}
	return result, e
}
func (s *WecomService) CancelJSON(raw json.RawMessage) error {
	key, e := feishuKey(raw)
	if e != nil {
		return e
	}
	s.mu.Lock()
	delete(s.logins, key)
	s.mu.Unlock()
	return nil
}
func (s *WecomService) PollJSON(ctx context.Context, raw json.RawMessage) (protocol.JSONObject, error) {
	key, e := feishuKey(raw)
	if e != nil {
		return record(), e
	}
	s.mu.Lock()
	s.purgeLocked()
	session := s.logins[key]
	s.mu.Unlock()
	if session == nil {
		return wecomPollResult("not_started", "当前没有进行中的企业微信绑定，请重新生成二维码。"), nil
	}
	body, e := s.request(ctx, "query_result", [2]string{"scode", session.scode})
	if e != nil {
		return record(), e
	}
	status := strings.ToLower(wecomClean(feishuProperty(body.Get("data"), "status")))
	result := wecomPollResult("waiting", "等待企业微信扫码确认...")
	switch status {
	case "success":
		info := feishuProperty(body.Get("data"), "bot_info")
		id, secret := wecomCleanRaw(feishuProperty(info, "botid")), wecomCleanRaw(feishuProperty(info, "secret"))
		if len(id) == 0 || len(secret) == 0 {
			result = wecomPollResult("failed", "企业微信返回的机器人凭据不完整，请重新扫码。")
		} else {
			result = record(field("connected", true), protocol.JSONField{Name: "botId", Value: id}, protocol.JSONField{Name: "secret", Value: secret})
		}
	case "expired", "timeout":
		result = wecomPollResult("expired", "二维码已过期，请重新生成。")
	case "fail", "failed", "error":
		result = wecomPollResult("failed", "企业微信授权失败，请重新扫码。")
	default:
		return result, nil
	}
	s.mu.Lock()
	delete(s.logins, key)
	s.mu.Unlock()
	return result, nil
}
func wecomPollResult(status, message string) protocol.JSONObject {
	return record(field("connected", false), field("status", status), field("message", message))
}
