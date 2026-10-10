package imruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WechatAPIOptions struct {
	BaseURL, Token json.RawMessage
	HTTP           *http.Client
	Environment    map[string]string
	UUID           func() string
	Random         io.Reader
}
type WechatAPI struct {
	o      WechatAPIOptions
	HTTP   *http.Client
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
	once   sync.Once
}

func wechatHTTP(client *http.Client, env map[string]string) (*http.Client, error) {
	if client == nil {
		var e error
		client, e = AdapterNetworkHTTP(env, true)
		if e != nil {
			return nil, e
		}
	}
	copy := *client
	copy.Timeout = 0
	if copy.Transport == nil {
		copy.Transport = http.DefaultTransport.(*http.Transport).Clone()
	} else if t, ok := copy.Transport.(*http.Transport); ok {
		copy.Transport = t.Clone()
	}
	copy.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) > 20 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	return &copy, nil
}
func NewWechatAPI(o WechatAPIOptions) (*WechatAPI, error) {
	client, e := wechatHTTP(o.HTTP, o.Environment)
	if e != nil {
		return nil, e
	}
	if len(o.BaseURL) == 0 {
		o.BaseURL = raw(WechatBaseURL)
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WechatAPI{o: o, HTTP: client, ctx: ctx, cancel: cancel}, nil
}
func (a *WechatAPI) operation(ctx context.Context) (context.Context, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, nil, context.Canceled
	}
	a.wg.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, cancel)
	return ctx, func() { stop(); cancel(); a.wg.Done() }, nil
}
func (a *WechatAPI) Close() {
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		a.mu.Unlock()
		a.wg.Wait()
		a.HTTP.CloseIdleConnections()
	})
}
func (a *WechatAPI) post(ctx context.Context, path, label string, body protocol.JSONObject, timeout time.Duration) ([]byte, error) {
	ctx, finish, e := a.operation(ctx)
	if e != nil {
		return nil, e
	}
	defer finish()
	base, e := wechatStringOnly(a.o.BaseURL, "url.endsWith is not a function")
	if e != nil {
		return nil, e
	}
	text := base.String()
	if !strings.HasSuffix(text, "/") {
		text += "/"
	}
	target, e := whatwg.ParseRef(text, path)
	if e != nil {
		return nil, e
	}
	// Source creates AbortController even for a zero timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	data, e := body.MarshalJSON()
	if e != nil {
		return nil, e
	}
	data = protocol.StringifyValidatedJavaScriptJSON(data)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, target.Href(false), bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Length", strconv.Itoa(len(data)))
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", WechatAppClientVersion)
	if token := a.o.Token; len(token) > 0 && string(token) != "null" {
		t, e := wechatStringOnly(token, "opts.token?.trim is not a function")
		if e != nil {
			return nil, e
		}
		t = t.Trim()
		if len(t) > 0 {
			req.Header.Set("Authorization", "Bearer "+t.String())
		}
	}
	a.mu.Lock()
	var entropy [4]byte
	_, e = io.ReadFull(a.o.Random, entropy[:])
	a.mu.Unlock()
	if e != nil {
		return nil, e
	}
	uin := strconv.FormatUint(uint64(binary.BigEndian.Uint32(entropy[:])), 10)
	req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(uin)))
	res, e := a.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	data, e = io.ReadAll(res.Body)
	if e != nil {
		return nil, e
	}
	data = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf})
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %d: %s", label, res.StatusCode, data)
	}
	return data, nil
}
func wechatTimeout(defaultValue time.Duration, overrides []time.Duration) time.Duration {
	if len(overrides) > 0 {
		return overrides[0]
	}
	return defaultValue
}
func wechatBaseInfo() json.RawMessage {
	return raw(wechatFields(wechatField("channel_version", WechatChannelVersion)))
}
func wechatParsed(data []byte, e error) (json.RawMessage, error) {
	if e != nil {
		return nil, e
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return append(json.RawMessage(nil), data...), nil
}
func (a *WechatAPI) GetUpdates(ctx context.Context, cursor json.RawMessage, timeout ...time.Duration) (json.RawMessage, error) {
	c := cursor
	if len(c) == 0 || string(c) == "null" {
		c = raw("")
	}
	body := wechatFields(protocol.JSONField{Name: "get_updates_buf", Value: c}, protocol.JSONField{Name: "base_info", Value: wechatBaseInfo()})
	data, e := a.post(ctx, "ilink/bot/getupdates", "wechatGetUpdates", body, wechatTimeout(35*time.Second, timeout))
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return raw(wechatFields(wechatField("ret", 0), wechatField("msgs", []any{}), protocol.JSONField{Name: "get_updates_buf", Value: cursor})), nil
	}
	return wechatParsed(data, e)
}
func (a *WechatAPI) SendText(ctx context.Context, chat json.RawMessage, text Text, contextToken json.RawMessage, timeout ...time.Duration) error {
	a.mu.Lock()
	id := a.o.UUID()
	a.mu.Unlock()
	var items json.RawMessage
	if len(text) > 0 {
		items = raw([]any{wechatFields(wechatField("type", 1), protocol.JSONField{Name: "text_item", Value: raw(wechatFields(wechatField("text", text)))})})
	}
	msg := wechatFields(wechatField("from_user_id", ""), protocol.JSONField{Name: "to_user_id", Value: chat}, wechatField("client_id", "claude-code-haha-wechat-"+id), wechatField("message_type", 2), wechatField("message_state", 2), protocol.JSONField{Name: "item_list", Value: items}, protocol.JSONField{Name: "context_token", Value: contextToken})
	data, e := a.post(ctx, "ilink/bot/sendmessage", "wechatSendMessage", wechatFields(protocol.JSONField{Name: "msg", Value: raw(msg)}, protocol.JSONField{Name: "base_info", Value: wechatBaseInfo()}), wechatTimeout(15*time.Second, timeout))
	if e != nil {
		return e
	}
	return wechatAPIOk(data, "wechatSendMessage")
}
func (a *WechatAPI) GetConfig(ctx context.Context, chat, contextToken json.RawMessage, timeout ...time.Duration) (json.RawMessage, error) {
	body := wechatFields(protocol.JSONField{Name: "ilink_user_id", Value: chat}, protocol.JSONField{Name: "context_token", Value: contextToken}, protocol.JSONField{Name: "base_info", Value: wechatBaseInfo()})
	return wechatParsed(a.post(ctx, "ilink/bot/getconfig", "wechatGetConfig", body, wechatTimeout(10*time.Second, timeout)))
}
func (a *WechatAPI) SendTyping(ctx context.Context, chat, ticket json.RawMessage, status string, timeout ...time.Duration) error {
	n := 2
	if status == "typing" {
		n = 1
	}
	body := wechatFields(protocol.JSONField{Name: "ilink_user_id", Value: chat}, protocol.JSONField{Name: "typing_ticket", Value: ticket}, wechatField("status", n), protocol.JSONField{Name: "base_info", Value: wechatBaseInfo()})
	data, e := a.post(ctx, "ilink/bot/sendtyping", "wechatSendTyping", body, wechatTimeout(10*time.Second, timeout))
	if e != nil {
		return e
	}
	return wechatAPIOk(data, "wechatSendTyping")
}

