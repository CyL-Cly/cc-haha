package imruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ChatPort is the platform presentation boundary in common/chat-runtime.ts.
// Telegram keeps its original dedicated handler; adapters using ImChatRuntime
// share this routing, approval, recovery and stream consumer.
type ResponseStream interface {
	Append(Text) error
	Finish() error
}
type OutboundImage struct {
	Buffer    []byte
	MIME, Alt string
}
type ChatPort struct {
	Platform, LogPrefix string
	SendNotice          func(string, string) error
	CreateResponse      func(string) ResponseStream
	SendImage           func(string, OutboundImage) error
	SetBusy             func(string, bool)
	ClearChat           func(string)
}
type InboundChatMessage struct {
	ChatID, DisplayName, DedupKey, Text string
	TextValue                           *Text
	UserID                              json.RawMessage
	Attachments                         []protocol.JSONObject
	LoadAttachments                     func() ([]protocol.JSONObject, error)
	HasAttachments                      *bool
	// authorized is set only by a platform's admission path after pairing.
	authorized bool
}
type ChatRuntimeOptions struct {
	ServerMessage  func(string, protocol.JSONObject) error
	Config         Config
	Port           ChatPort
	Bridge         *Bridge
	Store          *SessionStore
	Client         *HTTPClient
	Pairing        *Pairing
	Dedup          *MessageDedup
	FlushInterval  time.Duration
	FlushThreshold int
	Log            func(string)
	Now            func() time.Time
}
type runtimeChat struct {
	mu               sync.Mutex
	tail             chan struct{}
	state            ChatState
	buffer           *MessageBuffer
	response         ResponseStream
	watcher          ImageWatcher
	selection        *SessionSelection
	projectSelection bool
}
type ChatRuntime struct {
	o       ChatRuntimeOptions
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	chats   map[string]*runtimeChat
	retired []*MessageBuffer
	closed  bool
	wg      sync.WaitGroup
	once    sync.Once
}

func NewChatRuntime(ctx context.Context, o ChatRuntimeOptions) *ChatRuntime {
	if o.FlushInterval == 0 {
		o.FlushInterval = 800 * time.Millisecond
	}
	if o.FlushThreshold == 0 {
		o.FlushThreshold = 320
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &ChatRuntime{o: o, ctx: ctx, cancel: cancel, chats: map[string]*runtimeChat{}}
}
func (r *ChatRuntime) chat(id string) *runtimeChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.chats[id]; c != nil {
		return c
	}
	c := &runtimeChat{state: ChatState{State: "idle"}}
	c.selection = NewSessionSelection(SessionSelectionDeps{Client: r.o.Client, Bridge: r.o.Bridge, Store: r.o.Store, Handler: r.handler, Clear: r.ClearChat, Busy: r.Busy, Send: r.notice, ClearProjects: func(id string) { c := r.chat(id); c.mu.Lock(); c.projectSelection = false; c.mu.Unlock() }, Now: r.o.Now})
	r.chats[id] = c
	return c
}
func (r *ChatRuntime) launch(fn func()) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() { defer r.wg.Done(); fn() }()
	return true
}
func (r *ChatRuntime) notice(id, text string) error { return r.o.Port.SendNotice(id, text) }
func (r *ChatRuntime) busy(id string, v bool) {
	if r.o.Port.SetBusy != nil {
		r.o.Port.SetBusy(id, v)
	}
}
func (r *ChatRuntime) State(id string) ChatState {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Pending = append([]string{}, s.Pending...)
	return s
}
func (r *ChatRuntime) Busy(id string) bool {
	s := r.State(id)
	return s.State != "idle" || len(s.Pending) > 0
}
func (r *ChatRuntime) handler(id string) BridgeHandler {
	return func(ctx context.Context, m protocol.JSONObject) error {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			return nil
		}
		if r.o.ServerMessage != nil {
			return r.o.ServerMessage(id, m)
		}
		return r.HandleServerMessage(id, m)
	}
}
func (r *ChatRuntime) HandleInbound(m InboundChatMessage) error {
	previous, done, accepted := r.admitInbound(&m)
	if !accepted {
		return nil
	}
	return r.handleAdmittedInbound(m, previous, done)
}

