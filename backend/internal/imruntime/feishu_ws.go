package imruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type FeishuSocket interface {
	Read() ([]byte, error)
	SendBinary([]byte) error
	Close(string) error
}
type feishuSocket struct{ *Socket }

func (s feishuSocket) SendBinary(p []byte) error { return s.frame(2, p) }
func DialFeishuSocket(ctx context.Context, address string, config *tls.Config) (FeishuSocket, error) {
	s, e := DialSocket(ctx, address, config)
	if e != nil {
		return nil, e
	}
	return feishuSocket{s}, nil
}

type FeishuWSOptions struct {
	Client        *FeishuClient
	Dispatcher    *FeishuDispatcher
	TLS           *tls.Config
	Dial          func(context.Context, string, *tls.Config) (FeishuSocket, error)
	Now           func() time.Time
	Random        func() float64
	Sleep         func(context.Context, time.Duration) error
	Log           func(string)
	AutoReconnect *bool
}
type FeishuWSConfig struct {
	URL                                             string
	Service                                         int32
	PingInterval, ReconnectInterval, ReconnectNonce float64
	ReconnectCount                                  float64
}
type feishuFragment struct {
	parts   map[int64][]byte
	length  int64
	created int64
}
type FeishuWS struct {
	o                        FeishuWSOptions
	ctx                      context.Context
	cancel                   context.CancelFunc
	mu                       sync.Mutex
	socket                   FeishuSocket
	config                   FeishuWSConfig
	cache                    map[string]*feishuFragment
	started, closed          bool
	wg                       sync.WaitGroup
	once                     sync.Once
	done                     chan struct{}
	lastConnect, nextConnect int64
}