type WechatContexts struct {
	mu     sync.Mutex
	tokens map[string]json.RawMessage
}

func NewWechatContexts() *WechatContexts {
	return &WechatContexts{tokens: map[string]json.RawMessage{}}
}
func (c *WechatContexts) Set(chat string, token json.RawMessage) {
	if !wechatTruthy(token) {
		return
	}
	c.mu.Lock()
	c.tokens[chat] = append(json.RawMessage(nil), token...)
	c.mu.Unlock()
}
func (c *WechatContexts) Get(chat string) json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.tokens[chat]...)
}

type WechatSender struct {
	API      *WechatAPI
	Contexts *WechatContexts
	Warn     func(string)
}

func (s *WechatSender) Send(ctx context.Context, chat string, text Text) error {
	var token json.RawMessage
	if s.Contexts != nil {
		token = s.Contexts.Get(chat)
	}
	for _, chunk := range SplitMessage(text, WechatTextLimit) {
		e := s.API.SendText(ctx, raw(chat), chunk, token)
		if e != nil {
			if !wechatTruthy(token) {
				return e
			}
			if s.Warn != nil {
				s.Warn("[WeChat] sendText with context token failed, retrying without context: " + e.Error())
			}
			if e = s.API.SendText(ctx, raw(chat), chunk, nil); e != nil {
				return e
			}
		}
	}
	return nil
}
func NewWechatMessageBuffer(sender *WechatSender, ctx context.Context, chat string, log func(error)) *MessageBuffer {
	return NewMessageBuffer(func(text Text, _ bool) error {
		if len(text.Trim()) == 0 {
			return nil
		}
		return sender.Send(ctx, chat, text)
	}, 3*time.Second, 200, log)
}