// QueueInbound admits synchronously, then performs the original per-chat FIFO
// task without blocking the platform's update cursor/next message admission.
func (r *ChatRuntime) QueueInbound(m InboundChatMessage) {
	previous, done, accepted := r.admitInbound(&m)
	if !accepted {
		return
	}
	if !r.launch(func() { _ = r.handleAdmittedInbound(m, previous, done) }) {
		close(done)
	}
}
func (r *ChatRuntime) admitInbound(m *InboundChatMessage) (<-chan struct{}, chan struct{}, bool) {
	if m.ChatID == "" || r.ctx.Err() != nil {
		return nil, nil, false
	}
	if m.DedupKey != "" && !r.o.Dedup.TryRecord(r.o.Port.Platform+":"+m.DedupKey) {
		return nil, nil, false
	}
	m.Text = protocol.TrimJavaScriptString(m.Text)
	if m.TextValue != nil {
		text := m.TextValue.Trim()
		m.TextValue = &text
		m.Text = text.String()
	}
	has := len(m.Attachments) > 0 || m.LoadAttachments != nil
	if m.HasAttachments != nil {
		has = *m.HasAttachments
	}
	if m.Text == "" && !has {
		return nil, nil, false
	}
	c := r.chat(m.ChatID)
	c.mu.Lock()
	previous := c.tail
	done := make(chan struct{})
	c.tail = done
	c.mu.Unlock()
	return previous, done, true
}
func (r *ChatRuntime) handleAdmittedInbound(m InboundChatMessage, previous <-chan struct{}, done chan struct{}) error {
	defer close(done)
	if previous != nil {
		select {
		case <-previous:
		case <-r.ctx.Done():
			return nil
		}
	}
	if r.ctx.Err() != nil {
		return nil
	}
	has := len(m.Attachments) > 0 || m.LoadAttachments != nil
	if m.HasAttachments != nil {
		has = *m.HasAttachments
	}
	if err := r.route(m, has); err != nil {
		r.busy(m.ChatID, false)
		detail := r.errorDetail(err)
		r.o.Log(r.o.Port.LogPrefix + " Failed to handle message for " + m.ChatID + ": " + detail)
		if noticeErr := r.notice(m.ChatID, "处理消息失败："+detail); noticeErr != nil {
			r.o.Log(r.o.Port.LogPrefix + " Failed to deliver notice: " + noticeErr.Error())
		}
	}
	return nil
}
func (r *ChatRuntime) errorDetail(err error) string {
	if r.o.Port.Platform == "wecom" {
		var ack *WecomAckError
		if errors.As(err, &ack) {
			return "[object Object]"
		}
	}
	return err.Error()
}
func (r *ChatRuntime) route(m InboundChatMessage, has bool) error {
	id := m.ChatID
	c := r.chat(id)
	if !m.authorized && !r.o.Pairing.Allowed(r.o.Port.Platform, m.UserID) {
		paired := false
		var err error
		if m.Text != "" {
			paired, err = r.o.Pairing.TryPair(r.o.Port.Platform, m.Text, m.DisplayName, m.UserID)
			if err != nil {
				return err
			}
		}
		if paired {
			return r.notice(id, "配对成功！现在可以开始聊天了。\n\n发送消息即可与 Claude 对话，发送 /help 查看可用命令。")
		}
		return r.notice(id, "未授权。请在 Claude Code 桌面端生成配对码后发送给我。")
	}
	if !has {
		handled, err := r.command(id, m.Text)
		if handled || err != nil {
			return err
		}
		if decision := ParsePermissionCommand(m.Text, r.State(id).Pending); decision != nil {
			return r.permission(id, *decision)
		}
		if handled, err := c.selection.Handle(r.ctx, id, m.Text); handled || err != nil {
			return err
		}
		c.mu.Lock()
		project := c.projectSelection
		c.mu.Unlock()
		if project {
			if m.Text != "" {
				return r.StartNew(id, m.Text)
			}
			return nil
		}
	}
	ready, err := r.ensure(id)
	if err != nil || !ready {
		return err
	}
	attachments := m.Attachments
	if attachments == nil && m.LoadAttachments != nil {
		attachments, err = m.LoadAttachments()
		if err != nil {
			return err
		}
	}
	text := m.Text
	textValue := NewText(text)
	if m.TextValue != nil {
		textValue = m.TextValue.Clone()
	}
	if text == "" && len(attachments) > 0 {
		text = "(用户发送了附件)"
		textValue = NewText(text)
	}
	if text == "" && len(attachments) == 0 {
		return nil
	}
	r.busy(id, true)
	c.mu.Lock()
	if !r.wechatProfile() {
		c.state.State = "thinking"
	}
	var attachmentJSON json.RawMessage
	if len(attachments) > 0 {
		attachmentJSON = raw(attachments)
	}
	sent := r.o.Bridge.SendUserText(id, textValue, attachmentJSON)
	if sent && r.wechatProfile() {
		c.state.State = "thinking"
	}
	if !sent && !r.wechatProfile() {
		c.state.State = "idle"
	}
	c.mu.Unlock()
	if !sent {
		if !r.wechatProfile() {
			r.busy(id, false)
		}
		return r.notice(id, "消息发送失败，连接可能已断开。请发送 /new 重新开始。")
	}
	return nil
}
func IMHelp() string {
	return "可用命令：\n\n/new [项目] / 新会话 — 新建会话或切换项目\n/projects / 项目列表 — 查看最近项目\n/sessions [项目] / 会话列表 — 查看当前项目的旧会话\n/sessions projects — 选择其他项目的旧会话\n/resume <编号> / 继续会话 <编号> — 恢复列表中的会话\n/sessions next / /sessions prev — 翻页\n/cancel / 取消选择 — 退出选择，保留当前会话\n/status / 状态 — 查看当前会话状态\n/clear / 清空 — 清空当前会话上下文\n/stop / 停止 — 停止当前生成\n/help / 帮助 — 显示这份帮助\n权限审批：/allow <id>、/always <id>、/deny <id>"
}
func (r *ChatRuntime) command(id, text string) (bool, error) {
	switch text {
	case "/help", "帮助":
		return true, r.notice(id, IMHelp())
	case "/status", "状态":
		s, err := r.Status(id)
		if err != nil {
			return true, err
		}
		return true, r.notice(id, s)
	case "/projects", "项目列表":
		return true, r.ShowProjects(id)
	case "/stop", "停止", "/clear", "清空":
		restored, err := r.Existing(id)
		if err != nil {
			return true, err
		}
		if restored.Status != "restored" {
			notice := emptyStatus
			if restored.Status == "unavailable" {
				notice = SessionReconnectNotice
			}
			return true, r.notice(id, notice)
		}
		if text == "/stop" || text == "停止" {
			r.o.Bridge.Send(id, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("stop_generation")}))
			return true, r.notice(id, "已发送停止信号。")
		}
		r.ClearChat(id)
		if r.o.Bridge.SendUser(id, "/clear", nil) {
			c := r.chat(id)
			c.mu.Lock()
			c.state.State = "thinking"
			c.mu.Unlock()
			return true, r.notice(id, "已清空当前会话上下文。")
		}
		return true, r.notice(id, "无法发送 /clear，请先发送 /new 重新连接会话。")
	}
	if text == "/new" || text == "新会话" || strings.HasPrefix(text, "/new ") || !r.wechatProfile() && strings.HasPrefix(text, "新会话 ") {
		query := ""
		if strings.HasPrefix(text, "/new ") {
			query = protocol.TrimJavaScriptString(text[len("/new "):])
		} else if strings.HasPrefix(text, "新会话 ") {
			query = protocol.TrimJavaScriptString(text[len("新会话 "):])
		}
		return true, r.StartNew(id, query)
	}
	return false, nil
}
func (r *ChatRuntime) permission(id string, d PermissionDecision) error {
	c := r.chat(id)
	c.mu.Lock()
	found := false
	for _, v := range c.state.Pending {
		found = found || v == d.RequestID
	}
	if !found {
		c.mu.Unlock()
		return r.notice(id, "未找到待确认的权限请求："+d.RequestID)
	}
	m := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("permission_response")}, protocol.JSONField{Name: "requestId", Value: raw(d.RequestID)}, protocol.JSONField{Name: "allowed", Value: raw(d.Allowed)})
	if d.Rule != "" {
		m.Set("rule", raw(d.Rule))
	}
	sent := r.o.Bridge.Send(id, m)
	if sent {
		for i, v := range c.state.Pending {
			if v == d.RequestID {
				c.state.Pending = append(c.state.Pending[:i], c.state.Pending[i+1:]...)
				break
			}
		}
		if c.state.PendingPermissionCount > 0 {
			c.state.PendingPermissionCount--
		}
	}
	c.mu.Unlock()
	if sent {
		return r.notice(id, PermissionDecisionStatus(d)+"。")
	}
	return r.notice(id, "权限响应发送失败，请检查会话状态。")
}
func (r *ChatRuntime) getResponse(id string) ResponseStream {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.response == nil {
		c.response = r.o.Port.CreateResponse(id)
	}
	return c.response
}
func (r *ChatRuntime) getBuffer(id string) *MessageBuffer {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buffer == nil {
		c.buffer = NewMessageBuffer(func(text Text, _ bool) error {
			if len(text) == 0 || r.wechatProfile() && len(text.Trim()) == 0 {
				return nil
			}
			return r.getResponse(id).Append(text)
		}, r.o.FlushInterval, r.o.FlushThreshold, func(err error) { r.o.Log(r.o.Port.LogPrefix + " Flush failed: " + err.Error()) })
	}
	return c.buffer
}
func (r *ChatRuntime) detachResponse(id string) ResponseStream {
	c := r.chat(id)
	c.mu.Lock()
	response := c.response
	c.response = nil
	c.mu.Unlock()
	return response
}
func (r *ChatRuntime) closeResponse(response ResponseStream) {
	if response != nil {
		if err := response.Finish(); err != nil {
			r.o.Log(r.o.Port.LogPrefix + " Failed to finish response: " + err.Error())
		}
	}
}
func (r *ChatRuntime) retire(b *MessageBuffer) {
	if b != nil {
		r.mu.Lock()
		r.retired = append(r.retired, b)
		r.mu.Unlock()
	}
}
func (r *ChatRuntime) finish(id string, discard bool) {
	c := r.chat(id)
	c.mu.Lock()
	b := c.buffer
	c.mu.Unlock()
	if b != nil {
		if discard {
			b.Reset()
		} else {
			b.Complete()
		}
	}
	c.mu.Lock()
	if c.buffer == b {
		c.buffer = nil
	}
	c.mu.Unlock()
	r.retire(b)
	r.closeResponse(r.detachResponse(id))
}
func (r *ChatRuntime) ClearChat(id string) {
	c := r.chat(id)
	c.mu.Lock()
	b := c.buffer
	response := c.response
	c.buffer = nil
	c.response = nil
	c.watcher = ImageWatcher{}
	c.state.State = "idle"
	c.state.Verb = ""
	c.state.Pending = nil
	c.state.PendingPermissionCount = 0
	c.mu.Unlock()
	if b != nil {
		b.Reset()
		r.retire(b)
	}
	if response != nil {
		if !r.launch(func() { r.closeResponse(response) }) {
			r.closeResponse(response)
		}
	}
	r.busy(id, false)
	if r.o.Port.ClearChat != nil {
		r.o.Port.ClearChat(id)
	}
}
func (r *ChatRuntime) HandleServerMessage(id string, m protocol.JSONObject) error {
	c := r.chat(id)
	c.mu.Lock()
	if c.state.SyncPermission(m) {
		c.mu.Unlock()
		return nil
	}
	kind := m.String("type")
	switch kind {
	case "status":
		c.state.State = m.String("state")
		c.state.Verb = m.String("verb")
	case "content_start":
		if m.String("blockType") == "text" {
			c.state.State = "streaming"
		} else if m.String("blockType") == "tool_use" {
			c.state.State = "tool_executing"
			if isString(m.Get("toolName")) {
				c.state.Verb = m.String("toolName")
			}
		}
	case "tool_use_complete":
		c.state.State = "tool_executing"
		if isString(m.Get("toolName")) {
			c.state.Verb = m.String("toolName")
		}
	case "tool_result":
		c.state.State = "thinking"
		c.state.Verb = ""
	case "permission_request":
		c.state.PendingPermissionCount++
		c.state.State = "permission_pending"
		id := m.String("requestId")
		found := false
		for _, v := range c.state.Pending {
			found = found || v == id
		}
		if !found {
			c.state.Pending = append(c.state.Pending, id)
		}
	case "message_complete", "error":
		c.state.State = "idle"
		c.state.Verb = ""
	case "system_notification":
		if m.String("subtype") == "init" {
			model := object(m.Get("data")).String("model")
			if protocol.TrimJavaScriptString(model) != "" {
				c.state.Model = model
			}
		}
	}
	state := c.state.State
	c.mu.Unlock()
	switch kind {
	case "status":
		if state == "thinking" || state == "tool_executing" {
			r.busy(id, true)
		} else if state == "idle" {
			r.busy(id, false)
		}
	case "content_start":
		if m.String("blockType") == "tool_use" {
			r.busy(id, true)
		}
	case "tool_use_complete", "tool_result":
		r.busy(id, true)
	case "content_delta":
		if isString(m.Get("text")) && m.String("text") != "" {
			text := TextJSON(m.Get("text"))
			r.getBuffer(id).Append(text)
			if r.o.Port.SendImage != nil {
				c.mu.Lock()
				pending := c.watcher.Feed(text)
				c.mu.Unlock()
				for _, p := range pending {
					r.launch(func() { r.dispatchImage(id, p) })
				}
			}
		}
	case "permission_request":
		r.busy(id, false)
		if !r.wechatProfile() {
			r.finish(id, false)
		}
		return r.notice(id, "🔐 需要权限确认 ["+m.String("requestId")+"]\n工具: "+m.String("toolName")+"\n"+PreviewInput(m.Get("input"), 300).String()+"\n\n"+PermissionInstructions(m.String("requestId")))
	case "message_complete":
		r.busy(id, false)
		r.finish(id, false)
	case "error":
		r.busy(id, false)
		r.finish(id, true)
		if r.wechatProfile() {
			return r.notice(id, "错误: "+m.String("message"))
		}
		return r.handleError(id, m.String("message"))
	}
	return nil
}
func (r *ChatRuntime) wechatProfile() bool { return r.o.Port.Platform == "wechat" }
func isString(v json.RawMessage) bool      { return len(v) > 0 && v[0] == '"' }
func (r *ChatRuntime) dispatchImage(id string, p PendingUpload) {
	work := ""
	if s := r.o.Store.Get(id); s != nil {
		work = s.WorkDir
	}
	image := LoadSafeOutboundImage(p, work)
	if !image.OK {
		r.o.Log(r.o.Port.LogPrefix + " Outbound image rejected: " + image.Reason)
		return
	}
	if err := r.o.Port.SendImage(id, OutboundImage{image.Buffer, image.MIME, p.Alt}); err != nil {
		r.o.Log(r.o.Port.LogPrefix + " dispatchOutboundImage failed: " + err.Error())
	}
}

