package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type FeishuRuntimeOptions struct {
	Remote                        FeishuRemoteOptions
	Config                        Config
	Credentials                   protocol.JSONObject
	Domain                        string
	EncryptKey, VerificationToken Text
	Client                        *FeishuClient
	ServerHTTP                    *http.Client
	SocketTLS                     *tls.Config
	WSOptions                     FeishuWSOptions
	Now                           func() time.Time
	Sleep                         func(context.Context, time.Duration) error
	Log                           func(string)
	Streaming                     FeishuStreamingOptions
}
type feishuRuntimeChat struct {
	mu            sync.Mutex
	tail          chan struct{}
	state         ChatState
	card          *FeishuStreamingCard
	selection     *SessionSelection
	projectPicker bool
	watcher       ImageWatcher
	uploaded      map[string]json.RawMessage
}
type FeishuRuntime struct {
	migration    *MigrationLifecycle
	o            FeishuRuntimeOptions
	API          *FeishuClient
	Media        *FeishuMedia
	Dispatcher   *FeishuDispatcher
	WS           *FeishuWS
	Bridge       *Bridge
	Client       *HTTPClient
	Store        *SessionStore
	Pairing      *Pairing
	Dedup        *MessageDedup
	Attachments  *AttachmentStore
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	chats        map[string]*feishuRuntimeChat
	closed       bool
	wg           sync.WaitGroup
	start, close sync.Once
	botOpenID    json.RawMessage
}

