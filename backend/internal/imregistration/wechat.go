package imregistration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

const WechatDefaultBaseURL = "https://ilinkai.weixin.qq.com"
const WechatQRStatusTimeout = 35 * time.Second
const WechatLoginTTLMillis int64 = 5 * 60 * 1000
const WechatClientVersion = "131335" // Source buildClientVersion("2.1.7")
type WechatOptions struct {
	Client *http.Client
	Now    func() time.Time
	UUID   func() string
}
type wechatLogin struct {
	qrcode, qrcodeURL json.RawMessage
	startedAt         int64
	base              string
}
type WechatService struct {
	options   WechatOptions
	mu        sync.Mutex
	logins    map[string]*wechatLogin
	ctx       context.Context
	cancel    context.CancelFunc
	requests  sync.WaitGroup
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

func NewWechat(o WechatOptions) (*WechatService, error) {
	if o.Client == nil {
		return nil, fmt.Errorf("WeChat registration requires actual HTTP client")
	}
	client := *o.Client
	client.Timeout = 0
	if client.Transport == nil {
		client.Transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	if tr, ok := client.Transport.(*http.Transport); ok {
		client.Transport = tr.Clone()
	}
	// Original fetch follows redirects (including cross-origin) up to 20 hops.
	client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) > 20 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	o.Client = &client
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WechatService{options: o, logins: map[string]*wechatLogin{}, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}
func (s *WechatService) operation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, fmt.Errorf("WeChat registration is closed")
	}
	s.requests.Add(1)
	owned, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return owned, func() { stop(); cancel(); s.requests.Done() }, nil
}
func (s *WechatService) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		clear(s.logins)
		s.mu.Unlock()
		go func() { s.requests.Wait(); s.options.Client.CloseIdleConnections(); close(s.done) }()
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *WechatService) purgeLocked() {
	for k, v := range s.logins {
		if s.options.Now().UnixMilli()-v.startedAt >= WechatLoginTTLMillis {
			delete(s.logins, k)
		}
	}
}
func (s *WechatService) get(ctx context.Context, base, endpoint, label string, timeout time.Duration) (json.RawMessage, error) {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	u, e := whatwg.ParseRef(base, endpoint)
	if e != nil {
		return nil, e
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.Href(false), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", WechatClientVersion)
	response, e := s.options.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	if e != nil {
		return nil, e
	}
	raw = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(raw), []byte{0xef, 0xbb, 0xbf})
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %d: %s", label, response.StatusCode, raw)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return bytes.TrimSpace(raw), nil
}
func (s *WechatService) Start(ctx context.Context, opts protocol.JSONObject) (protocol.JSONObject, error) {
	ctx, finish, e := s.operation(ctx)
	if e != nil {
		return record(), e
	}
	defer finish()
	// Source purges before UUID/key evaluation, retaining its clock cutpoint.
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
	if !truthy(opts.Get("force")) && existing != nil && s.options.Now().UnixMilli()-existing.startedAt < WechatLoginTTLMillis {
		s.mu.Unlock()
		return wechatStarted(keyRaw, existing, "二维码已就绪，请使用微信扫描。"), nil
	}
	s.mu.Unlock()
	botType := opts.Get("botType")
	if !truthy(botType) {
		botType = encoded("3")
	}
	encodedType, e := qqURIComponent(botType)
	if e != nil {
		return record(), e
	}
	raw, e := s.get(ctx, WechatDefaultBaseURL, "ilink/bot/get_bot_qrcode?bot_type="+encodedType, "wechatQrStart", 0)
	if e != nil {
		return record(), e
	}
	if string(raw) == "null" {
		return record(), fmt.Errorf("null is not an object (evaluating 'qr.qrcode')")
	}
	qr, image := feishuProperty(raw, "qrcode"), feishuProperty(raw, "qrcode_img_content")
	if !truthy(qr) || !truthy(image) {
		return record(), fmt.Errorf("WeChat QR response did not include a QR code URL")
	}
	login := &wechatLogin{qrcode: qr, qrcodeURL: image, base: WechatDefaultBaseURL, startedAt: s.options.Now().UnixMilli()}
	s.mu.Lock()
	if !s.closed {
		s.logins[key] = login
	}
	s.mu.Unlock()
	return wechatStarted(keyRaw, login, "使用微信扫描二维码完成绑定。"), nil
}
func wechatStarted(key json.RawMessage, login *wechatLogin, message string) protocol.JSONObject {
	return record(protocol.JSONField{Name: "qrcodeUrl", Value: login.qrcodeURL}, field("message", message), protocol.JSONField{Name: "sessionKey", Value: key})
}
func wechatPolled(status, message string) protocol.JSONObject {
	return record(field("connected", false), field("status", status), field("message", message))
}
func (s *WechatService) PollJSON(ctx context.Context, keyRaw json.RawMessage) (protocol.JSONObject, error) {
	ctx, finish, e := s.operation(ctx)
	if e != nil {
		return record(), e
	}
	defer finish()
	s.mu.Lock()
	s.purgeLocked()
	s.mu.Unlock()
	key, e := feishuKey(keyRaw)
	if e != nil {
		return record(), e
	}
	s.mu.Lock()
	login := s.logins[key]
	if login == nil {
		s.mu.Unlock()
		return wechatPolled("not_started", "当前没有进行中的微信绑定，请重新生成二维码。"), nil
	}
	base, qr := login.base, append(json.RawMessage(nil), login.qrcode...)
	s.mu.Unlock()
	encodedQR, e := qqURIComponent(qr)
	var raw json.RawMessage
	if e == nil {
		raw, e = s.get(ctx, base, "ilink/bot/get_qrcode_status?qrcode="+encodedQR, "wechatQrStatus", WechatQRStatusTimeout)
	}
	// Every encode, HTTP, JSON and network failure inside pollQrStatus becomes wait.
	if e != nil {
		raw = encoded(map[string]string{"status": "wait"})
	}
	if string(raw) == "null" {
		return record(), fmt.Errorf("null is not an object (evaluating 'status.status')")
	}
	status := feishuProperty(raw, "status")
	switch {
	case feishuIsString(status, "wait"):
		return wechatPolled("wait", "等待扫码。"), nil
	case feishuIsString(status, "scaned"):
		return wechatPolled("scaned", "已扫码，请在微信中确认。"), nil
	case feishuIsString(status, "scaned_but_redirect"):
		if host := feishuProperty(raw, "redirect_host"); truthy(host) {
			text, e := feishuString(host)
			if e != nil {
				return record(), e
			}
			s.mu.Lock()
			login.base = "https://" + text
			s.mu.Unlock()
		}
		return wechatPolled("scaned_but_redirect", "已扫码，正在切换微信网关。"), nil
	case feishuIsString(status, "expired"):
		s.mu.Lock()
		delete(s.logins, key)
		s.mu.Unlock()
		return wechatPolled("expired", "二维码已过期，请重新生成。"), nil
	case feishuIsString(status, "confirmed"):
		s.mu.Lock()
		delete(s.logins, key)
		base = login.base
		s.mu.Unlock()
		token, id := feishuProperty(raw, "bot_token"), feishuProperty(raw, "ilink_bot_id")
		if !truthy(token) || !truthy(id) {
			return wechatPolled("confirmed", "微信已确认，但服务端未返回完整凭据。"), nil
		}
		apiBase := feishuProperty(raw, "baseurl")
		if !truthy(apiBase) {
			apiBase = encoded(base)
		}
		return record(field("connected", true), field("status", "confirmed"), field("message", "微信绑定成功。"), protocol.JSONField{Name: "botToken", Value: token}, protocol.JSONField{Name: "accountId", Value: id}, protocol.JSONField{Name: "baseUrl", Value: apiBase}, protocol.JSONField{Name: "userId", Value: feishuProperty(raw, "ilink_user_id")}), nil
	}
	// Original switch has no default and returns undefined for unknown statuses.
	return protocol.JSONObject{}, nil
}