func NewFeishuWS(ctx context.Context, o FeishuWSOptions) (*FeishuWS, error) {
	if o.Client == nil || o.Dispatcher == nil {
		return nil, errors.New("Feishu WS requires actual client/dispatcher")
	}
	if o.Dial == nil {
		o.Dial = DialFeishuSocket
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Float64
	}
	if o.Sleep == nil {
		o.Sleep = adapterSleep
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.TLS == nil {
		if tr, ok := o.Client.o.HTTP.Transport.(*http.Transport); ok {
			o.TLS = tr.TLSClientConfig
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &FeishuWS{o: o, ctx: ctx, cancel: cancel, config: FeishuWSConfig{PingInterval: 120000, ReconnectInterval: 120000, ReconnectNonce: 30000, ReconnectCount: -1}, cache: map[string]*feishuFragment{}, done: make(chan struct{})}, nil
}
func (w *FeishuWS) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return context.Canceled
	}
	if w.started {
		return nil
	}
	w.started = true
	w.wg.Add(2)
	go func() { defer w.wg.Done(); defer close(w.done); w.run() }()
	go func() {
		defer w.wg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-w.ctx.Done():
				return
			case <-t.C:
				w.SweepFragments()
			}
		}
	}()
	return nil
}
func (w *FeishuWS) Wait() error {
	select {
	case <-w.done:
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}
func (w *FeishuWS) Close() {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.cancel()
		socket := w.socket
		w.socket = nil
		w.mu.Unlock()
		if socket != nil {
			_ = socket.Close("ws client closed manually")
		}
		w.wg.Wait()
	})
}
func (w *FeishuWS) Config() FeishuWSConfig { w.mu.Lock(); defer w.mu.Unlock(); return w.config }
func (w *FeishuWS) ReconnectInfo() (int64, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastConnect, w.nextConnect
}
func feishuWSNumber(o protocol.JSONObject, key string) float64 {
	v := o.Get(key)
	if len(v) == 0 {
		return math.NaN()
	}
	return qqNumber(v)
}
func (w *FeishuWS) PullConnectConfig(ctx context.Context) (bool, error) {
	ctx, done, e := w.o.Client.begin(ctx)
	if e != nil {
		return false, e
	}
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body := raw(feishuObject(protocol.JSONField{Name: "AppID", Value: w.o.Client.o.Credentials.Get("appId")}, protocol.JSONField{Name: "AppSecret", Value: w.o.Client.o.Credentials.Get("appSecret")}))
	req, e := http.NewRequestWithContext(ctx, "POST", w.o.Client.Origin+"/callback/ws/endpoint", bytes.NewReader(body))
	if e != nil {
		return false, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", FeishuSDKUserAgent)
	req.Header.Set("locale", "zh")
	res, e := w.o.Client.o.HTTP.Do(req)
	if e != nil {
		return false, e
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		return false, e
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return false, &FeishuHTTPError{res.StatusCode, feishuResponse(data)}
	}
	response := object(feishuResponse(data))
	code, number := wechatNumeric(response.Get("code"))
	if number && (code == 1 || code == 1000040343) {
		return false, nil
	}
	d := object(response.Get("data"))
	address := d.Get("URL")
	clientConfig := object(d.Get("ClientConfig"))
	if !isString(address) || !clientConfig.IsObject() {
		return false, nil
	}
	// SDK 1.60 passes the whole URL to querystring.parse (the first key retains
	// its URL prefix); service_id is taken from that original parse result.
	q, _ := url.ParseQuery(TextJSON(address).String())
	service := math.NaN()
	if vs := q["service_id"]; len(vs) > 0 {
		if len(vs) == 1 {
			service = qqNumber(raw(vs[0]))
		} else {
			service = qqNumber(raw(vs))
		}
	}
	cfg := FeishuWSConfig{URL: TextJSON(address).String(), Service: feishuInt(service), PingInterval: feishuWSNumber(clientConfig, "PingInterval") * 1000, ReconnectCount: feishuWSNumber(clientConfig, "ReconnectCount"), ReconnectInterval: feishuWSNumber(clientConfig, "ReconnectInterval") * 1000, ReconnectNonce: feishuWSNumber(clientConfig, "ReconnectNonce") * 1000}
	w.mu.Lock()
	w.config = cfg
	w.mu.Unlock()
	return true, nil
}
func feishuTimer(ms float64) time.Duration {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 1 || ms > 2147483647 {
		return time.Millisecond
	}
	return time.Duration(math.Trunc(ms)) * time.Millisecond
}
func (w *FeishuWS) send(f FeishuFrame) error {
	w.mu.Lock()
	s := w.socket
	closed := w.closed
	w.mu.Unlock()
	if closed || s == nil {
		return nil
	}
	return s.SendBinary(EncodeFeishuFrame(f))
}
func (w *FeishuWS) Ping() error {
	cfg := w.Config()
	return w.send(FeishuFrame{Service: cfg.Service, Method: 0, Headers: []FeishuHeader{{"type", "ping"}}})
}
func (w *FeishuWS) run() {
	first := true
	count := 0
	for w.ctx.Err() == nil {
		if !first {
			if w.o.AutoReconnect != nil && !*w.o.AutoReconnect {
				return
			}
			cfg := w.Config()
			delay := cfg.ReconnectInterval
			if count == 0 {
				delay = 0
				if cfg.ReconnectNonce != 0 {
					delay = cfg.ReconnectNonce * w.o.Random()
				}
			}
			w.mu.Lock()
			w.nextConnect = w.o.Now().UnixMilli() + int64(delay)
			w.mu.Unlock()
			if w.o.Sleep(w.ctx, feishuTimer(delay)) != nil {
				return
			}
			count++
		}
		w.mu.Lock()
		w.lastConnect = w.o.Now().UnixMilli()
		w.mu.Unlock()
		ok, e := w.PullConnectConfig(w.ctx)
		if e != nil {
			w.o.Log("[ws] " + e.Error())
		}
		var socket FeishuSocket
		if ok {
			socket, e = w.o.Dial(w.ctx, w.Config().URL, w.o.TLS)
			if e != nil {
				w.o.Log("[ws] ws connect failed: " + e.Error())
			}
		}
		if socket == nil {
			first = false
			cfg := w.Config()
			if count > 0 && cfg.ReconnectCount >= 0 && float64(count) >= cfg.ReconnectCount {
				return
			}
			continue
		}
		count = 0
		first = false
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			_ = socket.Close("closed")
			return
		}
		w.socket = socket
		w.mu.Unlock()
		_ = w.Ping()
		pingCtx, pingCancel := context.WithCancel(w.ctx)
		pingDone := make(chan struct{})
		go func() {
			defer close(pingDone)
			for {
				cfg := w.Config()
				timer := time.NewTimer(feishuTimer(cfg.PingInterval))
				select {
				case <-pingCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
					_ = w.Ping()
				}
			}
		}()
		for w.ctx.Err() == nil {
			p, e := socket.Read()
			if e != nil {
				break
			}
			f, e := DecodeFeishuFrame(p)
			if e != nil {
				w.o.Log("[ws] " + e.Error())
				continue
			}
			w.mu.Lock()
			if w.closed {
				w.mu.Unlock()
				break
			}
			w.wg.Add(1)
			w.mu.Unlock()
			go func() {
				defer w.wg.Done()
				if e := w.HandleFrame(w.ctx, f); e != nil {
					w.o.Log("[ws] " + e.Error())
				}
			}()
		}
		pingCancel()
		<-pingDone
		w.mu.Lock()
		if w.socket == socket {
			w.socket = nil
		}
		w.mu.Unlock()
		_ = socket.Close("reconnect")
	}
}
func (w *FeishuWS) SweepFragments() {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.o.Now().UnixMilli()
	for key, value := range w.cache {
		if now-value.created > 10000 {
			delete(w.cache, key)
		}
	}
}
func (w *FeishuWS) HandleFrame(ctx context.Context, f FeishuFrame) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.Method == 0 {
		kind := ""
		for _, h := range f.Headers {
			if h.Key == "type" {
				kind = h.Value
				break
			}
		}
		if kind == "pong" && f.Payload != nil {
			data := object(f.Payload)
			if !data.IsObject() {
				return errors.New("invalid pong config")
			}
			w.mu.Lock()
			w.config.PingInterval = feishuWSNumber(data, "PingInterval") * 1000
			w.config.ReconnectCount = feishuWSNumber(data, "ReconnectCount")
			w.config.ReconnectInterval = feishuWSNumber(data, "ReconnectInterval") * 1000
			w.config.ReconnectNonce = feishuWSNumber(data, "ReconnectNonce") * 1000
			w.mu.Unlock()
		}
		return nil
	}
	if f.Method != 1 {
		return nil
	}
	headers := map[string]string{}
	for _, h := range f.Headers {
		headers[h.Key] = h.Value
	}
	if headers["type"] != "event" {
		return nil
	}
	sum := math.NaN()
	if value, ok := headers["sum"]; ok {
		sum = qqNumber(raw(value))
	}
	if math.IsNaN(sum) || sum < 0 || sum > 4294967295 || math.Trunc(sum) != sum {
		return errors.New("Invalid array length")
	}
	seq := math.NaN()
	if value, ok := headers["seq"]; ok {
		seq = qqNumber(raw(value))
	}
	key := "u:"
	if id, ok := headers["message_id"]; ok {
		key = "s:" + id
	}
	w.mu.Lock()
	part := w.cache[key]
	if part == nil {
		part = &feishuFragment{parts: map[int64][]byte{}, length: int64(sum), created: w.o.Now().UnixMilli()}
		w.cache[key] = part
	}
	if seq >= 0 && seq < 4294967295 && math.Trunc(seq) == seq {
		n := int64(seq)
		part.parts[n] = append([]byte{}, f.Payload...)
		if n >= part.length {
			part.length = n + 1
		}
	}
	complete := int64(len(part.parts)) == part.length
	var payload []byte
	if complete {
		payload = []byte{}
		for i := int64(0); i < part.length; i++ {
			p, ok := part.parts[i]
			if !ok {
				complete = false
				break
			}
			payload = append(payload, p...)
		}
		if complete && json.Valid(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(payload), []byte{239, 187, 191})) {
			delete(w.cache, key)
		}
	}
	w.mu.Unlock()
	if !complete {
		return nil
	}
	payload = bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(payload), []byte{239, 187, 191})
	if !json.Valid(payload) {
		return errors.New("invalid event JSON")
	}
	// DataCache returns the parsed value; WSClient skips falsy merged data.
	if !credentialTruthy(payload) {
		return nil
	}
	response := feishuObject(feishuField("code", 200))
	start := w.o.Now().UnixMilli()
	result, e := w.o.Dispatcher.Invoke(ctx, payload, false)
	if e != nil {
		response.Set("code", raw(500))
		w.o.Log("[ws] invoke event failed: " + e.Error())
	} else if credentialTruthy(result) {
		response.Set("data", raw(base64.StdEncoding.EncodeToString(protocol.StringifyValidatedJavaScriptJSON(result))))
	}
	end := w.o.Now().UnixMilli()
	f.Headers = append(append([]FeishuHeader{}, f.Headers...), FeishuHeader{"biz_rt", fmt.Sprint(start - end)})
	f.Payload = raw(response)
	return w.send(f)
}