func NewFeishuRuntime(ctx context.Context, o FeishuRuntimeOptions) (*FeishuRuntime, error) {
	if err := migrationRuntimeAvailable(o.Config); err != nil {
		return nil, err
	}
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	if !filepath.IsAbs(o.Config.Home) || !filepath.IsAbs(o.Config.ConfigDir) {
		return nil, fmt.Errorf("Feishu runtime requires explicit home/config")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = adapterSleep
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	if o.Client != nil && !o.Credentials.IsObject() {
		o.Credentials = o.Client.o.Credentials.Clone()
	}
	if !credentialTruthy(o.Credentials.Get("appId")) || !credentialTruthy(o.Credentials.Get("appSecret")) {
		return nil, fmt.Errorf("[Feishu] Missing FEISHU_APP_ID / FEISHU_APP_SECRET. Set env or ~/.claude/adapters.json")
	}
	var e error
	if o.Client == nil {
		o.Client, e = NewFeishuClient(ctx, FeishuClientOptions{Credentials: o.Credentials, Domain: o.Domain, Environment: o.Config.Environment, Now: o.Now, Log: o.Log})
		if e != nil {
			return nil, e
		}
	}
	if o.ServerHTTP == nil {
		o.ServerHTTP, e = AdapterNetworkHTTP(o.Config.Environment, true)
		if e != nil {
			o.Client.Close()
			return nil, e
		}
	}
	if o.SocketTLS == nil {
		c, e := AdapterNetworkHTTP(o.Config.Environment, false)
		if e != nil {
			o.Client.Close()
			return nil, e
		}
		if transport, ok := c.Transport.(*http.Transport); ok {
			o.SocketTLS = transport.TLSClientConfig
		}
		c.CloseIdleConnections()
	}
	ctx, lifecycle := newMigrationContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	r := &FeishuRuntime{migration: lifecycle, o: o, API: o.Client, ctx: ctx, cancel: cancel, chats: map[string]*feishuRuntimeChat{}}
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
	r.Media = NewFeishuMedia(r.API, r.Attachments)
	r.Dispatcher = NewFeishuDispatcher(o.EncryptKey, o.VerificationToken)
	r.Dispatcher.Register("im.message.receive_v1", func(ctx context.Context, event protocol.JSONObject) (json.RawMessage, error) {
		if e := r.HandleMessage(event); e != nil {
			r.o.Log("[Feishu] Message handler error: " + e.Error())
		}
		return nil, nil
	})
	r.Dispatcher.Register("card.action.trigger", func(ctx context.Context, event protocol.JSONObject) (json.RawMessage, error) {
		value, e := r.HandleCardAction(event)
		if e != nil {
			r.o.Log("[Feishu] Card action error: " + e.Error())
			return nil, nil
		}
		return value, nil
	})
	wsOptions := o.WSOptions
	wsOptions.Client = r.API
	wsOptions.Dispatcher = r.Dispatcher
	if wsOptions.Now == nil {
		wsOptions.Now = o.Now
	}
	if wsOptions.Log == nil {
		wsOptions.Log = o.Log
	}
	r.WS, e = NewFeishuWS(ctx, wsOptions)
	if e != nil {
		r.Close()
		return nil, e
	}
	r.launch(func() { r.Attachments.GC() })
	return r, nil
}
func (r *FeishuRuntime) launch(f func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return false
	}
	r.wg.Add(1)
	go func() { defer r.wg.Done(); f() }()
	return true
}
func (r *FeishuRuntime) chat(id string) *feishuRuntimeChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	chat := r.chats[id]
	if chat == nil {
		chat = &feishuRuntimeChat{state: ChatState{State: "idle"}, uploaded: map[string]json.RawMessage{}}
		chat.selection = NewSessionSelection(SessionSelectionDeps{Client: r.Client, Bridge: r.Bridge, Store: r.Store, Handler: r.handler, Clear: r.ClearChat, Busy: r.Busy, Send: r.Notice, ClearProjects: func(id string) { r.clearProjectPicker(id) }, Now: r.o.Now})
		r.chats[id] = chat
	}
	return chat
}
func (r *FeishuRuntime) handler(id string) BridgeHandler {
	return func(ctx context.Context, m protocol.JSONObject) error {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			return nil
		}
		return r.HandleServerMessage(id, m)
	}
}
func (r *FeishuRuntime) State(id string) ChatState {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Pending = append([]string{}, s.Pending...)
	return s
}
func (r *FeishuRuntime) Busy(id string) bool {
	s := r.State(id)
	return s.State != "idle" || s.PendingPermissionCount > 0 || len(s.Pending) > 0
}
func (r *FeishuRuntime) Notice(id, text string) error {
	_, e := r.API.SendText(r.ctx, NewText(id), NewText(text), nil)
	return e
}
func (r *FeishuRuntime) enqueue(id string, f func() error) <-chan struct{} {
	c := r.chat(id)
	c.mu.Lock()
	previous := c.tail
	done := make(chan struct{})
	c.tail = done
	c.mu.Unlock()
	if !r.launch(func() {
		defer close(done)
		if previous != nil {
			select {
			case <-previous:
			case <-r.ctx.Done():
				return
			}
		}
		if r.ctx.Err() != nil {
			return
		}
		if e := f(); e != nil {
			r.o.Log("[ChatQueue] Error in task for chat " + id + ": " + e.Error())
		}
	}) {
		close(done)
	}
	return done
}
func (r *FeishuRuntime) Drain(ctx context.Context, id string) error {
	c := r.chat(id)
	c.mu.Lock()
	done := c.tail
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *FeishuRuntime) Start() error {
	var result error
	r.start.Do(func() { r.resolveBotOpenID(); result = r.WS.Start() })
	return result
}
func (r *FeishuRuntime) Wait() error { return r.WS.Wait() }
func (r *FeishuRuntime) Close() {
	r.close.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.cancel()
		chats := []*feishuRuntimeChat{}
		for _, c := range r.chats {
			chats = append(chats, c)
		}
		r.mu.Unlock()
		if r.WS != nil {
			r.WS.Close()
		}
		if r.Bridge != nil {
			r.Bridge.Close()
		}
		r.API.Close()
		r.wg.Wait()
		for _, c := range chats {
			c.mu.Lock()
			card := c.card
			c.mu.Unlock()
			if card != nil {
				card.Close()
			}
		}
		if r.Dedup != nil {
			r.Dedup.Close()
		}
		if r.o.ServerHTTP != nil {
			r.o.ServerHTTP.CloseIdleConnections()
		}
	})
}
func (r *FeishuRuntime) resolveBotOpenID() {
	for i := 0; i < 3 && r.ctx.Err() == nil; i++ {
		body, e := r.API.BotInfo(r.ctx)
		if e == nil {
			v := object(object(body).Get("bot")).Get("open_id")
			if len(v) == 0 || string(v) == "null" {
				v = object(object(object(body).Get("data")).Get("bot")).Get("open_id")
			}
			if credentialTruthy(v) {
				r.mu.Lock()
				r.botOpenID = v
				r.mu.Unlock()
				return
			}
		} else if i < 2 {
			if r.o.Sleep(r.ctx, time.Duration(2000*(i+1))*time.Millisecond) != nil {
				return
			}
		}
	}
	r.o.Log("[Feishu] Could not resolve bot open_id (group @mention check may not work)")
}
func (r *FeishuRuntime) clearProjectPicker(id string) {
	c := r.chat(id)
	c.mu.Lock()
	c.projectPicker = false
	c.mu.Unlock()
}
func (r *FeishuRuntime) ClearChat(id string) {
	c := r.chat(id)
	c.mu.Lock()
	card := c.card
	c.card = nil
	c.watcher = ImageWatcher{}
	c.uploaded = map[string]json.RawMessage{}
	c.state.State = "idle"
	c.state.Verb = ""
	c.state.Pending = nil
	c.state.PendingPermissionCount = 0
	c.mu.Unlock()
	if card != nil {
		r.launch(func() { _ = card.Abort(r.ctx, fmt.Errorf("session cleared")); card.Close() })
	}
}
func (r *FeishuRuntime) prepareNew(id string) {
	c := r.chat(id)
	c.selection.Clear(id)
	r.Bridge.Reset(id)
	_ = r.Store.Delete(id)
	c.mu.Lock()
	card := c.card
	c.card = nil
	c.watcher = ImageWatcher{}
	c.uploaded = map[string]json.RawMessage{}
	c.state = ChatState{State: "idle"}
	c.mu.Unlock()
	if card != nil {
		r.launch(func() { _ = card.Abort(r.ctx, fmt.Errorf("session reset")); card.Close() })
	}
}
func (r *FeishuRuntime) existing(id string) (SessionRestore, error) {
	return RestoreSession(r.ctx, id, r.Store, r.Client, r.Bridge, r.handler(id), func() { r.ClearChat(id) })
}
func (r *FeishuRuntime) ensure(id string) (bool, error) {
	v, e := r.existing(id)
	if e != nil {
		return false, e
	}
	if v.Status == "restored" {
		return true, nil
	}
	if v.Status == "unavailable" {
		return false, r.Notice(id, SessionReconnectNotice)
	}
	if r.o.Config.WorkDir != "" {
		return r.create(id, r.o.Config.WorkDir)
	}
	return false, r.showProjects(id)
}
func (r *FeishuRuntime) create(id, work string) (bool, error) {
	c := r.chat(id)
	c.selection.Clear(id)
	r.Bridge.Reset(id)
	c.mu.Lock()
	card := c.card
	c.card = nil
	c.mu.Unlock()
	if card != nil {
		r.launch(func() { _ = card.Abort(r.ctx, fmt.Errorf("session reset")); card.Close() })
	}
	sid, e := r.Client.CreateSession(r.ctx, work)
	if e == nil {
		e = r.Store.Set(id, sid, work)
	}
	if e != nil {
		return false, r.Notice(id, "❌ 无法创建会话: "+e.Error())
	}
	r.Bridge.Connect(id, sid, r.handler(id))
	if !r.Bridge.WaitOpen(r.ctx, id) {
		return false, r.Notice(id, "⚠️ 连接服务器超时，请重试。")
	}
	return true, nil
}
func (r *FeishuRuntime) getCard(id string) *FeishuStreamingCard {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.card == nil {
		o := r.o.Streaming
		o.Client = r.API
		o.ChatID = NewText(id)
		if o.Now == nil {
			o.Now = r.o.Now
		}
		if o.Log == nil {
			o.Log = r.o.Log
		}
		c.card = NewFeishuStreamingCard(r.ctx, o)
	}
	return c.card
}
func (r *FeishuRuntime) detachCard(id string) *FeishuStreamingCard {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	card := c.card
	c.card = nil
	return card
}
func (r *FeishuRuntime) HandleMessage(event protocol.JSONObject) error {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil
	}
	defer done(nil)
	message := object(event.Get("message"))
	sender := object(object(event.Get("sender")).Get("sender_id"))
	mid, chat, user, content, kind := message.Get("message_id"), message.Get("chat_id"), sender.Get("open_id"), message.Get("content"), message.Get("message_type")
	for _, v := range []json.RawMessage{mid, chat, user, content, kind} {
		if !credentialTruthy(v) {
			return nil
		}
	}
	if !r.Dedup.TryRecord(string(protocol.StringifyValidatedJavaScriptJSON(mid))) {
		return nil
	}
	if string(message.Get("chat_type")) != `"p2p"` {
		return nil
	}
	id := jsString(chat)
	payload, e := FeishuExtractInbound(qqString(content), jsString(kind))
	if e != nil {
		return e
	}
	if !r.Pairing.Allowed("feishu", user) {
		text := payload.Text.Trim()
		if len(text) > 0 {
			paired, e := r.Pairing.TryPair("feishu", text.String(), "Feishu User", user)
			if e != nil {
				return e
			}
			if paired {
				return r.Notice(id, "✅ 配对成功！现在可以开始聊天了。\n\n发送消息即可与 Claude 对话。")
			}
			return r.Notice(id, "🔒 未授权。请在 Claude Code 桌面端生成配对码后发送给我。")
		}
		return nil
	}
	text := feishuReplaceString(payload.Text, `@_user_\d+`, false, true, "").Trim()
	if len(text) == 0 && len(payload.PendingDownloads) == 0 {
		return nil
	}
	r.enqueue(id, func() error { return r.route(id, text, qqString(mid), payload.PendingDownloads) })
	return nil
}
func (r *FeishuRuntime) route(id string, text Text, mid Text, downloads []FeishuPendingDownload) error {
	has := len(downloads) > 0
	c := r.chat(id)
	if !has {
		if decision := ParsePermissionCommand(text.String(), r.State(id).Pending); decision != nil {
			if r.applyPermission(id, *decision) {
				return r.Notice(id, PermissionDecisionStatus(*decision)+"。")
			}
			return nil
		}
		if handled, e := c.selection.Handle(r.ctx, id, text.String()); handled || e != nil {
			return e
		}
		if handled, e := r.projectInput(id, text.String()); handled || e != nil {
			return e
		}
		switch text.String() {
		case "/help", "帮助":
			return r.Notice(id, IMHelp())
		case "/status", "状态":
			status, e := r.status(id)
			if e != nil {
				return e
			}
			return r.Notice(id, status)
		case "/clear", "清空", "/stop", "停止":
			v, e := r.existing(id)
			if e != nil {
				return e
			}
			if v.Status != "restored" {
				notice := emptyStatus
				if v.Status == "unavailable" {
					notice = SessionReconnectNotice
				}
				return r.Notice(id, notice)
			}
			if text.String() == "/stop" || text.String() == "停止" {
				r.Bridge.Send(id, feishuObject(feishuField("type", "stop_generation")))
				return r.Notice(id, "⏹ 已发送停止信号。")
			}
			r.ClearChat(id)
			if r.Bridge.SendUser(id, "/clear", nil) {
				c.mu.Lock()
				c.state.State = "thinking"
				c.mu.Unlock()
				return r.Notice(id, "🧹 已清空当前会话上下文。")
			}
			return r.Notice(id, "⚠️ 无法发送 /clear，请先发送 /new 重新连接会话。")
		case "/projects", "项目列表":
			return r.showProjects(id)
		}
	}
	ready, e := r.ensure(id)
	if e != nil || !ready {
		return e
	}
	attachments, e := r.collect(id, mid, downloads)
	if e != nil {
		r.o.Log("[Feishu] Unexpected attachment pipeline error: " + e.Error())
		return r.Notice(id, "📎 附件处理异常,请稍后重试")
	}
	if len(text) == 0 && len(attachments) > 0 {
		text = NewText("(用户发送了附件)")
	}
	if len(text) == 0 && len(attachments) == 0 {
		return nil
	}
	card := r.getCard(id)
	r.launch(func() {
		if e := card.EnsureCreated(r.ctx); e != nil {
			r.o.Log("[Feishu] pre-create streaming card failed: " + e.Error())
		}
	})
	var refs json.RawMessage
	if len(attachments) > 0 {
		refs = raw(attachments)
	}
	if r.Bridge.SendUserText(id, text, refs) {
		c.mu.Lock()
		c.state.State = "thinking"
		c.mu.Unlock()
		return nil
	}
	return r.Notice(id, "⚠️ 消息发送失败，连接可能已断开。请发送 /new 重新开始。")
}
func (r *FeishuRuntime) collect(id string, mid Text, downloads []FeishuPendingDownload) ([]protocol.JSONObject, error) {
	session := id
	if stored := r.Store.Get(id); stored != nil {
		session = stored.SessionID
	}
	type result struct {
		local LocalAttachment
		e     error
	}
	results := make([]result, len(downloads))
	var wg sync.WaitGroup
	for i, p := range downloads {
		i, p := i, p
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := Text{}
			if p.FileName != nil {
				name = *p.FileName
			}
			local, e := r.Media.DownloadResource(r.ctx, FeishuDownloadParams{MessageID: mid, FileKey: p.FileKey, Kind: p.Kind, FileName: name, SessionID: session})
			results[i] = result{local, e}
		}()
	}
	wg.Wait()
	refs := []protocol.JSONObject{}
	failed := 0
	for _, result := range results {
		if result.e != nil {
			failed++
			r.o.Log("[Feishu] downloadResource failed: " + result.e.Error())
			continue
		}
		local := result.local
		check := CheckAttachmentLimit(local.Kind, local.Size, local.MIME)
		if !check.OK {
			if e := r.Notice(id, check.Hint); e != nil {
				return nil, e
			}
			continue
		}
		refs = append(refs, AttachmentRef(local))
	}
	if failed > 0 {
		notice := fmt.Sprintf("📎 %d 个附件下载失败,已跳过", failed)
		if failed == len(downloads) {
			notice = "📎 附件下载失败,请稍后重试"
		}
		if e := r.Notice(id, notice); e != nil {
			return nil, e
		}
	}
	return refs, nil
}
