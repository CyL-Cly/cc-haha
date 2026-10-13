package imruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TelegramRuntimeOptions struct {
	Config                Config
	API                   *TelegramAPI
	ServerHTTP, MediaHTTP *http.Client
	Log                   func(string)
	Delivery              TelegramDeliveryOptions
	Now                   func() time.Time
}
type telegramChat struct {
	queue            sync.Mutex
	mu               sync.Mutex
	state            ChatState
	thinking         Text
	watcher          ImageWatcher
	projectSelection bool
	selection        *telegramSelection
	sessionSelection *SessionSelection
}
type TelegramRuntime struct {
	migration *MigrationLifecycle
	options   TelegramRuntimeOptions
	API       *TelegramAPI
	Bridge    *Bridge
	Client    *HTTPClient
	Store     *SessionStore
	Pairing   *Pairing
	Delivery  *TelegramDelivery
	Media     *TelegramMedia
	Dedup     *MessageDedup
	Poller    *TelegramPoller
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	chats     map[string]*telegramChat
	closed    bool
	wg        sync.WaitGroup
	close     sync.Once
}

func NewTelegramRuntime(ctx context.Context, options TelegramRuntimeOptions) (*TelegramRuntime, error) {
	if err := migrationRuntimeAvailable(options.Config); err != nil {
		return nil, err
	}
	if options.Config.Err != nil {
		return nil, options.Config.Err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if !filepath.IsAbs(options.Config.Home) || !filepath.IsAbs(options.Config.ConfigDir) {
		return nil, errors.New("Telegram runtime requires explicit home/config")
	}
	if options.Log == nil {
		options.Log = func(string) {}
	}
	api := options.API
	if api == nil {
		var err error
		api, err = NewTelegramAPI(TelegramAPIOptions{Token: options.Config.Token, Environment: options.Config.Environment})
		if err != nil {
			return nil, err
		}
	}
	serverHTTP := options.ServerHTTP
	if serverHTTP == nil {
		var err error
		serverHTTP, err = AdapterNetworkHTTP(options.Config.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	mediaHTTP := options.MediaHTTP
	if mediaHTTP == nil {
		var err error
		mediaHTTP, err = AdapterNetworkHTTP(options.Config.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	options.ServerHTTP = serverHTTP
	options.MediaHTTP = mediaHTTP
	ctx, lifecycle := newMigrationContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	r := &TelegramRuntime{migration: lifecycle, options: options, API: api, ctx: ctx, cancel: cancel, chats: map[string]*telegramChat{}}
	r.Bridge = NewBridge(ctx, options.Config, nil, options.Log, BridgeOptions{Now: options.Now})
	r.Client = NewHTTPClient(options.Config, serverHTTP)
	r.Store = NewSessionStore(options.Config.ConfigDir)
	r.Store.Lifecycle = lifecycle
	r.Store.NowMillis = func() int64 { return options.Now().UnixMilli() }
	r.Pairing = NewPairing(options.Config.ConfigDir, nil)
	r.Pairing.Lifecycle = lifecycle
	r.Pairing.NowMillis = func() int64 { return options.Now().UnixMilli() }
	r.Delivery = NewTelegramDelivery(ctx, api, options.Delivery)
	r.Media = &TelegramMedia{api, NewConfiguredAttachmentStore(options.Config), mediaHTTP}
	r.Media.Store.Lifecycle = lifecycle
	r.Dedup = NewMessageDedup(ctx)
	r.Dedup.Now = options.Now
	r.Poller = &TelegramPoller{API: api, Handler: r.HandleUpdate, Log: options.Log}
	return r, nil
}
func (r *TelegramRuntime) launch(fn func()) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() { defer r.wg.Done(); fn() }()
}
func (r *TelegramRuntime) Start() {
	r.options.Log("[Telegram] Starting bot...")
	r.options.Log("[Telegram] Server: " + r.options.Config.ServerURL)
	users := collectJSONValues(object(r.options.Config.Document.Get("telegram")).Get("allowedUsers"))
	allowed := "paired users only"
	if len(users) > 0 {
		allowed, _ = protocol.JoinJavaScriptValues(users, ", ")
	}
	r.options.Log("[Telegram] Allowed users: " + allowed)
	r.launch(func() { r.Media.Store.GC() })
	r.launch(func() {
		if err := SyncTelegramCommands(r.ctx, r.API); err != nil && r.ctx.Err() == nil {
			r.options.Log("[Telegram] Command menu sync failed: " + err.Error())
		} else if err == nil {
			r.options.Log("[Telegram] Command menu synced")
		}
	})
	r.mu.Lock()
	if !r.closed {
		r.Poller.Start(r.ctx)
	}
	r.mu.Unlock()
}

func collectJSONValues(value json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	_ = json.Unmarshal(value, &result)
	return result
}
func (r *TelegramRuntime) Wait() error { return r.Poller.Wait() }
func (r *TelegramRuntime) Close() {
	r.close.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.cancel()
		r.Poller.Close()
		r.Bridge.Close()
		r.Dedup.Close()
		r.wg.Wait()
		r.Delivery.Close()
		r.options.ServerHTTP.CloseIdleConnections()
		r.options.MediaHTTP.CloseIdleConnections()
	})
}
func (r *TelegramRuntime) chat(id string) *telegramChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	if chat := r.chats[id]; chat != nil {
		return chat
	}
	chat := &telegramChat{state: ChatState{State: "idle"}}
	chat.sessionSelection = NewSessionSelection(SessionSelectionDeps{Client: r.Client, Bridge: r.Bridge, Store: r.Store, Handler: func(id string) BridgeHandler { return r.handler(id) }, Clear: r.ClearChat, Busy: r.Busy, Send: r.notice, ClearProjects: func(id string) { c := r.chat(id); c.projectSelection = false; c.selection = nil }, Now: r.options.Now})
	r.chats[id] = chat
	return chat
}
func (r *TelegramRuntime) Busy(id string) bool {
	chat := r.chat(id)
	chat.mu.Lock()
	defer chat.mu.Unlock()
	return chat.state.State != "idle" || len(chat.state.Pending) > 0
}
func (r *TelegramRuntime) notice(id, text string) error {
	_, err := r.API.SendMessage(r.ctx, id, NewText(text))
	return err
}
func (r *TelegramRuntime) handler(id string) BridgeHandler {
	return func(ctx context.Context, message protocol.JSONObject) error {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			return nil
		}
		return r.HandleServerMessage(id, message)
	}
}
func (r *TelegramRuntime) ClearChat(id string) {
	r.Delivery.Clear(id)
	chat := r.chat(id)
	chat.mu.Lock()
	chat.thinking = nil
	chat.state.State = "idle"
	chat.state.Verb = ""
	chat.state.PendingPermissionCount = 0
	chat.state.Pending = nil
	chat.watcher = ImageWatcher{}
	chat.mu.Unlock()
}
func (r *TelegramRuntime) existing(id string) (SessionRestore, error) {
	return RestoreSession(r.ctx, id, r.Store, r.Client, r.Bridge, r.handler(id), func() { r.ClearChat(id) })
}
func (r *TelegramRuntime) create(id, work string) (bool, error) {
	r.Bridge.Reset(id)
	session, err := r.Client.CreateSession(r.ctx, work)
	if err != nil {
		return false, r.notice(id, "❌ 无法创建会话: "+err.Error())
	}
	if err = r.Store.Set(id, session, work); err != nil {
		return false, r.notice(id, "❌ 无法创建会话: "+err.Error())
	}
	r.Bridge.Connect(id, session, r.handler(id))
	if !r.Bridge.WaitOpen(r.ctx, id) {
		return false, r.notice(id, "⚠️ 连接服务器超时，请重试。")
	}
	return true, nil
}
func (r *TelegramRuntime) ensure(id string) (bool, error) {
	result, err := r.existing(id)
	if err != nil {
		return false, err
	}
	if result.Status == "restored" {
		return true, nil
	}
	if result.Status == "unavailable" {
		return false, r.notice(id, SessionReconnectNotice)
	}
	if r.options.Config.WorkDir != "" {
		return r.create(id, r.options.Config.WorkDir)
	}
	return false, r.showProjects(id)
}
func jsonID(value json.RawMessage) string {
	quoted, _ := protocol.JavaScriptStringJSON(value)
	return TextJSON(quoted).String()
}
func (r *TelegramRuntime) HandleUpdate(ctx context.Context, update protocol.JSONObject) error {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil
	}
	defer done(nil)
	if ctx.Err() != nil || r.ctx.Err() != nil {
		return nil
	}
	if callback := update.Get("callback_query"); len(callback) > 0 && string(callback) != "null" {
		return r.handleCallback(object(callback))
	}
	message := object(update.Get("message"))
	if len(message.OwnProperties()) == 0 {
		return nil
	}
	chat := object(message.Get("chat"))
	from := object(message.Get("from"))
	id := jsonID(chat.Get("id"))
	text := TextJSON(message.Get("text"))
	command, query := r.command(message, text)
	if command != "" {
		switch command {
		case "start", "help":
			return r.notice(id, TelegramHelp())
		case "provider", "model", "skills", "stop", "status", "clear":
			if chat.String("type") != "private" || len(from.OwnProperties()) == 0 {
				return nil
			}
			if !r.Pairing.Allowed("telegram", from.Get("id")) {
				return r.notice(id, "🔒 未授权。请在 Claude Code 桌面端生成配对码后发送给我。")
			}
			return r.extendedCommand(id, command, query)
		case "new", "projects", "sessions", "resume":
			text = NewText("/" + command)
			if trimmed := protocol.TrimJavaScriptString(query); trimmed != "" {
				text = text.Join(NewText(" " + trimmed))
			}
		case "allow", "always", "allow-always", "deny":
			text = NewText("/" + command)
			if query != "" {
				text = text.Join(NewText(" " + query))
			}
		}
	}
	attachments := []protocol.JSONObject{}
	hasMedia := false
	for _, key := range []string{"photo", "document", "video", "audio", "voice"} {
		hasMedia = hasMedia || truthy(message.Get(key))
	}
	if len(message.Get("text")) == 0 && hasMedia {
		text = TextJSON(message.Get("caption"))
		rejections := []string{}
		session := id
		if stored := r.Store.Get(id); stored != nil {
			session = stored.SessionID
		}
		run := func(file protocol.JSONObject, name, mime *string) {
			attachment, err := r.Media.Download(r.ctx, file.String("file_id"), session, name, mime)
			if err != nil {
				rejections = append(rejections, "📎 附件下载失败,请稍后重试")
				return
			}
			limit := CheckAttachmentLimit(attachment.Kind, attachment.Size, attachment.MIME)
			if !limit.OK {
				rejections = append(rejections, limit.Hint)
				return
			}
			attachments = append(attachments, AttachmentRef(attachment))
		}
		photos := collectJSONArray(message.Get("photo"))
		if len(photos) > 0 {
			photo := photos[len(photos)-1]
			name, mime := "photo-"+photo.String("file_unique_id")+".jpg", "image/jpeg"
			run(photo, &name, &mime)
		}
		for _, key := range []string{"document", "video", "audio", "voice"} {
			if !truthy(message.Get(key)) {
				continue
			}
			file := object(message.Get(key))
			var name, mime *string
			if v := file.Get("file_name"); len(v) > 0 && string(v) != "null" {
				text := TextJSON(v).String()
				name = &text
			}
			if v := file.Get("mime_type"); len(v) > 0 && string(v) != "null" {
				text := TextJSON(v).String()
				mime = &text
			}
			if key == "voice" {
				text := "voice-" + file.String("file_unique_id") + ".ogg"
				name = &text
				if mime == nil {
					m := "audio/ogg"
					mime = &m
				}
			}
			run(file, name, mime)
		}
		for _, rejection := range rejections {
			_ = r.notice(id, rejection)
		}
		if len(attachments) == 0 && len(text.Trim()) == 0 {
			return nil
		}
	}
	if len(from.OwnProperties()) == 0 || chat.String("type") != "private" {
		return nil
	}
	if len(message.Get("message_id")) == 0 || !r.Dedup.TryRecord("telegram:"+id+":"+jsonID(message.Get("message_id"))) {
		return nil
	}
	if !r.Pairing.Allowed("telegram", from.Get("id")) {
		name := strings.TrimSpace(from.String("first_name") + " " + from.String("last_name"))
		paired, err := r.Pairing.TryPair("telegram", text.Trim().String(), name, from.Get("id"))
		if err != nil {
			return err
		}
		if paired {
			return r.notice(id, "✅ 配对成功！现在可以开始聊天了。\n\n发送消息即可与 Claude 对话。")
		}
		return r.notice(id, "🔒 未授权。请在 Claude Code 桌面端生成配对码后发送给我。")
	}
	c := r.chat(id)
	c.queue.Lock()
	defer c.queue.Unlock()
	c.mu.Lock()
	pending := append([]string{}, c.state.Pending...)
	c.mu.Unlock()
	if !hasMedia && len(attachments) == 0 {
		if decision := ParsePermissionCommand(text.String(), pending); decision != nil {
			return r.permission(id, *decision)
		}
	}
	if !hasMedia && len(attachments) == 0 {
		trimmed := text.Trim().String()
		if trimmed == "/new" || strings.HasPrefix(trimmed, "/new ") || strings.HasPrefix(trimmed, "/new\n") {
			return r.startNew(id, protocol.TrimJavaScriptString(strings.TrimPrefix(trimmed, "/new")))
		}
		if trimmed == "/projects" {
			return r.showProjects(id)
		}
		if trimmed == "/resume" {
			return r.showResumeProjects(id)
		}
		if handled, err := c.sessionSelection.Handle(r.ctx, id, text.String()); handled || err != nil {
			return err
		}
		if c.projectSelection {
			if trimmed != "" {
				return r.startNew(id, trimmed)
			}
			return nil
		}
	}
	ready, err := r.ensure(id)
	if err != nil || !ready {
		return err
	}
	effective := text
	if len(effective) == 0 && len(attachments) > 0 {
		effective = NewText("(用户发送了附件)")
	}
	if len(effective) == 0 && len(attachments) == 0 {
		return nil
	}
	packet := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user_message")}, protocol.JSONField{Name: "content", Value: raw(effective)})
	if len(attachments) > 0 {
		packet.Set("attachments", raw(attachments))
	}
	c.mu.Lock()
	sent := r.Bridge.Send(id, packet)
	if sent {
		c.state.State = "thinking"
	}
	c.mu.Unlock()
	if !sent {
		return r.notice(id, "⚠️ 消息发送失败，连接可能已断开。请发送 /new 重新开始。")
	}
	return nil
}
func (r *TelegramRuntime) command(message protocol.JSONObject, text Text) (string, string) {
	for _, entity := range collectJSONArray(message.Get("entities")) {
		if entity.String("type") != "bot_command" || string(entity.Get("offset")) != "0" {
			continue
		}
		var length int
		_ = json.Unmarshal(entity.Get("length"), &length)
		parts := strings.Split(strings.TrimPrefix(text.Slice(0, length).String(), "/"), "@")
		if len(parts) > 1 && !strings.EqualFold(parts[1], r.Poller.Username()) {
			return "", ""
		}
		switch parts[0] {
		case "start", "help", "provider", "model", "skills", "new", "projects", "sessions", "resume", "stop", "status", "clear", "allow", "always", "allow-always", "deny":
			return parts[0], text.Slice(length, len(text)).TrimStart().String()
		}
	}
	return "", ""
}
func (r *TelegramRuntime) permission(id string, decision PermissionDecision) error {
	c := r.chat(id)
	c.mu.Lock()
	found := false
	for _, request := range c.state.Pending {
		found = found || request == decision.RequestID
	}
	c.mu.Unlock()
	if !found {
		return r.notice(id, "未找到待确认的权限请求："+decision.RequestID)
	}
	sent := r.sendPermission(id, decision)
	if sent {
		c.mu.Lock()
		removePending(&c.state, decision.RequestID)
		c.mu.Unlock()
		return r.notice(id, PermissionDecisionStatus(decision)+"。")
	}
	return r.notice(id, "权限响应发送失败，请检查会话状态。")
}
func removePending(state *ChatState, id string) {
	for i, v := range state.Pending {
		if v == id {
			state.Pending = append(state.Pending[:i], state.Pending[i+1:]...)
			break
		}
	}
	if state.PendingPermissionCount > 0 {
		state.PendingPermissionCount--
	}
}
func (r *TelegramRuntime) sendPermission(id string, decision PermissionDecision) bool {
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("permission_response")}, protocol.JSONField{Name: "requestId", Value: raw(decision.RequestID)}, protocol.JSONField{Name: "allowed", Value: raw(decision.Allowed)})
	if decision.Rule != "" {
		message.Set("rule", raw(decision.Rule))
	}
	return r.Bridge.Send(id, message)
}

