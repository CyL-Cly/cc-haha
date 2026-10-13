package imruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type WechatRuntimeOptions struct {
	Config     Config
	API        *WechatAPI
	Media      *WechatMedia
	ServerHTTP *http.Client
	SocketTLS  *tls.Config
	Log        func(string)
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
}

type WechatRuntime struct {
	migration    *MigrationLifecycle
	o            WechatRuntimeOptions
	API          *WechatAPI
	Media        *WechatMedia
	Contexts     *WechatContexts
	Sender       *WechatSender
	Typing       *WechatTypingController
	Bridge       *Bridge
	Client       *HTTPClient
	Store        *SessionStore
	Pairing      *Pairing
	Dedup        *MessageDedup
	Attachments  *AttachmentStore
	Chat         *ChatRuntime
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	cursor       json.RawMessage
	start, close sync.Once
	done         chan struct{}
	wg           sync.WaitGroup
}

func adapterSleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NewWechatRuntime(ctx context.Context, o WechatRuntimeOptions) (*WechatRuntime, error) {
	if err := migrationRuntimeAvailable(o.Config); err != nil {
		return nil, err
	}
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	if !filepath.IsAbs(o.Config.Home) || !filepath.IsAbs(o.Config.ConfigDir) {
		return nil, errors.New("WeChat runtime requires explicit home/config")
	}
	credentials := WechatConfig(o.Config)
	if !credentialTruthy(credentials.BotToken) || !credentialTruthy(credentials.AccountID) {
		return nil, errors.New("[WeChat] Missing QR-bound account. Bind WeChat in Desktop Settings > IM.")
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = adapterSleep
	}
	var err error
	if o.API == nil {
		o.API, err = NewWechatAPI(WechatAPIOptions{BaseURL: credentials.BaseURL, Token: credentials.BotToken, Environment: o.Config.Environment})
		if err != nil {
			return nil, err
		}
	}
	if o.ServerHTTP == nil {
		o.ServerHTTP, err = AdapterNetworkHTTP(o.Config.Environment, true)
		if err != nil {
			o.API.Close()
			return nil, err
		}
	}
	if o.SocketTLS == nil {
		client, e := AdapterNetworkHTTP(o.Config.Environment, false)
		if e != nil {
			o.API.Close()
			o.ServerHTTP.CloseIdleConnections()
			return nil, e
		}
		if transport, ok := client.Transport.(*http.Transport); ok {
			o.SocketTLS = transport.TLSClientConfig
		}
		client.CloseIdleConnections()
	}
	ctx, lifecycle := newMigrationContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	r := &WechatRuntime{migration: lifecycle, o: o, API: o.API, ctx: ctx, cancel: cancel, cursor: raw(""), done: make(chan struct{})}
	r.Bridge = NewBridge(ctx, o.Config, o.SocketTLS, o.Log, BridgeOptions{Now: o.Now})
	r.Client = NewHTTPClient(o.Config, o.ServerHTTP)
	r.Store = NewSessionStore(o.Config.ConfigDir)
	r.Store.Lifecycle = lifecycle
	r.Store.NowMillis = func() int64 { return o.Now().UnixMilli() }
	r.Pairing = NewPairing(o.Config.ConfigDir, nil)
	r.Pairing.Lifecycle = lifecycle
	r.Pairing.NowMillis = r.Store.NowMillis
	r.Dedup = NewMessageDedup(ctx)
	r.Dedup.Now = o.Now
	r.Attachments = NewConfiguredAttachmentStore(o.Config)
	r.Attachments.Lifecycle = lifecycle
	r.Attachments.Now = o.Now
	if o.Media == nil {
		o.Media, err = NewWechatMedia(WechatMediaOptions{Environment: o.Config.Environment, Store: r.Attachments, Now: o.Now})
		if err != nil {
			cancel()
			r.Bridge.Close()
			r.Dedup.Close()
			r.API.Close()
			o.ServerHTTP.CloseIdleConnections()
			return nil, err
		}
	}
	r.Media = o.Media
	r.Contexts = NewWechatContexts()
	r.Sender = &WechatSender{API: r.API, Contexts: r.Contexts, Warn: o.Log}
	r.Typing = NewWechatTypingController(ctx, r.API, r.Contexts, WechatTypingOptions{Log: o.Log})
	port := ChatPort{Platform: "wechat", LogPrefix: "[WeChat]", SendNotice: r.Notice, CreateResponse: func(id string) ResponseStream { return &wechatResponse{runtime: r, chat: id} }, SetBusy: func(id string, busy bool) {
		if busy {
			r.Typing.Start(id)
		} else {
			r.Typing.Stop(id)
		}
	}}
	r.Chat = NewChatRuntime(ctx, ChatRuntimeOptions{Config: o.Config, Port: port, Bridge: r.Bridge, Client: r.Client, Store: r.Store, Pairing: r.Pairing, Dedup: r.Dedup, FlushInterval: 3 * time.Second, FlushThreshold: 200, Log: o.Log, Now: o.Now})
	return r, nil
}

type wechatResponse struct {
	runtime *WechatRuntime
	chat    string
}

func (s *wechatResponse) Append(text Text) error {
	return s.runtime.Sender.Send(s.runtime.ctx, s.chat, text)
}
func (*wechatResponse) Finish() error { return nil }

