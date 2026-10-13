package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/klauspost/compress/zstd"
)

const DingTalkAPIURL = "https://api.dingtalk.com"
const DingTalkGatewayURL = DingTalkAPIURL + "/v1.0/gateway/connections/open"
const DingTalkRobotTopic = "/v1.0/im/bot/messages/get"
const DingTalkCardTopic = "/v1.0/card/instances/callback"

type DingTalkAPIOptions struct {
	Credentials protocol.JSONObject
	Environment map[string]string
	HTTP        *http.Client
	Now         func() time.Time
	Random      func() float64
	Log         func(string)
}
type dingTalkToken struct {
	token     json.RawMessage
	expiresAt float64
}
type DingTalkAPI struct {
	o      DingTalkAPIOptions
	HTTP   *http.Client
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	cache  *dingTalkToken
	wg     sync.WaitGroup
	close  sync.Once
	cards  *DingTalkCardLimiter
}

func NewDingTalkAPI(ctx context.Context, o DingTalkAPIOptions) (*DingTalkAPI, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = dingTalkRandom
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	client := o.HTTP
	if client == nil {
		var e error
		client, e = AdapterNetworkHTTP(o.Environment, true)
		if e != nil {
			return nil, e
		}
	}
	o.Credentials = o.Credentials.Clone()
	env := map[string]string{}
	for key, value := range o.Environment {
		env[key] = value
	}
	o.Environment = env
	ctx, cancel := context.WithCancel(ctx)
	api := &DingTalkAPI{o: o, HTTP: client, ctx: ctx, cancel: cancel}
	api.cards = NewDingTalkCardLimiter(o.Now)
	return api, nil
}
func (a *DingTalkAPI) Close() {
	a.close.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		a.mu.Unlock()
		a.wg.Wait()
		a.HTTP.CloseIdleConnections()
	})
}
func (a *DingTalkAPI) begin(ctx context.Context) (context.Context, func(), error) {
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

// Composite operations join Close; each physical request owns cancellation.
// Returning from a composite must not abort its unread fetch success body.
func (a *DingTalkAPI) track(ctx context.Context) (context.Context, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, nil, context.Canceled
	}
	a.wg.Add(1)
	return ctx, a.wg.Done, nil
}
func (a *DingTalkAPI) request(ctx context.Context, method, address string, body json.RawMessage, token json.RawMessage, axios bool) (*http.Response, func(), error) {
	ctx, done, e := a.begin(ctx)
	if e != nil {
		return nil, nil, e
	}
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		done()
		return nil, nil, e
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "Bun/1.3.14")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != nil {
		req.Header.Set("x-acs-dingtalk-access-token", dingTalkCoerce(token).String())
	}
	if axios {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "axios/1.13.6")
		req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	}
	resp, e := a.HTTP.Do(req)
	if e != nil {
		done()
		return nil, nil, e
	}
	return resp, func() { _ = resp.Body.Close(); done() }, nil
}
func dingTalkBody(resp *http.Response) ([]byte, error) {
	data, e := io.ReadAll(resp.Body)
	if e != nil {
		return nil, e
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "zstd") {
		decoder, e := zstd.NewReader(nil)
		if e != nil {
			return nil, e
		}
		defer decoder.Close()
		return decoder.DecodeAll(data, nil)
	}
	return wecomDecompress(data, resp.Header.Get("Content-Encoding"))
}
func (a *DingTalkAPI) drain(resp *http.Response, done func()) {
	go func() { defer done(); _, _ = io.Copy(io.Discard, resp.Body) }()
}
func (a *DingTalkAPI) AccessToken(ctx context.Context) (json.RawMessage, error) {
	now := float64(a.o.Now().UnixMilli())
	a.mu.Lock()
	cache := a.cache
	if cache != nil && cache.expiresAt > now+60000 {
		token := append(json.RawMessage(nil), cache.token...)
		a.mu.Unlock()
		return token, nil
	}
	a.mu.Unlock()
	body := protocol.NewJSONObject(protocol.JSONField{Name: "appKey", Value: a.o.Credentials.Get("clientId")}, protocol.JSONField{Name: "appSecret", Value: a.o.Credentials.Get("clientSecret")})
	resp, done, e := a.request(ctx, "POST", DingTalkAPIURL+"/v1.0/oauth2/accessToken", dingTalkWire(body), nil, false)
	if e != nil {
		return nil, e
	}
	defer done()
	data, _ := dingTalkBody(resp)
	value := object(data)
	token := value.Get("accessToken")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !credentialTruthy(token) {
		message := value.Get("message")
		if credentialTruthy(message) {
			return nil, errors.New(dingTalkCoerce(message).String())
		}
		return nil, fmt.Errorf("accessToken request failed: %d", resp.StatusCode)
	}
	expire := value.Get("expireIn")
	if len(expire) == 0 || string(expire) == "null" {
		expire = raw(7200)
	}
	// Original getAccessToken has no singleflight. Concurrent misses may each
	// fetch, with last completion winning and expiry anchored before its fetch.
	a.mu.Lock()
	a.cache = &dingTalkToken{append(json.RawMessage(nil), token...), now + dingTalkNumber(expire)*1000}
	a.mu.Unlock()
	return token, nil
}
func (a *DingTalkAPI) SendText(ctx context.Context, webhook string, text Text) error {
	ctx, complete, e := a.track(ctx)
	if e != nil {
		return e
	}
	defer complete()
	if webhook == "" {
		a.o.Log("[DingTalk] Missing sessionWebhook; cannot send response")
		return nil
	}
	token, e := a.AccessToken(ctx)
	if e != nil {
		return e
	}
	for _, chunk := range SplitMessage(text, 3500) {
		body := protocol.NewJSONObject(dingTalkField("msgtype", "markdown"), dingTalkField("markdown", protocol.NewJSONObject(dingTalkField("title", "Claude Code"), dingTalkField("text", chunk))))
		resp, done, e := a.request(ctx, "POST", webhook, dingTalkWire(body), token, false)
		if e != nil {
			return e
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := dingTalkBody(resp)
			a.o.Log(fmt.Sprintf("[DingTalk] sendText failed: %d %s", resp.StatusCode, string(protocol.DecodeJavaScriptUTF8(data))))
			done()
		} else {
			a.drain(resp, done)
		}
	}
	return nil
}
func GetDingTalkAttachmentName(candidate protocol.JSONObject, actual LocalAttachment) Text {
	if name := dingTalkString(candidate.Get("fileName")); len(name) > 0 {
		return name.Clone()
	}
	return NewText(actual.Name)
}
func NewDingTalkAttachmentStore(home string) *AttachmentStore {
	store := NewAttachmentStore(home)
	store.Random = func() string { return dingTalkRandomSuffix(dingTalkRandom(), 6) }
	return store
}

