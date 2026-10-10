package imregistration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
)

const QQReadyTimeout = 20 * time.Second
const qqSessionTTLMillis = 10 * 60 * 1000

type QQOptions struct {
	Client   *http.Client
	Now      func() time.Time
	UUID     func() string
	Random   io.Reader
	Schedule func(time.Duration, func()) func()
}

type qqReady struct {
	url string
	err error
}
type qqSession struct {
	key                          json.RawMessage
	url                          string
	urlVersion, deliveredVersion int
	status                       string
	credentials                  protocol.JSONObject
	failure                      string
	startedAt                    int64
	ready                        chan qqReady
	readyOnce, abortOnce         sync.Once
	aborted                      chan struct{}
	done                         chan struct{}
}

type QQService struct {
	o         QQOptions
	http      qqHTTP
	mu        sync.Mutex
	randomMu  sync.Mutex
	logins    map[string]*qqSession
	workers   sync.WaitGroup
	ioContext context.Context
	stopIO    context.CancelFunc
	closed    bool
}

func NewQQ(o QQOptions) (*QQService, error) {
	client, transport, e := newQQHTTPClient(o.Client)
	if e != nil {
		return nil, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.Schedule == nil {
		o.Schedule = func(d time.Duration, f func()) func() { t := time.AfterFunc(d, f); return func() { t.Stop() } }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &QQService{o: o, http: qqHTTP{client: client, transport: transport, drains: new(sync.WaitGroup)}, logins: map[string]*qqSession{}, ioContext: ctx, stopIO: cancel}, nil
}

func (s *QQService) dispose(entry *qqSession) { entry.abortOnce.Do(func() { close(entry.aborted) }) }
func (s *QQService) purgeLocked() {
	now := s.o.Now().UnixMilli()
	for key, entry := range s.logins {
		if now-entry.startedAt > qqSessionTTLMillis {
			delete(s.logins, key)
			s.dispose(entry)
		}
	}
}

func (s *QQService) displayed(entry *qqSession, url string) {
	s.mu.Lock()
	entry.url = url
	entry.urlVersion++
	s.mu.Unlock()
	entry.readyOnce.Do(func() { entry.ready <- qqReady{url: url} })
}
func (s *QQService) failed(entry *qqSession, e error) {
	message := e.Error()
	status := "failed"
	lower := strings.ToLower(message)
	if strings.Contains(lower, "expire") || strings.Contains(lower, "timeout") || strings.Contains(message, "过期") {
		status = "expired"
	}
	s.mu.Lock()
	entry.status, entry.failure = status, message
	s.mu.Unlock()
	entry.readyOnce.Do(func() { entry.ready <- qqReady{err: e} })
}
func (s *QQService) succeeded(entry *qqSession, credentials protocol.JSONObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !truthy(credentials.Get("appId")) || !truthy(credentials.Get("appSecret")) {
		entry.status = "failed"
		entry.failure = "QQ 返回的机器人凭据不完整，请重新扫码。"
		return
	}
	entry.status = "success"
	entry.credentials = credentials.Clone()
}
func qqAborted(entry *qqSession) bool {
	select {
	case <-entry.aborted:
		return true
	default:
		return false
	}
}
func (s *QQService) sleep(entry *qqSession) error {
	if qqAborted(entry) {
		return fmt.Errorf("已取消")
	}
	ready := make(chan struct{})
	stop := s.o.Schedule(time.Duration(QQPollIntervalMillis)*time.Millisecond, func() { close(ready) })
	defer stop()
	select {
	case <-entry.aborted:
		return fmt.Errorf("已取消")
	case <-s.ioContext.Done():
		return fmt.Errorf("已取消")
	case <-ready:
		return nil
	}
}

func (s *QQService) driver(entry *qqSession, source json.RawMessage) {
	defer s.workers.Done()
	defer close(entry.done)
	for {
		if qqAborted(entry) {
			s.failed(entry, fmt.Errorf("已取消"))
			return
		}
		key := make([]byte, 32)
		s.randomMu.Lock()
		_, randomError := io.ReadFull(s.o.Random, key)
		s.randomMu.Unlock()
		if e := randomError; e != nil {
			s.failed(entry, fmt.Errorf("获取绑定任务失败: %s", e))
			return
		}
		bindKey := base64.StdEncoding.EncodeToString(key)
		body, e := s.http.post(s.ioContext, QQCreateBindURL, record(field("key", bindKey)))
		if e != nil {
			s.failed(entry, fmt.Errorf("获取绑定任务失败: %s", e))
			return
		}
		task, e := qqCreateResult(body)
		if e != nil {
			s.failed(entry, fmt.Errorf("获取绑定任务失败: %s", e))
			return
		}
		url, e := QQConnectURL(task, source)
		if e != nil {
			s.failed(entry, e)
			return
		}
		s.displayed(entry, url)
		for {
			if qqAborted(entry) {
				s.failed(entry, fmt.Errorf("已取消"))
				return
			}
			body, e = s.http.post(s.ioContext, QQPollBindURL, record(protocol.JSONField{Name: "task_id", Value: task}))
			if e == nil {
				body, e = qqPollResult(body)
			}
			if e != nil {
				if e = s.sleep(entry); e != nil {
					s.failed(entry, e)
					return
				}
				continue
			}
			if qqStrictStatus(body.Get("status"), 2) {
				plain, e := QQDecryptSecretJSON(body.Get("botEncryptSecret"), bindKey)
				if e != nil {
					s.failed(entry, e)
					return
				}
				s.succeeded(entry, record(protocol.JSONField{Name: "appId", Value: body.Get("botAppId")}, field("appSecret", plain), protocol.JSONField{Name: "userOpenid", Value: body.Get("userOpenid")}))
				return
			}
			if qqStrictStatus(body.Get("status"), 3) {
				break
			}
			if e = s.sleep(entry); e != nil {
				s.failed(entry, e)
				return
			}
		}
	}
}

func (s *QQService) Start(ctx context.Context, opts protocol.JSONObject) (protocol.JSONObject, error) {
	key := opts.Get("sessionKey")
	if !truthy(key) {
		key = encoded(s.o.UUID())
	}
	name, e := feishuKey(key)
	if e != nil {
		return record(), e
	}
	source := opts.Get("source")
	if !truthy(source) {
		source = encoded("claude-code-haha")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return record(), fmt.Errorf("QQ registration is closed")
	}
	s.purgeLocked()
	for old, entry := range s.logins {
		delete(s.logins, old)
		s.dispose(entry)
	}
	entry := &qqSession{key: append(json.RawMessage(nil), key...), status: "waiting", startedAt: s.o.Now().UnixMilli(), ready: make(chan qqReady, 1), aborted: make(chan struct{}), done: make(chan struct{})}
	s.workers.Add(1)
	s.mu.Unlock()
	go s.driver(entry, source)
	timeout := make(chan struct{})
	stop := s.o.Schedule(QQReadyTimeout, func() { close(timeout) })
	defer stop()
	var ready qqReady
	select {
	case ready = <-entry.ready:
	case <-timeout:
		s.dispose(entry)
		return record(), fmt.Errorf("QQ 二维码获取超时，请重试。")
	case <-ctx.Done():
		s.dispose(entry)
		return record(), context.Cause(ctx)
	}
	if ready.err != nil {
		s.dispose(entry)
		return record(), ready.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.dispose(entry)
		return record(), fmt.Errorf("QQ registration is closed")
	}
	entry.deliveredVersion = entry.urlVersion
	s.logins[name] = entry
	return record(protocol.JSONField{Name: "sessionKey", Value: key}, field("verificationUrl", ready.url), field("pollIntervalMs", QQPollIntervalMillis), field("message", "使用 QQ 扫描二维码，创建并授权机器人。")), nil
}

func (s *QQService) PollJSON(ctx context.Context, key json.RawMessage) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return record(), context.Cause(ctx)
	}
	name, e := feishuKey(key)
	if e != nil {
		return record(), e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	entry := s.logins[name]
	if entry == nil {
		return qqPoll("not_started", "当前没有进行中的 QQ 绑定，请重新生成二维码。"), nil
	}
	if entry.status == "success" && entry.credentials.IsObject() {
		delete(s.logins, name)
		s.dispose(entry)
		return record(field("connected", true), protocol.JSONField{Name: "appId", Value: entry.credentials.Get("appId")}, protocol.JSONField{Name: "appSecret", Value: entry.credentials.Get("appSecret")}, protocol.JSONField{Name: "userOpenid", Value: entry.credentials.Get("userOpenid")}), nil
	}
	if entry.status == "expired" || entry.status == "failed" {
		delete(s.logins, name)
		s.dispose(entry)
		return qqPoll(entry.status, entry.failure), nil
	}
	result := qqPoll("waiting", "等待 QQ 扫码确认...")
	if entry.urlVersion != entry.deliveredVersion {
		result.Set("verificationUrl", encoded(entry.url))
	}
	entry.deliveredVersion = entry.urlVersion
	return result, nil
}
func qqPoll(status, message string) protocol.JSONObject {
	return record(field("connected", false), field("status", status), field("message", message))
}
func (s *QQService) CancelJSON(key json.RawMessage) error {
	name, e := feishuKey(key)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.logins[name]; entry != nil {
		delete(s.logins, name)
		s.dispose(entry)
	}
	return nil
}
func (s *QQService) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for key, entry := range s.logins {
		delete(s.logins, key)
		s.dispose(entry)
	}
	s.mu.Unlock()
	s.stopIO()
	s.http.transport.CloseIdleConnections()
	done := make(chan struct{})
	go func() { s.workers.Wait(); s.http.drains.Wait(); s.http.transport.CloseIdleConnections(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