var invalidThinking = regexp.MustCompile(`(?i)Invalid.*signature.*thinking`)

func (r *ChatRuntime) handleError(id, message string) error {
	if invalidThinking.MatchString(message) {
		work := r.o.Config.WorkDir
		if s := r.o.Store.Get(id); s != nil && s.WorkDir != "" {
			work = s.WorkDir
		}
		if work != "" {
			if err := r.notice(id, "会话上下文已失效，正在自动重建..."); err != nil {
				return err
			}
			r.ClearChat(id)
			r.o.Bridge.Reset(id)
			if err := r.o.Store.Delete(id); err != nil {
				return err
			}
			ok, err := r.create(id, work)
			if err != nil {
				return err
			}
			if ok {
				return r.notice(id, "已重建会话，请重新发送消息。")
			}
			return r.notice(id, "重建会话失败，请发送 /new 手动新建。")
		}
		return r.notice(id, "会话上下文已失效，请发送 /new 新建会话。")
	}
	return r.notice(id, "错误: "+message)
}
func (r *ChatRuntime) Existing(id string) (SessionRestore, error) {
	return RestoreSession(r.ctx, id, r.o.Store, r.o.Client, r.o.Bridge, r.handler(id), func() { r.ClearChat(id) })
}
func (r *ChatRuntime) ensure(id string) (bool, error) {
	v, err := r.Existing(id)
	if err != nil {
		return false, err
	}
	if v.Status == "restored" {
		return true, nil
	}
	if v.Status == "unavailable" {
		return false, r.notice(id, SessionReconnectNotice)
	}
	if r.o.Config.WorkDir != "" {
		return r.create(id, r.o.Config.WorkDir)
	}
	return false, r.ShowProjects(id)
}
func (r *ChatRuntime) create(id, work string) (bool, error) {
	if r.wechatProfile() {
		r.chat(id).selection.Clear(id)
	}
	r.o.Bridge.Reset(id)
	r.ClearChat(id)
	session, err := r.o.Client.CreateSession(r.ctx, work)
	if err == nil {
		err = r.o.Store.Set(id, session, work)
	}
	if err != nil {
		return false, r.notice(id, "无法创建会话: "+err.Error())
	}
	r.o.Bridge.Connect(id, session, r.handler(id))
	if !r.o.Bridge.WaitOpen(r.ctx, id) {
		return false, r.notice(id, "连接服务器超时，请重试。")
	}
	return true, nil
}
func (r *ChatRuntime) ShowProjects(id string) error {
	c := r.chat(id)
	c.selection.Clear(id)
	projects, err := r.o.Client.Projects(r.ctx)
	if err != nil {
		return r.notice(id, "无法获取项目列表: "+err.Error())
	}
	if len(projects) == 0 {
		return r.notice(id, "没有找到最近的项目。发送 /new 会使用默认工作目录："+r.o.Config.WorkDir+"\n也可以发送 /new /path/to/project 指定项目。")
	}
	lines := []string{}
	for i, p := range projects {
		if i >= 10 {
			break
		}
		branch := ""
		if p.String("branch") != "" {
			branch = " (" + p.String("branch") + ")"
		}
		lines = append(lines, fmt.Sprintf("%d. %s%s\n   %s", i+1, p.String("projectName"), branch, p.String("realPath")))
	}
	c.mu.Lock()
	c.projectSelection = true
	c.mu.Unlock()
	return r.notice(id, "选择项目（回复编号）：\n\n"+strings.Join(lines, "\n\n")+"\n\n下次可直接 /new <编号、名称或绝对路径> 快速新建会话")
}
func (r *ChatRuntime) StartNew(id, query string) error {
	c := r.chat(id)
	c.selection.Clear(id)
	r.o.Bridge.Reset(id)
	if err := r.o.Store.Delete(id); err != nil {
		return err
	}
	r.ClearChat(id)
	c.mu.Lock()
	c.projectSelection = false
	c.mu.Unlock()
	if query == "" {
		if r.o.Config.WorkDir != "" {
			ok, err := r.create(id, r.o.Config.WorkDir)
			if err != nil || !ok {
				return err
			}
			return r.notice(id, "已新建会话，可以开始对话了。")
		}
		return r.ShowProjects(id)
	}
	p, matches, err := r.o.Client.MatchProject(r.ctx, query)
	if err != nil {
		return r.notice(id, err.Error())
	}
	if p != nil {
		ok, err := r.create(id, p.String("realPath"))
		if err != nil || !ok {
			return err
		}
		branch := ""
		if p.String("branch") != "" {
			branch = " (" + p.String("branch") + ")"
		}
		return r.notice(id, "已新建会话："+p.String("projectName")+branch)
	}
	if len(matches) > 0 {
		lines := []string{}
		for i, p := range matches {
			lines = append(lines, fmt.Sprintf("%d. %s - %s", i+1, p.String("projectName"), p.String("realPath")))
		}
		return r.notice(id, "匹配到多个项目，请更精确：\n\n"+strings.Join(lines, "\n"))
	}
	return r.notice(id, "未找到匹配 \""+query+"\" 的项目。发送 /projects 查看完整列表。")
}
func (r *ChatRuntime) Status(id string) (string, error) {
	v, err := r.Existing(id)
	if err != nil {
		return "", err
	}
	if v.Status == "unavailable" {
		return SessionReconnectNotice, nil
	}
	if v.Status == "missing" {
		return emptyStatus, nil
	}
	s := v.Session
	project := filepath.Base(s.WorkDir)
	branch := ""
	if git, err := r.o.Client.Git(r.ctx, s.SessionID); err == nil {
		if git.String("repoName") != "" {
			project = git.String("repoName")
		} else if git.String("workDir") != "" {
			project = filepath.Base(git.String("workDir"))
		}
		branch = git.String("branch")
	}
	state := r.State(id)
	lines := []string{"当前会话状态："}
	if project != "" {
		if branch != "" {
			project += " (" + branch + ")"
		}
		lines = append(lines, "项目: "+project)
	} else if branch != "" {
		lines = append(lines, "分支: "+branch)
	}
	sid := s.SessionID
	if len(NewText(sid)) > 12 {
		sid = NewText(sid).Slice(0, 8).String() + "…"
	}
	lines = append(lines, "会话: "+sid)
	if state.Model != "" {
		lines = append(lines, "模型: "+state.Model)
	}
	label := map[string]string{"idle": "空闲", "thinking": "思考中", "streaming": "生成中", "tool_executing": "执行工具中", "permission_pending": "等待权限确认"}[state.State]
	if label == "" {
		label = "空闲"
	}
	if state.Verb != "" && state.Verb != "Thinking" {
		label += " (" + state.Verb + ")"
	}
	lines = append(lines, "状态: "+label)
	if state.PendingPermissionCount > 0 {
		lines = append(lines, fmt.Sprintf("审批: %d 个待确认", state.PendingPermissionCount))
	}
	if tasks, err := r.o.Client.Tasks(r.ctx, s.SessionID); err == nil && len(tasks) > 0 {
		counts := map[string]int{}
		for _, task := range tasks {
			counts[task.String("status")]++
		}
		parts := []string{fmt.Sprintf("总计 %d", len(tasks))}
		for _, v := range []struct{ k, l string }{{"in_progress", "进行中"}, {"pending", "待处理"}, {"completed", "已完成"}} {
			if counts[v.k] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", v.l, counts[v.k]))
			}
		}
		lines = append(lines, "任务: "+strings.Join(parts, " · "))
	}
	return strings.Join(lines, "\n"), nil
}
func (r *ChatRuntime) Close() {
	r.once.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		chats := []*runtimeChat{}
		for _, c := range r.chats {
			chats = append(chats, c)
		}
		r.mu.Unlock()
		for _, c := range chats {
			c.mu.Lock()
			tail := c.tail
			c.mu.Unlock()
			if tail != nil {
				<-tail
			}
		}
		r.wg.Wait()
		r.mu.Lock()
		retired := append([]*MessageBuffer{}, r.retired...)
		r.mu.Unlock()
		for _, c := range chats {
			c.mu.Lock()
			b := c.buffer
			c.buffer = nil
			c.mu.Unlock()
			if b != nil {
				b.Close()
			}
			c.mu.Lock()
			response := c.response
			c.response = nil
			c.mu.Unlock()
			r.closeResponse(response)
		}
		for _, b := range retired {
			b.Close()
		}
	})
}
