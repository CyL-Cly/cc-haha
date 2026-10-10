package imregistration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

const FeishuRequestTimeout = 15 * time.Second
const feishuSessionTTLMillis = 30 * 60 * 1000
const feishuRegistrationPath = "/oauth/v1/app/registration"

type FeishuOptions struct {
	Client *http.Client
	Now    func() time.Time
	UUID   func() string
}

type feishuSession struct {
	deviceCode             string
	accountsDomain, domain string
	intervalSeconds        float64
	domainSwitched         bool
	startedAt, expiresAt   float64
}

type FeishuService struct {
	options  FeishuOptions
	mu       sync.Mutex
	sessions map[string]*feishuSession
}

func NewFeishu(o FeishuOptions) (*FeishuService, error) {
	if o.Client == nil {
		return nil, fmt.Errorf("Feishu registration requires actual HTTP client")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	return &FeishuService{options: o, sessions: map[string]*feishuSession{}}, nil
}

func feishuString(raw json.RawMessage) (string, error) {
	value, e := protocol.ExactJavaScriptStringJSON(raw)
	if e != nil {
		return "", e
	}
	units, e := protocol.DecodeJavaScriptJSONString(value)
	return string(utf16.Decode(units)), e
}
func feishuKey(raw json.RawMessage) (string, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", e
	}
	p := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(p[i*2:], v)
	}
	return string(p), nil
}
func feishuIsString(raw json.RawMessage, value string) bool {
	if len(raw) == 0 || raw[0] != '"' {
		return false
	}
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	return e == nil && string(utf16.Decode(units)) == value
}
func feishuProperty(raw json.RawMessage, name string) json.RawMessage {
	var object protocol.JSONObject
	if object.UnmarshalJSON(raw) == nil {
		return object.Get(name)
	}
	return nil
}
func (s *FeishuService) purgeLocked() {
	now := float64(s.options.Now().UnixMilli())
	for key, v := range s.sessions {
		if now-v.startedAt > feishuSessionTTLMillis || now > v.expiresAt {
			delete(s.sessions, key)
		}
	}
}

// AssertFeishuVerificationURL applies only the original registration whitelist.
func AssertFeishuVerificationURL(raw json.RawMessage) (*whatwg.Url, error) {
	if !truthy(raw) {
		return nil, fmt.Errorf("Feishu registration returned no verification URL")
	}
	text, e := feishuString(raw)
	if e != nil {
		return nil, e
	}
	u, e := whatwg.Parse(text)
	if e != nil {
		return nil, fmt.Errorf("Feishu registration returned an invalid verification URL")
	}
	allowed := u.Hostname() == "accounts.feishu.cn" || u.Hostname() == "open.feishu.cn" || u.Hostname() == "accounts.larksuite.com" || u.Hostname() == "open.larksuite.com"
	if u.Protocol() != "https:" || !allowed || (u.Port() != "" && u.Port() != "443") || u.Username() != "" || u.Password() != "" {
		return nil, fmt.Errorf("Feishu registration returned an unsafe verification URL")
	}
	return u, nil
}

// Source adapters/feishu/registration.ts defaultAddons is a fixed authorization
// declaration. This is its Node gzip/base64url representation, rather than a
// compressor-dependent spelling of the same JSON. Actual Source tests pin the
// bytes and decompressed declaration. It is not used for dynamic payloads.
const feishuDefaultAddonsWire = "H4sIAAAAAAAAE3WO0QqDMAxF_yXPImyP_RWREutdKdpWkswxhv8-dAyEsadAzr3JedEiUBi5G8-KhjTUBUruRYbCxch1lLLLUOWIdrkuPmt0Ah5rmZ_UnOifraKMntUP1T5AoPUuAdRQYBmnZG6f7iHJQP3WEFYUOyySIf_otF8dQUBa4dcL9du2n5vngcN0rnbHk5aDpVpakxQjZI-_AS40AqL8AAAA"

