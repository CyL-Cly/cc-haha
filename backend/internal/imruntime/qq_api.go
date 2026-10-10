package imruntime

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/dop251/goja"
)

const QQTokenURL = "https://bots.qq.com/app/getAppAccessToken"
const QQAPIURL = "https://api.sgroup.qq.com"
const QQUserAgent = "qqbot-nodejs/0.1.0 (Node/24.3.0)"
const QQTextLimit = 1500
const QQFlushInterval = 600 * time.Millisecond
const QQFlushThreshold = 200

type QQClientOptions struct {
	Credentials protocol.JSONObject // original appId / appSecret strings, including UTF16
	HTTP        *http.Client
	Environment map[string]string
	Now         func() time.Time
	Random      func() float64
	Schedule    func(time.Duration, func()) func()
	Log         func(string)
}
type QQTarget struct {
	Scope               string
	TargetID, MessageID Text
}
type QQAPIError struct {
	Message             Text
	HTTPStatus          int
	Path                string
	BizCode, BizMessage json.RawMessage
}

func (e *QQAPIError) Error() string { return e.Message.String() }

type qqToken struct {
	value   json.RawMessage
	expires float64
}
type qqPendingToken struct {
	done  chan struct{}
	value json.RawMessage
	err   error
}
type qqUploadCache struct {
	info    json.RawMessage
	expires int64
}
type QQClient struct {
	o             QQClientOptions
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	token         *qqToken
	pending       *qqPendingToken
	refreshCancel context.CancelFunc
	closed        bool
	wg            sync.WaitGroup
	uploads       map[string]qqUploadCache
	uploadOrder   []string
}

func qqObject(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func qqField(name string, v any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(v)}
}
func qqString(value json.RawMessage) Text {
	r, _ := protocol.ExactJavaScriptStringJSON(value)
	return TextJSON(r)
}
func qqNumber(value json.RawMessage) float64 {
	quoted, _ := json.Marshal(string(value))
	v, e := goja.New().RunString("Number(JSON.parse(" + string(quoted) + "))")
	if e != nil {
		return math.NaN()
	}
	return v.ToFloat()
}
func qqParse(data []byte) (json.RawMessage, error) {
	data = protocol.DecodeJavaScriptUTF8(data)
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(data)); invalid {
		return nil, errors.New(TextJSON(message).String())
	}
	return protocol.ParseAndStringifyJavaScriptJSON(data)
}
func qqRawKey(text Text) string { return string(protocol.QuoteJavaScriptUTF16(text)) }

func NewQQClient(ctx context.Context, o QQClientOptions) (*QQClient, error) {
	o.Credentials = o.Credentials.Clone()
	if !truthy(o.Credentials.Get("appId")) {
		return nil, errors.New("QQBot: appId is required")
	}
	if !truthy(o.Credentials.Get("appSecret")) {
		return nil, errors.New("QQBot: appSecret is required")
	}
	for _, name := range []string{"appId", "appSecret"} {
		v := o.Credentials.Get(name)
		if len(v) == 0 || v[0] != '"' {
			return nil, fmt.Errorf("QQBot %s requires original string credential", name)
		}
	}
	if o.HTTP == nil {
		var e error
		o.HTTP, e = AdapterNetworkHTTP(o.Environment, true)
		if e != nil {
			return nil, e
		}
	}
	copy := *o.HTTP
	copy.Timeout = 0
	o.HTTP = &copy
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = func() float64 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			n := uint64(0)
			for _, v := range b {
				n = n<<8 | uint64(v)
			}
			return float64(n>>11) / (1 << 53)
		}
	}
	if o.Schedule == nil {
		o.Schedule = func(d time.Duration, f func()) func() { t := time.AfterFunc(d, f); return func() { t.Stop() } }
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &QQClient{o: o, ctx: ctx, cancel: cancel, uploads: map[string]qqUploadCache{}}, nil
}
func (c *QQClient) begin(ctx context.Context) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, context.Canceled
	}
	c.wg.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	return ctx, func() { stop(); cancel(); c.wg.Done() }, nil
}
func (c *QQClient) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	if c.refreshCancel != nil {
		c.refreshCancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.o.HTTP.CloseIdleConnections()
}
func (c *QQClient) ClearToken() { c.mu.Lock(); c.token = nil; c.mu.Unlock() }
func (c *QQClient) GetAccessToken(ctx context.Context) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	now := float64(c.o.Now().UnixMilli())
	if c.token != nil {
		ahead := math.Min(300000, (c.token.expires-now)/3)
		if now < c.token.expires-ahead {
			v := append(json.RawMessage(nil), c.token.value...)
			c.mu.Unlock()
			return v, nil
		}
	}
	pending := c.pending
	if pending == nil {
		pending = &qqPendingToken{done: make(chan struct{})}
		c.pending = pending
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			v, e := c.fetchToken()
			c.mu.Lock()
			pending.value, pending.err = v, e
			c.pending = nil
			close(pending.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-pending.done:
		return append(json.RawMessage(nil), pending.value...), pending.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-c.ctx.Done():
		return nil, context.Cause(c.ctx)
	}
}
func (c *QQClient) doHeaders(ctx context.Context, method, target string, headers http.Header, body []byte, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := c.o.Schedule(timeout, cancel)
	req, e := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if e != nil {
		stop()
		cancel()
		return nil, e
	}
	req.Header = headers
	res, e := c.o.HTTP.Do(req)
	stop()
	if e != nil {
		cancel()
		return nil, e
	}
	res.Body = &qqCancelBody{ReadCloser: res.Body, cancel: cancel}
	return res, nil
}

type qqCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *qqCancelBody) Close() error { e := b.ReadCloser.Close(); b.cancel(); return e }
func (c *QQClient) fetchToken() (json.RawMessage, error) {
	body := qqObject(protocol.JSONField{Name: "appId", Value: raw(TextJSON(c.o.Credentials.Get("appId")).Trim())}, protocol.JSONField{Name: "clientSecret", Value: c.o.Credentials.Get("appSecret")})
	res, e := c.doHeaders(c.ctx, "POST", QQTokenURL, http.Header{"Content-Type": {"application/json"}, "User-Agent": {QQUserAgent}}, raw(body), 10*time.Second)
	if e != nil {
		return nil, fmt.Errorf("Network error getting access_token: %s", e)
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := NewText(fmt.Sprintf("Token fetch failed: HTTP %d", res.StatusCode))
		if len(data) > 0 {
			msg = msg.Join(NewText(" — ")).Join(NewText(string(protocol.DecodeJavaScriptUTF8(data))).Slice(0, 200))
		}
		return nil, errors.New(msg.String())
	}
	if e != nil {
		return nil, fmt.Errorf("Failed to parse access_token response: %s", e)
	}
	value, e := qqParse(data)
	if e != nil {
		return nil, fmt.Errorf("Failed to parse access_token response: %s", e)
	}
	obj := object(value)
	if !truthy(obj.Get("access_token")) {
		return nil, fmt.Errorf("Failed to get access_token: %s", value)
	}
	expires := obj.Get("expires_in")
	if len(expires) == 0 || string(expires) == "null" {
		expires = raw(7200)
	}
	at := float64(c.o.Now().UnixMilli()) + qqNumber(expires)*1000
	c.mu.Lock()
	c.token = &qqToken{append(json.RawMessage(nil), obj.Get("access_token")...), at}
	c.mu.Unlock()
	if math.IsNaN(at) || math.IsInf(at, 0) || math.Abs(at) > 8.64e15 {
		return nil, errors.New("Invalid Date")
	}
	return append(json.RawMessage(nil), obj.Get("access_token")...), nil
}
func (c *QQClient) StartBackgroundRefresh() {
	c.mu.Lock()
	if c.closed || c.refreshCancel != nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.refreshCancel = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer func() { c.mu.Lock(); c.refreshCancel = nil; c.mu.Unlock() }()
		for ctx.Err() == nil {
			_, e := c.GetAccessToken(ctx)
			delay := time.Minute
			if e != nil {
				if ctx.Err() != nil {
					return
				}
				c.o.Log("[qqbot:token] Background refresh failed: " + e.Error())
				delay = 5 * time.Second
			} else {
				c.mu.Lock()
				cached := c.token
				// Original refresh loop looks up the untrimmed appId, while
				// getAccessToken stores by its trimmed id. Preserve that lookup.
				id := TextJSON(c.o.Credentials.Get("appId"))
				if qqRawKey(id) != qqRawKey(id.Trim()) {
					cached = nil
				}
				if cached != nil {
					delay = time.Duration(math.Max(cached.expires-float64(c.o.Now().UnixMilli())-300000-c.o.Random()*30000, 60000)) * time.Millisecond
				}
				c.mu.Unlock()
			}
			if c.sleep(ctx, delay) != nil {
				return
			}
		}
	}()
}
func (c *QQClient) sleep(ctx context.Context, d time.Duration) error {
	ready := make(chan struct{})
	stop := c.o.Schedule(d, func() { close(ready) })
	defer stop()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	}
}
func (c *QQClient) Request(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	ctx, finish, e := c.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer finish()
	token, e := c.GetAccessToken(ctx)
	if e != nil {
		return nil, e
	}
	return c.requestToken(ctx, token, method, path, body)
}
func (c *QQClient) requestToken(ctx context.Context, token json.RawMessage, method, path string, body json.RawMessage) (json.RawMessage, error) {
	timeout := 30 * time.Second
	if strings.Contains(path, "/files") || strings.Contains(path, "/upload_prepare") || strings.Contains(path, "/upload_part_finish") {
		timeout = 120 * time.Second
	}
	if !truthy(body) {
		body = nil
	}
	h := http.Header{"Authorization": {"QQBot " + qqString(token).String()}, "Content-Type": {"application/json"}, "User-Agent": {QQUserAgent}}
	res, e := c.doHeaders(ctx, method, QQAPIURL+path, h, body, timeout)
	if e != nil {
		return nil, &QQAPIError{NewText("Network error [" + path + "]: " + e.Error()), 0, path, nil, nil}
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		return nil, &QQAPIError{NewText("Failed to read response [" + path + "]: " + e.Error()), res.StatusCode, path, nil, nil}
	}
	text := NewText(string(protocol.DecodeJavaScriptUTF8(data)))
	html := strings.Contains(res.Header.Get("Content-Type"), "text/html") || text.TrimStart().HasPrefix(NewText("<"))
	parsed, parseErr := qqParse(data)
	apiError := func(msg Text, code, message json.RawMessage) error {
		return &QQAPIError{msg, res.StatusCode, path, code, message}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if html {
			hint := fmt.Sprintf("开放平台返回 HTTP %d", res.StatusCode)
			if res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
				hint = "调用发生异常，请稍候重试"
			} else if res.StatusCode == 429 {
				hint = "请求过于频繁，已被限流"
			}
			return nil, apiError(NewText(hint+"（"+path+"），请稍后重试"), nil, nil)
		}
		if parseErr == nil && string(parsed) != "null" {
			o := object(parsed)
			msg := o.Get("message")
			code := o.Get("code")
			if len(code) == 0 || string(code) == "null" {
				code = o.Get("err_code")
			}
			display := text
			if len(msg) > 0 && string(msg) != "null" {
				display = qqString(msg)
			}
			return nil, apiError(NewText("API Error ["+path+"]: ").Join(display), code, msg)
		}
		return nil, apiError(NewText(fmt.Sprintf("API Error [%s] HTTP %d: ", path, res.StatusCode)).Join(text.Slice(0, 200)), nil, nil)
	}
	if html {
		return nil, apiError(NewText("QQ 服务端返回了非 JSON 响应（"+path+"），可能是临时故障，请稍后重试"), nil, nil)
	}
	if parseErr != nil {
		return nil, apiError(NewText("开放平台响应格式异常（"+path+"），请稍后重试"), nil, nil)
	}
	return parsed, nil
}
func (c *QQClient) nextSequence() int32 {
	a := int32(c.o.Now().UnixMilli() % 100000000)
	b := int32(math.Floor(c.o.Random() * 65536))
	return (a ^ b) % 65536
}
func qqMessagePath(t QQTarget) string {
	if t.Scope == "c2c" {
		return "/v2/users/" + t.TargetID.String() + "/messages"
	}
	return "/v2/groups/" + t.TargetID.String() + "/messages"
}
func (c *QQClient) SendText(ctx context.Context, t QQTarget, text Text) (json.RawMessage, error) {
	b := qqObject(qqField("content", text), qqField("msg_type", 0))
	if len(t.MessageID) > 0 {
		b.Set("msg_seq", raw(c.nextSequence()))
		b.Set("msg_id", raw(t.MessageID))
	} else if len(text.Trim()) == 0 {
		return nil, errors.New("Proactive message content must not be empty")
	}
	return c.Request(ctx, "POST", qqMessagePath(t), raw(b))
}
func (c *QQClient) SendNotice(ctx context.Context, t QQTarget, text Text) error {
	for _, chunk := range SplitMessage(text, QQTextLimit) {
		if _, e := c.SendText(ctx, t, chunk); e != nil {
			return e
		}
	}
	return nil
}
func (c *QQClient) SendTyping(ctx context.Context, t QQTarget) (json.RawMessage, error) {
	if t.Scope != "c2c" {
		return nil, errors.New("sendTyping is only supported for C2C targets")
	}
	seq := int32(1)
	if len(t.MessageID) > 0 {
		seq = c.nextSequence()
	}
	b := qqObject(qqField("msg_type", 6), qqField("input_notify", qqObject(qqField("input_type", 1), qqField("input_second", 30))), qqField("msg_seq", seq))
	if len(t.MessageID) > 0 {
		b.Set("msg_id", raw(t.MessageID))
	}
	return c.Request(ctx, "POST", qqMessagePath(t), raw(b))
}
func (c *QQClient) SendStream(ctx context.Context, t QQTarget, body protocol.JSONObject) (json.RawMessage, error) {
	if t.Scope != "c2c" {
		return nil, errors.New("Streaming is only supported for C2C targets")
	}
	return c.Request(ctx, "POST", "/v2/users/"+t.TargetID.String()+"/stream_messages", raw(body))
}
func (c *QQClient) mediaCacheGet(hash string, t QQTarget) json.RawMessage {
	key := hash + ":" + t.Scope + ":" + qqRawKey(t.TargetID) + ":1"
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.uploads[key]
	if !ok {
		return nil
	}
	if c.o.Now().UnixMilli() >= v.expires {
		delete(c.uploads, key)
		return nil
	}
	return append(json.RawMessage(nil), v.info...)
}
func (c *QQClient) mediaCacheSet(hash string, t QQTarget, v protocol.JSONObject) {
	ttl := qqNumber(v.Get("ttl"))
	if !truthy(v.Get("file_info")) || ttl <= 0 || math.IsNaN(ttl) {
		return
	}
	key := hash + ":" + t.Scope + ":" + qqRawKey(t.TargetID) + ":1"
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.uploads) >= 500 {
		now := c.o.Now().UnixMilli()
		for k, v := range c.uploads {
			if now >= v.expires {
				delete(c.uploads, k)
			}
		}
		if len(c.uploads) >= 500 {
			count := 0
			for _, k := range c.uploadOrder {
				if _, ok := c.uploads[k]; ok {
					delete(c.uploads, k)
					count++
					if count >= 250 {
						break
					}
				}
			}
		}
	}
	if _, ok := c.uploads[key]; !ok {
		c.uploadOrder = append(c.uploadOrder, key)
	}
	c.uploads[key] = qqUploadCache{append(json.RawMessage(nil), v.Get("file_info")...), c.o.Now().UnixMilli() + int64(math.Max(ttl-60, 10)*1000)}
}
func (c *QQClient) SendImage(ctx context.Context, t QQTarget, image OutboundImage) error {
	limit := CheckAttachmentLimit("image", int64(len(image.Buffer)), image.MIME)
	if !limit.OK {
		c.o.Log("[QQ] Outbound image rejected: " + limit.Hint)
		return nil
	}
	var upload protocol.JSONObject
	var e error
	if len(image.Buffer) >= 5*1024*1024 {
		upload, e = c.uploadChunked(ctx, t, image.Buffer)
	} else {
		encoded := base64.StdEncoding.EncodeToString(image.Buffer)
		sum := md5.Sum([]byte(encoded))
		hash := fmt.Sprintf("%x", sum)
		info := c.mediaCacheGet(hash, t)
		if len(info) > 0 {
			upload = qqObject(protocol.JSONField{Name: "file_info", Value: info})
		} else {
			body := qqObject(qqField("file_type", 1), qqField("srv_send_msg", false))
			if encoded != "" {
				body.Set("file_data", raw(encoded))
			}
			path := strings.TrimSuffix(qqMessagePath(t), "/messages") + "/files"
			for attempt := 0; attempt <= 2; attempt++ {
				var v json.RawMessage
				v, e = c.Request(ctx, "POST", path, raw(body))
				if e == nil {
					upload = object(v)
					break
				}
				msg := e.Error()
				if attempt == 2 || strings.Contains(msg, "400") || strings.Contains(msg, "401") || strings.Contains(msg, "Invalid") || strings.Contains(msg, "timeout") || strings.Contains(msg, "Timeout") {
					break
				}
				if e = c.sleep(ctx, time.Duration(1<<attempt)*time.Second); e != nil {
					break
				}
			}
			if e == nil {
				c.mediaCacheSet(hash, t, upload)
			}
		}
	}
	if e != nil {
		return e
	}
	seq := int32(1)
	if len(t.MessageID) > 0 {
		seq = c.nextSequence()
	}
	b := qqObject(qqField("msg_type", 7), qqField("media", qqObject(protocol.JSONField{Name: "file_info", Value: upload.Get("file_info")})), qqField("msg_seq", seq))
	if image.Alt != "" {
		b.Set("content", raw(image.Alt))
	}
	if len(t.MessageID) > 0 {
		b.Set("msg_id", raw(t.MessageID))
	}
	_, e = c.Request(ctx, "POST", qqMessagePath(t), raw(b))
	return e
}

// Scalar integer readers operate after Source JSON parsing, not a schema rewrite.
func qqInt(rawValue json.RawMessage, fallback int) int {
	if len(rawValue) == 0 {
		return fallback
	}
	v, e := strconv.ParseFloat(string(rawValue), 64)
	if e != nil {
		return fallback
	}
	return int(v)
}