var invalidThinkingSignature = regexp.MustCompile(`(?i)Invalid.*signature.*thinking`)

func (r *TelegramRuntime) HandleServerMessage(id string, message protocol.JSONObject) error {
	c := r.chat(id)
	c.mu.Lock()
	if c.state.SyncPermission(message) {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	switch message.String("type") {
	case "status":
		c.mu.Lock()
		c.state.State = message.String("state")
		c.state.Verb = message.String("verb")
		state := c.state.State
		c.mu.Unlock()
		if state == "thinking" && !r.Delivery.HasState(id) {
			if _, err := r.Delivery.EnsurePlaceholder(id, NewText("💭 思考中...")); err != nil {
				return err
			}
			c.mu.Lock()
			c.thinking = Text{}
			c.mu.Unlock()
		}
	case "content_start":
		switch message.String("blockType") {
		case "text":
			c.mu.Lock()
			c.thinking = nil
			c.mu.Unlock()
			return r.Delivery.Handle(id, "content_start", "text", nil)
		case "tool_use":
			return r.Delivery.Complete(id)
		}
	case "content_delta":
		text := TextJSON(message.Get("text"))
		if len(text) > 0 {
			c.mu.Lock()
			c.thinking = nil
			uploads := c.watcher.Feed(text)
			c.mu.Unlock()
			if err := r.Delivery.Append(id, text); err != nil {
				return err
			}
			for _, pending := range uploads {
				pending := pending
				r.launch(func() {
					work := ""
					if session := r.Store.Get(id); session != nil {
						work = session.WorkDir
					}
					image := LoadSafeOutboundImage(pending, work, r.options.Config.ConfigDir)
					if image.OK {
						if err := r.API.SendPhoto(r.ctx, id, image.Buffer, pending.Alt); err != nil && r.ctx.Err() == nil {
							r.options.Log("[Telegram] dispatchOutboundMedia failed: " + err.Error())
						}
					} else {
						r.options.Log("[Telegram] Outbound image rejected: " + image.Reason)
					}
				})
			}
		}
	case "thinking":
		if placeholder := r.Delivery.Placeholder(id); placeholder != nil {
			c.mu.Lock()
			update := TelegramThinking(c.thinking, TextJSON(message.Get("text")), 1000)
			c.thinking = update.FullText
			c.mu.Unlock()
			_ = r.API.EditMessage(r.ctx, id, *placeholder, update.MessageText)
		}
	case "permission_request":
		request := message.String("requestId")
		c.mu.Lock()
		c.state.PendingPermissionCount++
		c.state.State = "permission_pending"
		found := false
		for _, old := range c.state.Pending {
			found = found || old == request
		}
		if !found {
			c.state.Pending = append(c.state.Pending, request)
		}
		c.mu.Unlock()
		text := NewText("🔐 需要权限确认 [" + request + "]\n工具: " + message.String("toolName") + "\n").Join(PreviewInput(message.Get("input"), 300)).Join(NewText("\n\n" + PermissionInstructions(request)))
		keyboard := map[string]any{"inline_keyboard": []any{[]any{map[string]string{"text": "✅ 允许", "callback_data": "permit:" + request + ":yes"}, map[string]string{"text": "♾️ 永久允许", "callback_data": "permit:" + request + ":always"}}, []any{map[string]string{"text": "❌ 拒绝", "callback_data": "permit:" + request + ":no"}}}}
		_, err := r.API.SendOptions(r.ctx, id, text, protocol.NewJSONObject(protocol.JSONField{Name: "reply_markup", Value: raw(keyboard)}))
		return err
	case "message_complete":
		c.mu.Lock()
		c.state.State = "idle"
		c.state.Verb = ""
		c.mu.Unlock()
		err := r.Delivery.Complete(id)
		c.mu.Lock()
		c.thinking = nil
		c.mu.Unlock()
		return err
	case "error":
		c.mu.Lock()
		c.state.State = "idle"
		c.state.Verb = ""
		c.thinking = nil
		c.mu.Unlock()
		msg := message.String("message")
		if invalidThinkingSignature.MatchString(msg) {
			work := r.options.Config.WorkDir
			if stored := r.Store.Get(id); stored != nil && stored.WorkDir != "" {
				work = stored.WorkDir
			}
			if work == "" {
				return r.notice(id, "⚠️ 会话上下文已失效，请发送 /new 新建会话。")
			}
			if err := r.notice(id, "⚠️ 会话上下文已失效，正在自动重建..."); err != nil {
				return err
			}
			r.ClearChat(id)
			r.Bridge.Reset(id)
			_ = r.Store.Delete(id)
			ok, err := r.create(id, work)
			if err != nil {
				return err
			}
			if ok {
				return r.notice(id, "✅ 已重建会话，请重新发送消息。")
			}
			return r.notice(id, "❌ 重建会话失败，请发送 /new 手动新建。")
		}
		return r.notice(id, "❌ "+msg)
	case "system_notification":
		if message.String("subtype") == "init" {
			model := object(message.Get("data")).String("model")
			if protocol.TrimJavaScriptString(model) != "" {
				c.mu.Lock()
				c.state.Model = model
				c.mu.Unlock()
			}
		}
	}
	return nil
}
func (r *TelegramRuntime) showProjects(id string) error {
	c := r.chat(id)
	c.sessionSelection.Clear(id)
	c.selection = nil
	c.projectSelection = false
	projects, err := r.Client.Projects(r.ctx)
	if err != nil {
		return r.notice(id, "❌ 无法获取项目列表: "+err.Error())
	}
	if len(projects) == 0 {
		return r.notice(id, "没有找到最近的项目。发送 /new 会使用默认工作目录："+r.options.Config.WorkDir+"\n也可以发送 /new /path/to/project 指定项目。")
	}
	lines := []string{}
	for i, project := range projects {
		if i >= 10 {
			break
		}
		branch := ""
		if project.String("branch") != "" {
			branch = " (" + project.String("branch") + ")"
		}
		lines = append(lines, fmt.Sprintf("%d. %s%s\n   %s", i+1, project.String("projectName"), branch, project.String("realPath")))
	}
	c.projectSelection = true
	return r.notice(id, "选择项目（回复编号）：\n\n"+strings.Join(lines, "\n\n")+"\n\n💡 下次可直接 /new <编号、名称或绝对路径> 快速新建会话")
}
func (r *TelegramRuntime) startNew(id, query string) error {
	r.Bridge.Reset(id)
	_ = r.Store.Delete(id)
	r.ClearChat(id)
	c := r.chat(id)
	c.projectSelection = false
	c.sessionSelection.Clear(id)
	c.selection = nil
	c.mu.Lock()
	c.state = ChatState{State: "idle"}
	c.mu.Unlock()
	if query != "" {
		project, ambiguous, err := r.Client.MatchProject(r.ctx, query)
		if err != nil {
			return r.notice(id, "❌ "+err.Error())
		}
		if project != nil {
			ok, err := r.create(id, project.String("realPath"))
			if err != nil || !ok {
				return err
			}
			branch := ""
			if project.String("branch") != "" {
				branch = " (" + project.String("branch") + ")"
			}
			return r.notice(id, "✅ 已新建会话："+project.String("projectName")+branch)
		}
		if len(ambiguous) > 0 {
			lines := []string{}
			for i, p := range ambiguous {
				lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, p.String("projectName"), p.String("realPath")))
			}
			return r.notice(id, "匹配到多个项目，请更精确：\n\n"+strings.Join(lines, "\n"))
		}
		return r.notice(id, "未找到匹配 \""+query+"\" 的项目。发送 /projects 查看完整列表。")
	}
	if r.options.Config.WorkDir != "" {
		ok, err := r.create(id, r.options.Config.WorkDir)
		if err != nil || !ok {
			return err
		}
		return r.notice(id, "✅ 已新建会话，可以开始对话了。")
	}
	return r.showProjects(id)
}