func (s *FeishuService) post(ctx context.Context, domain string, pairs ...[2]string) (json.RawMessage, error) {
	u, _ := whatwg.Parse("https://" + domain + feishuRegistrationPath)
	params := u.SearchParams()
	for _, p := range pairs {
		params.Append(p[0], p[1])
	}
	timeout, cancel := context.WithTimeout(ctx, FeishuRequestTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(timeout, "POST", "https://"+domain+feishuRegistrationPath, strings.NewReader(feishuForm(params)))
	if e != nil {
		return nil, e
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, e := s.options.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	raw = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(raw), []byte{0xef, 0xbb, 0xbf})
	if e != nil || !json.Valid(raw) || !truthy(raw) {
		return nil, fmt.Errorf("Feishu registration returned HTTP %d", response.StatusCode)
	}
	return raw, nil
}

func (s *FeishuService) Begin(ctx context.Context, options protocol.JSONObject) (protocol.JSONObject, error) {
	s.mu.Lock()
	s.purgeLocked()
	s.mu.Unlock()
	domain, accounts := "feishu", "accounts.feishu.cn"
	if options.String("domain") == "lark" {
		domain, accounts = "lark", "accounts.larksuite.com"
	}
	begun, e := s.post(ctx, accounts, [2]string{"action", "begin"}, [2]string{"archetype", "PersonalAgent"}, [2]string{"auth_method", "client_secret"}, [2]string{"request_user_info", "open_id"})
	if e != nil {
		return record(), e
	}
	if errorValue := feishuProperty(begun, "error"); truthy(errorValue) {
		description := feishuProperty(begun, "error_description")
		if !truthy(description) {
			description = errorValue
		}
		text, e := feishuString(description)
		if e != nil {
			return record(), e
		}
		return record(), fmt.Errorf("Feishu registration failed: %s", text)
	}
	deviceRaw := feishuProperty(begun, "device_code")
	if len(deviceRaw) == 0 || string(deviceRaw) == "null" {
		return record(), fmt.Errorf("Feishu registration returned no device code")
	}
	if deviceRaw[0] != '"' {
		return record(), fmt.Errorf("begun.device_code?.trim is not a function. (In 'begun.device_code?.trim()', 'begun.device_code?.trim' is undefined)")
	}
	_, device, e := trimString(deviceRaw)
	if e != nil {
		return record(), e
	}
	if device == "" {
		return record(), fmt.Errorf("Feishu registration returned no device code")
	}
	verification, e := AssertFeishuVerificationURL(feishuProperty(begun, "verification_uri_complete"))
	if e != nil {
		return record(), e
	}
	params := feishuParams(verification)
	params.Set("from", "sdk")
	source := options.Get("source")
	if !truthy(source) {
		source = encoded("claude-code-haha")
	}
	text, e := feishuString(source)
	if e != nil {
		return record(), e
	}
	params.Set("source", text)
	params.Set("tp", "sdk")
	params.Set("createOnly", "true")
	for _, p := range [][2]string{{"appName", "name"}, {"appDescription", "desc"}} {
		if v := options.Get(p[0]); truthy(v) {
			text, e := feishuString(v)
			if e != nil {
				return record(), e
			}
			params.Set(p[1], text)
		}
	}
	params.Set("addons", feishuDefaultAddonsWire)
	expires, e := duration(feishuProperty(begun, "expires_in"), 600)
	if e != nil {
		return record(), e
	}
	interval, e := duration(feishuProperty(begun, "interval"), 5)
	if e != nil {
		return record(), e
	}
	keyRaw := options.Get("sessionKey")
	if !truthy(keyRaw) {
		keyRaw = encoded(s.options.UUID())
	}
	key, e := feishuKey(keyRaw)
	if e != nil {
		return record(), e
	}
	startedAt := float64(s.options.Now().UnixMilli())
	s.mu.Lock()
	s.sessions[key] = &feishuSession{deviceCode: device, accountsDomain: accounts, domain: domain, intervalSeconds: interval, startedAt: startedAt, expiresAt: startedAt + expires*1000}
	s.mu.Unlock()
	return record(protocol.JSONField{Name: "sessionKey", Value: keyRaw}, field("verificationUri", feishuFormURL(verification)), field("expiresInSeconds", expires), field("intervalSeconds", interval), field("message", "使用飞书扫描二维码，确认后会自动创建机器人。")), nil
}
func (s *FeishuService) BeginDesktop(ctx context.Context) (protocol.JSONObject, error) {
	result, e := s.Begin(ctx, record(field("appName", "Claude Code Haha"), field("appDescription", "把飞书私聊接到本机的 Claude Code 会话。")))
	if e == nil {
		if qr, e := qrDataURL(result.String("verificationUri")); e == nil {
			result.Set("qrDataUrl", encoded(qr))
		}
	}
	return result, e
}
func (s *FeishuService) CancelJSON(keyRaw json.RawMessage) error {
	key, e := feishuKey(keyRaw)
	if e != nil {
		return e
	}
	s.mu.Lock()
	delete(s.sessions, key)
	s.mu.Unlock()
	return nil
}
func (s *FeishuService) PollJSON(ctx context.Context, keyRaw json.RawMessage) (protocol.JSONObject, error) {
	key, e := feishuKey(keyRaw)
	if e != nil {
		return record(), e
	}
	s.mu.Lock()
	s.purgeLocked()
	session := s.sessions[key]
	if session == nil {
		s.mu.Unlock()
		return record(field("status", "not_started"), field("message", "当前没有进行中的飞书扫码，请重新生成二维码。")), nil
	}
	accounts, device := session.accountsDomain, session.deviceCode
	s.mu.Unlock()
	data, e := s.post(ctx, accounts, [2]string{"action", "poll"}, [2]string{"device_code", device})
	if e != nil {
		return record(), e
	}
	switched := false
	s.mu.Lock()
	if feishuIsString(feishuProperty(feishuProperty(data, "user_info"), "tenant_brand"), "lark") && !session.domainSwitched {
		session.domainSwitched = true
		session.domain = "lark"
		session.accountsDomain = "accounts.larksuite.com"
		switched = true
	}
	accounts = session.accountsDomain
	s.mu.Unlock()
	if switched {
		data, e = s.post(ctx, accounts, [2]string{"action", "poll"}, [2]string{"device_code", device})
		if e != nil {
			return record(), e
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, secret := feishuProperty(data, "client_id"), feishuProperty(data, "client_secret")
	if truthy(id) && truthy(secret) {
		delete(s.sessions, key)
		result := record(field("status", "success"), protocol.JSONField{Name: "appId", Value: id}, protocol.JSONField{Name: "appSecret", Value: secret}, field("domain", session.domain))
		if openID := feishuProperty(feishuProperty(data, "user_info"), "open_id"); len(openID) > 0 {
			result.Set("openId", openID)
		}
		return result, nil
	}
	errorRaw := feishuProperty(data, "error")
	status, message := "failed", "飞书扫码授权失败，请重试。"
	switch {
	case len(errorRaw) == 0, feishuIsString(errorRaw, ""), feishuIsString(errorRaw, "authorization_pending"):
		status = "waiting"
	case feishuIsString(errorRaw, "slow_down"):
		session.intervalSeconds += 5
		status = "waiting"
	case feishuIsString(errorRaw, "expired_token"):
		status, message = "expired", "二维码已过期，请重新生成。"
	case feishuIsString(errorRaw, "access_denied"):
		status, message = "denied", "飞书授权被拒绝。"
	}
	if status == "waiting" {
		return record(field("status", status), field("intervalSeconds", session.intervalSeconds), field("message", "等待飞书扫码确认...")), nil
	}
	delete(s.sessions, key)
	return record(field("status", status), field("message", message)), nil
}

func feishuForm(params *whatwg.SearchParams) string {
	var parts []string
	params.Iterate(func(pair *whatwg.NameValuePair) {
		parts = append(parts, feishuFormEscape(pair.Name)+"="+feishuFormEscape(pair.Value))
	})
	return strings.Join(parts, "&")
}
func feishuFormEscape(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9', b == '*' || b == '-' || b == '.' || b == '_':
			out.WriteByte(b)
		case b == ' ':
			out.WriteByte('+')
		default:
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}
func feishuFormURL(u *whatwg.Url) string {
	u.SetSearch(feishuForm(u.SearchParams()))
	return u.Href(false)
}

// The URL library's query parser decodes percent-encoded plus before replacing
// '+' with space. Rebuild form pairs in WHATWG order using decode-after-plus.
func feishuParams(u *whatwg.Url) *whatwg.SearchParams {
	query := strings.TrimPrefix(u.Search(), "?")
	u.SetSearch("")
	params := u.SearchParams()
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		fields := strings.SplitN(pair, "=", 2)
		name, e := url.QueryUnescape(fields[0])
		if e != nil {
			name = whatwgFormDecode(fields[0])
		}
		value := ""
		if len(fields) > 1 {
			value, e = url.QueryUnescape(fields[1])
			if e != nil {
				value = whatwgFormDecode(fields[1])
			}
		}
		params.Append(string(protocol.DecodeJavaScriptUTF8([]byte(name))), string(protocol.DecodeJavaScriptUTF8([]byte(value))))
	}
	return params
}
func whatwgFormDecode(value string) string {
	value = strings.ReplaceAll(value, "+", " ")
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			a, b := feishuHex(value[i+1]), feishuHex(value[i+2])
			if a >= 0 && b >= 0 {
				out.WriteByte(byte(a<<4 | b))
				i += 2
				continue
			}
		}
		out.WriteByte(value[i])
	}
	return out.String()
}

func feishuHex(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}
