package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const FeishuAPIOrigin = "https://open.feishu.cn"
const LarkAPIOrigin = "https://open.larksuite.com"
const FeishuSDKUserAgent = "oapi-node-sdk/1.0.0"

type FeishuClientOptions struct {
	Credentials protocol.JSONObject
	Domain      string
	HTTP        *http.Client
	Environment map[string]string
	Cache       *FeishuTokenCache
	Now         func() time.Time
	Log         func(string)
}
type feishuCachedToken struct {
	value   json.RawMessage
	expires float64
}
type FeishuTokenCache struct {
	mu      sync.Mutex
	entries map[string]feishuCachedToken
}

func NewFeishuTokenCache() *FeishuTokenCache {
	return &FeishuTokenCache{entries: map[string]feishuCachedToken{}}
}

var feishuSDKCache = NewFeishuTokenCache()

type FeishuClient struct {
	o      FeishuClientOptions
	Origin string
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
	once   sync.Once
}
type FeishuHTTPError struct {
	Status int
	Body   json.RawMessage
}

func (e *FeishuHTTPError) Error() string {
	return fmt.Sprintf("Request failed with status code %d", e.Status)
}
func NewFeishuClient(ctx context.Context, o FeishuClientOptions) (*FeishuClient, error) {
	o.Credentials = o.Credentials.Clone()
	origin := FeishuAPIOrigin
	if o.Domain == "lark" {
		origin = LarkAPIOrigin
	}
	if o.HTTP == nil {
		var e error
		o.HTTP, e = AdapterNetworkHTTP(o.Environment, true)
		if e != nil {
			return nil, e
		}
	}
	client := *o.HTTP
	client.Timeout = 0
	o.HTTP = &client
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Cache == nil {
		o.Cache = feishuSDKCache
	}
	ctx, cancel := context.WithCancel(ctx)
	return &FeishuClient{o: o, Origin: origin, ctx: ctx, cancel: cancel}, nil
}
func (c *FeishuClient) begin(ctx context.Context) (context.Context, func(), error) {
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
func (c *FeishuClient) Close() {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		c.wg.Wait()
		c.o.HTTP.CloseIdleConnections()
	})
}
func feishuResponse(data []byte) json.RawMessage {
	data = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{239, 187, 191})
	if json.Valid(data) {
		return append(json.RawMessage(nil), bytes.TrimSpace(data)...)
	}
	return raw(NewText(string(data)))
}
func (c *FeishuClient) http(ctx context.Context, method, path string, params protocol.JSONObject, body []byte, contentType string, authorize bool) ([]byte, error) {
	var token json.RawMessage
	var e error
	if authorize {
		token, e = c.GetTenantAccessToken(ctx)
		if e != nil {
			return nil, e
		}
	}
	address := c.Origin + "/" + strings.TrimPrefix(path, "/")
	u, e := url.Parse(address)
	if e != nil {
		return nil, e
	}
	q := u.Query()
	for _, field := range params.OwnProperties() {
		v := field.Value
		if len(v) == 0 {
			continue
		}
		var vals []json.RawMessage
		if len(v) > 0 && v[0] == '[' && json.Unmarshal(v, &vals) == nil {
			for _, x := range vals {
				if string(x) != "null" {
					q.Add(jsString(field.NameJSON), jsString(x))
				} else {
					q.Add(jsString(field.NameJSON), "")
				}
			}
		} else if string(v) == "null" {
			q.Add(jsString(field.NameJSON), "")
		} else {
			q.Add(jsString(field.NameJSON), jsString(v))
		}
	}
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, strings.ToUpper(method), u.String(), bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", FeishuSDKUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if credentialTruthy(token) {
		req.Header.Set("Authorization", "Bearer "+jsString(token))
	}
	res, e := c.o.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		return nil, e
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &FeishuHTTPError{Status: res.StatusCode, Body: feishuResponse(data)}
	}
	return data, nil
}
func (c *FeishuClient) GetTenantAccessToken(ctx context.Context) (json.RawMessage, error) {
	ctx, done, e := c.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	return c.getTenantAccessToken(ctx)
}
func (c *FeishuClient) getTenantAccessToken(ctx context.Context) (json.RawMessage, error) {
	cache := c.o.Cache
	key := jsString(c.o.Credentials.Get("appId")) + "/Symbol(tenant-access-token)"
	now := float64(c.o.Now().UnixMilli())
	cache.mu.Lock()
	v, ok := cache.entries[key]
	cache.mu.Unlock()
	if ok && (v.expires == 0 || math.IsNaN(v.expires) || v.expires-now > 0) && credentialTruthy(v.value) {
		return append(json.RawMessage(nil), v.value...), nil
	}
	// The original SDK does not coalesce concurrent cache misses.
	body := protocol.NewJSONObject(protocol.JSONField{Name: "app_id", Value: c.o.Credentials.Get("appId")}, protocol.JSONField{Name: "app_secret", Value: c.o.Credentials.Get("appSecret")})
	p, e := body.MarshalJSON()
	if e != nil {
		return nil, e
	}
	data, e := c.http(ctx, "POST", "/open-apis/auth/v3/tenant_access_token/internal", protocol.NewJSONObject(), protocol.StringifyValidatedJavaScriptJSON(p), "application/json", false)
	if e != nil {
		return nil, e
	}
	response := object(feishuResponse(data))
	token := response.Get("tenant_access_token")
	expire := math.NaN()
	if x := response.Get("expire"); len(x) > 0 {
		expire = qqNumber(x)
	}
	cache.mu.Lock()
	cache.entries[key] = feishuCachedToken{append(json.RawMessage(nil), token...), float64(c.o.Now().UnixMilli()) + expire*1000 - 180000}
	cache.mu.Unlock()
	return token, nil
}
func (c *FeishuClient) Request(ctx context.Context, method, path string, params protocol.JSONObject, data json.RawMessage) (json.RawMessage, error) {
	ctx, done, e := c.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	contentType := ""
	if data != nil {
		data = protocol.StringifyValidatedJavaScriptJSON(data)
		contentType = "application/json"
	}
	p, e := c.http(ctx, method, path, params, data, contentType, true)
	if e != nil {
		return nil, e
	}
	return feishuResponse(p), nil
}
func feishuObject(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func feishuField(k string, v any) protocol.JSONField {
	return protocol.JSONField{Name: k, Value: raw(v)}
}
func (c *FeishuClient) CreateMessage(ctx context.Context, chat Text, kind string, content Text, uuid json.RawMessage) (json.RawMessage, error) {
	b := feishuObject(feishuField("receive_id", chat), feishuField("msg_type", kind), feishuField("content", content), protocol.JSONField{Name: "uuid", Value: uuid})
	return c.Request(ctx, "POST", "/open-apis/im/v1/messages", feishuObject(feishuField("receive_id_type", "chat_id")), raw(b))
}
func (c *FeishuClient) ReplyMessage(ctx context.Context, id Text, kind string, content Text, uuid json.RawMessage) (json.RawMessage, error) {
	return c.Request(ctx, "POST", "/open-apis/im/v1/messages/"+id.String()+"/reply", protocol.NewJSONObject(), raw(feishuObject(feishuField("content", content), feishuField("msg_type", kind), protocol.JSONField{Name: "uuid", Value: uuid})))
}
func (c *FeishuClient) PatchMessage(ctx context.Context, id Text, card json.RawMessage) (json.RawMessage, error) {
	return c.Request(ctx, "PATCH", "/open-apis/im/v1/messages/"+id.String(), protocol.NewJSONObject(), raw(feishuObject(feishuField("content", TextJSON(raw(string(protocol.StringifyValidatedJavaScriptJSON(card))))))))
}
func (c *FeishuClient) SendText(ctx context.Context, chat Text, text Text, reply Text) (json.RawMessage, error) {
	card := feishuObject(feishuField("zh_cn", feishuObject(feishuField("content", [][]any{{feishuObject(feishuField("tag", "md"), feishuField("text", text))}}))))
	content := NewText(string(protocol.StringifyValidatedJavaScriptJSON(raw(card))))
	var response json.RawMessage
	var e error
	if len(reply) > 0 {
		response, e = c.ReplyMessage(ctx, reply, "post", content, nil)
	} else {
		response, e = c.CreateMessage(ctx, chat, "post", content, nil)
	}
	if e != nil {
		c.o.Log("[Feishu] Send text error: " + e.Error())
		return nil, nil
	}
	return object(object(response).Get("data")).Get("message_id"), nil
}
func (c *FeishuClient) SendCard(ctx context.Context, chat Text, card json.RawMessage) (json.RawMessage, error) {
	response, e := c.CreateMessage(ctx, chat, "interactive", NewText(string(protocol.StringifyValidatedJavaScriptJSON(card))), nil)
	if e != nil {
		c.o.Log("[Feishu] Send card error: " + e.Error())
		return nil, nil
	}
	return object(object(response).Get("data")).Get("message_id"), nil
}
func (c *FeishuClient) BotInfo(ctx context.Context) (json.RawMessage, error) {
	return c.Request(ctx, "GET", "/open-apis/bot/v3/info", protocol.NewJSONObject(), nil)
}