func (r *WechatRuntime) Notice(id, text string) error { return r.Sender.Send(r.ctx, id, NewText(text)) }
func (r *WechatRuntime) Cursor() json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(json.RawMessage{}, r.cursor...)
}
func (r *WechatRuntime) Start() {
	r.start.Do(func() {
		r.o.Log("[WeChat] Starting adapter...")
		r.wg.Add(2)
		go func() {
			defer r.wg.Done()
			r.Attachments.GC()
		}()
		go func() { defer r.wg.Done(); defer close(r.done); r.poll() }()
	})
}
func (r *WechatRuntime) Wait() error { <-r.done; return nil }
func (r *WechatRuntime) Close() {
	r.close.Do(func() {
		r.cancel()
		r.Typing.Close()
		r.Bridge.Close()
		r.wg.Wait()
		r.Chat.Close()
		r.Dedup.Close()
		r.Media.Close()
		r.API.Close()
		r.o.ServerHTTP.CloseIdleConnections()
	})
}
func (r *WechatRuntime) poll() {
	for r.ctx.Err() == nil {
		body, err := r.API.GetUpdates(r.ctx, r.Cursor(), 35*time.Second)
		if r.ctx.Err() != nil {
			return
		}
		if err == nil {
			err = r.HandleUpdates(body)
		}
		if err != nil {
			r.o.Log("[WeChat] poll loop error: " + err.Error())
			if r.o.Sleep(r.ctx, 3*time.Second) != nil {
				return
			}
		}
	}
}
func (r *WechatRuntime) HandleUpdates(body json.RawMessage) error {
	if string(bytes.TrimSpace(body)) == "null" {
		return errors.New("null is not an object (evaluating 'resp.get_updates_buf')")
	}
	response := object(body)
	if value := response.Get("get_updates_buf"); credentialTruthy(value) {
		r.mu.Lock()
		r.cursor = append(json.RawMessage{}, value...)
		r.mu.Unlock()
	}
	ret, hasRet := wechatNumeric(response.Get("ret"))
	errCode, hasErr := wechatNumeric(response.Get("errcode"))
	if hasRet && ret != 0 || hasErr && errCode != 0 {
		return errors.New("[WeChat] getupdates returned an error")
	}
	msgs := response.Get("msgs")
	if len(msgs) == 0 || string(msgs) == "null" {
		return nil
	}
	// Strings are iterable in Source; each character has no from_user_id.
	if isString(msgs) {
		return nil
	}
	var messages []json.RawMessage
	if json.Unmarshal(msgs, &messages) != nil {
		return errors.New("resp.msgs is not iterable")
	}
	for _, message := range messages {
		if err := r.HandleMessage(message); err != nil {
			return err
		}
	}
	return nil
}
func (r *WechatRuntime) HandleMessage(body json.RawMessage) error {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil
	}
	defer done(nil)
	if string(bytes.TrimSpace(body)) == "null" {
		return errors.New("null is not an object (evaluating 'message.from_user_id')")
	}
	message := object(body)
	user := message.Get("from_user_id")
	if !credentialTruthy(user) {
		return nil
	}
	stringUser := isString(user)
	id := ""
	if stringUser {
		id = TextJSON(user).String()
	}
	keys := []Text{}
	for _, field := range []string{"message_id", "seq", "create_time_ms"} {
		v := message.Get(field)
		if len(v) == 0 || string(v) == "null" {
			v = raw("")
		}
		s, err := wechatString(v)
		if err != nil {
			return err
		}
		keys = append(keys, s)
	}
	if !r.Dedup.TryRecord(string(raw(JoinText(keys, NewText(":"))))) {
		return nil
	}
	if stringUser {
		r.Contexts.Set(id, message.Get("context_token"))
	}
	textJSON, err := WechatExtractText(message.Get("item_list"))
	if err != nil {
		return err
	}
	if !isString(textJSON) {
		return errors.New("extractWechatText(...).trim is not a function")
	}
	text := TextJSON(textJSON).Trim()
	candidates, err := WechatCollectMediaCandidates(message.Get("item_list"), r.o.Now)
	if err != nil {
		return err
	}
	if len(text) == 0 && len(candidates) == 0 {
		return nil
	}
	// Source redacts only nonempty messages after dedup/extraction. Even an
	// invalid sender therefore consumes its message key before that failure.
	if !stringUser {
		return errors.New("chatId.slice is not a function")
	}
	if !r.Pairing.Allowed("wechat", user) {
		paired := false
		if len(text) > 0 {
			paired, err = r.Pairing.TryPair("wechat", text.String(), "WeChat User", user)
			if err != nil {
				return err
			}
		}
		if paired {
			return r.Notice(id, "配对成功！现在可以开始聊天了。\n\n发送消息即可与 Claude 对话。发送 /help 查看可用命令。")
		}
		return r.Notice(id, "未授权。请先在 Claude Code 桌面端完成微信扫码绑定，再生成 IM 配对码后发送给我。")
	}
	hasAttachments := len(candidates) > 0
	r.Chat.QueueInbound(InboundChatMessage{ChatID: id, UserID: user, DisplayName: "WeChat User", Text: text.String(), TextValue: &text, HasAttachments: &hasAttachments, authorized: true, LoadAttachments: func() ([]protocol.JSONObject, error) {
		session := id
		if stored := r.Store.Get(id); stored != nil {
			session = stored.SessionID
		}
		return r.Media.Collect(r.ctx, candidates, session, func(notice string) error { return r.Notice(id, notice) })
	}})
	return nil
}