func NewDingTalkConfiguredAttachmentStore(config Config) *AttachmentStore {
	store := NewConfiguredAttachmentStore(config)
	store.Random = func() string { return dingTalkRandomSuffix(dingTalkRandom(), 6) }
	return store
}
func (a *DingTalkAPI) Gateway(ctx context.Context) (string, protocol.JSONObject, error) {
	body := protocol.NewJSONObject(protocol.JSONField{Name: "clientId", Value: a.o.Credentials.Get("clientId")}, protocol.JSONField{Name: "clientSecret", Value: a.o.Credentials.Get("clientSecret")}, dingTalkField("ua", ""), dingTalkField("subscriptions", []any{protocol.NewJSONObject(dingTalkField("type", "EVENT"), dingTalkField("topic", "*")), protocol.NewJSONObject(dingTalkField("type", "CALLBACK"), dingTalkField("topic", DingTalkRobotTopic)), protocol.NewJSONObject(dingTalkField("type", "CALLBACK"), dingTalkField("topic", DingTalkCardTopic))}))
	resp, done, e := a.request(ctx, "POST", DingTalkGatewayURL, dingTalkWire(body), nil, true)
	if e != nil {
		return "", protocol.JSONObject{}, e
	}
	defer done()
	data, e := dingTalkBody(resp)
	if e != nil {
		return "", protocol.JSONObject{}, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", protocol.JSONObject{}, fmt.Errorf("Request failed with status code %d", resp.StatusCode)
	}
	if !credentialTruthy(data) {
		return "", protocol.JSONObject{}, errors.New("build: get endpoint failed")
	}
	value := object(data)
	endpoint, ticket := value.Get("endpoint"), value.Get("ticket")
	if !credentialTruthy(endpoint) || !credentialTruthy(ticket) {
		return "", value, errors.New("endpoint or ticket is null")
	}
	return dingTalkCoerce(endpoint).Join(NewText("?ticket=")).Join(dingTalkCoerce(ticket)).String(), value, nil
}
func (a *DingTalkAPI) DownloadCandidate(ctx context.Context, store *AttachmentStore, candidate protocol.JSONObject, session string, token json.RawMessage) (LocalAttachment, error) {
	address := candidate.Get("url")
	if !credentialTruthy(address) {
		code := candidate.Get("downloadCode")
		if credentialTruthy(code) {
			body := protocol.NewJSONObject(protocol.JSONField{Name: "downloadCode", Value: code}, protocol.JSONField{Name: "robotCode", Value: a.o.Credentials.Get("clientId")})
			resp, done, e := a.request(ctx, "POST", DingTalkAPIURL+"/v1.0/robot/messageFiles/download", dingTalkWire(body), token, false)
			if e != nil {
				return LocalAttachment{}, e
			}
			data, _ := dingTalkBody(resp)
			value := object(data)
			address = value.Get("downloadUrl")
			done()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 || !credentialTruthy(address) {
				if message := value.Get("message"); credentialTruthy(message) {
					return LocalAttachment{}, errors.New(dingTalkCoerce(message).String())
				}
				return LocalAttachment{}, fmt.Errorf("DingTalk downloadCode exchange failed: %d", resp.StatusCode)
			}
		}
	}
	if !credentialTruthy(address) {
		return LocalAttachment{}, errors.New("DingTalk media item is missing a download URL")
	}
	resp, done, e := a.request(ctx, "GET", dingTalkCoerce(address).String(), nil, nil, false)
	if e != nil {
		return LocalAttachment{}, e
	}
	defer done()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LocalAttachment{}, fmt.Errorf("DingTalk media download failed: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	data, e := dingTalkBody(resp)
	if e != nil {
		return LocalAttachment{}, e
	}
	mime := resp.Header.Get("Content-Type")
	kind := candidate.String("kind")
	name := candidate.String("fileName")
	if mime == "" {
		mime = dingTalkInferMIME(name, kind)
	}
	if name == "" {
		suffix := ".jpg"
		for _, pair := range [][2]string{{"png", ".png"}, {"gif", ".gif"}, {"webp", ".webp"}} {
			if strings.Contains(mime, pair[0]) {
				suffix = pair[1]
				break
			}
		}
		name = fmt.Sprintf("dingtalk-image-%d%s", a.o.Now().UnixMilli(), suffix)
	}
	target, e := store.ResolvePath("dingtalk", session, name)
	if e != nil {
		return LocalAttachment{}, e
	}
	if e = store.Write(target, data); e != nil {
		return LocalAttachment{}, e
	}
	return LocalAttachment{kind, name, target, mime, int64(len(data)), data}, nil
}
func dingTalkInferMIME(name, kind string) string {
	if kind == "image" {
		return "image/jpeg"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}

func dingTalkNumber(value json.RawMessage) float64 {
	v := bytes.TrimSpace(value)
	if len(v) == 0 {
		return math.NaN()
	}
	if string(v) == "null" || string(v) == "false" {
		return 0
	}
	if string(v) == "true" {
		return 1
	}
	text := string(v)
	if v[0] == '"' || v[0] == '[' || v[0] == '{' {
		text = dingTalkCoerce(v).Trim().String()
	}
	if text == "" {
		return 0
	}
	if text == "Infinity" || text == "+Infinity" {
		return math.Inf(1)
	}
	if text == "-Infinity" {
		return math.Inf(-1)
	}
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") || strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B") || strings.HasPrefix(text, "0o") || strings.HasPrefix(text, "0O") {
		n, ok := new(big.Int).SetString(text[2:], map[byte]int{'x': 16, 'X': 16, 'b': 2, 'B': 2, 'o': 8, 'O': 8}[text[1]])
		if ok && n.Sign() >= 0 {
			v, _ := new(big.Float).SetInt(n).Float64()
			return v
		}
		return math.NaN()
	}
	if strings.Contains(strings.ToLower(text), "inf") {
		return math.NaN()
	}
	n, e := strconv.ParseFloat(text, 64)
	if e == nil || errors.Is(e, strconv.ErrRange) {
		return n
	}
	return math.NaN()
}

type DingTalkHTTPError struct {
	Method, Path string
	Status       int
	Body         Text
}

func (e *DingTalkHTTPError) Error() string {
	return fmt.Sprintf("%s %s failed: %d %s", e.Method, e.Path, e.Status, e.Body.String())
}
func (a *DingTalkAPI) cardRequest(ctx context.Context, method, path string, token json.RawMessage, body protocol.JSONObject) error {
	timeout := 15000.0
	if v := a.o.Environment["CC_HAHA_IM_CARD_REQUEST_TIMEOUT_MS"]; v != "" {
		n := dingTalkNumber(raw(v))
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 {
			timeout = n
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	streamOwnsCancel := false
	defer func() {
		if !streamOwnsCancel {
			cancel()
		}
	}()
	var expired atomic.Bool
	timer := time.AfterFunc(time.Duration(timeout*float64(time.Millisecond)), func() { expired.Store(true); cancel() })
	defer timer.Stop()
	resp, done, e := a.request(ctx, method, DingTalkAPIURL+path, dingTalkWire(body), token, false)
	if e == nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// fetch resolves at headers; clearing its timer does not abort the
			// unread success body. Keep cancellation with the owned drain.
			streamOwnsCancel = true
			a.drain(resp, func() { done(); cancel() })
			return nil
		}
		data, _ := dingTalkBody(resp)
		done()
		e = &DingTalkHTTPError{method, path, resp.StatusCode, NewText(string(protocol.DecodeJavaScriptUTF8(data)))}
	}
	if expired.Load() {
		return fmt.Errorf("%s %s timed out after %sms", method, path, strconv.FormatFloat(timeout, 'f', -1, 64))
	}
	return e
}
