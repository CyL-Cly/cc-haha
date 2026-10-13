package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type DingTalkRuntimeOptions struct {
	Config        Config
	APIOptions    DingTalkAPIOptions
	StreamOptions DingTalkStreamOptions
	NewAPI        func(context.Context, DingTalkAPIOptions) (*DingTalkAPI, error)
	NewStream     func(context.Context, DingTalkStreamOptions) (*DingTalkStream, error)
	ServerHTTP    *http.Client
	SocketTLS     *tls.Config
	Log           func(string)
	Now           func() time.Time
}

type dingTalkCardFuture struct {
	done chan struct{}
	card *DingTalkCard
	err  error
}
type dingTalkRuntimeChat struct {
	mu            sync.Mutex
	state         ChatState
	tail          chan struct{}
	webhook       string
	target        *protocol.JSONObject
	buffer        *MessageBuffer
	card          *dingTalkCardFuture
	text          Text
	textPresent   bool
	projectPicker bool
}

// DingTalk's original index owns its card and approval state. It uses the same
// server Bridge and session stores, without introducing another Agent loop.
type DingTalkRuntime struct {
	*platformRuntimeCore
	API             *DingTalkAPI
	Stream          *DingTalkStream
	Dispatcher      *DingTalkDispatcher
	mu              sync.Mutex
	chats           map[string]*dingTalkRuntimeChat
	permissionChats map[string]string
	selection       *SessionSelection
	retired         []*MessageBuffer
	closed          bool
	wg              sync.WaitGroup
	stop            sync.Once
}

func NewDingTalkRuntime(ctx context.Context, o DingTalkRuntimeOptions) (*DingTalkRuntime, error) {
	if o.Config.Err != nil {
		return nil, o.Config.Err
	}
	credentials := protocol.NewJSONObject(
		protocol.JSONField{Name: "clientId", Value: PlatformCredential(o.Config, "dingtalk", "clientId", "DINGTALK_CLIENT_ID", "")},
		protocol.JSONField{Name: "clientSecret", Value: PlatformCredential(o.Config, "dingtalk", "clientSecret", "DINGTALK_CLIENT_SECRET", "")},
	)
	if !credentialTruthy(credentials.Get("clientId")) || !credentialTruthy(credentials.Get("clientSecret")) {
		return nil, errors.New("[DingTalk] Missing DINGTALK_CLIENT_ID / DINGTALK_CLIENT_SECRET. Bind with QR auth in Desktop Settings or set env.")
	}
	core, err := newPlatformRuntimeCore(ctx, o.Config, o.ServerHTTP, o.SocketTLS, o.Log, o.Now)
	if err != nil {
		return nil, err
	}
	r := &DingTalkRuntime{platformRuntimeCore: core, chats: map[string]*dingTalkRuntimeChat{}, permissionChats: map[string]string{}}
	core.Attachments = NewDingTalkConfiguredAttachmentStore(o.Config)
	core.Attachments.Lifecycle = core.migration
	core.Attachments.Now = core.Now
	if o.NewAPI == nil {
		o.NewAPI = NewDingTalkAPI
	}
	apiOptions := o.APIOptions
	apiOptions.Credentials, apiOptions.Environment, apiOptions.Now, apiOptions.Log = credentials, o.Config.Environment, core.Now, core.Log
	r.API, err = o.NewAPI(core.ctx, apiOptions)
	if err != nil {
		core.close()
		return nil, err
	}
	r.selection = NewSessionSelection(SessionSelectionDeps{Client: core.ServerClient, Bridge: core.Bridge, Store: core.Store, Handler: r.handler, Clear: r.ClearChat, Busy: r.Busy, Send: r.Notice, Now: core.Now, ClearProjects: func(id string) { c := r.chat(id); c.mu.Lock(); c.projectPicker = false; c.mu.Unlock() }})
	if o.NewStream == nil {
		o.NewStream = NewDingTalkStream
	}
	streamOptions := o.StreamOptions
	streamOptions.API = r.API
	if streamOptions.TLS == nil {
		streamOptions.TLS = core.TLS
	}
	streamOptions.OnRobot = func(ctx context.Context, frame protocol.JSONObject) error {
		return r.Dispatcher.HandleRobot(ctx, frame)
	}
	streamOptions.OnCard = func(ctx context.Context, frame protocol.JSONObject) error { return r.Dispatcher.HandleCard(ctx, frame) }
	r.Stream, err = o.NewStream(core.ctx, streamOptions)
	if err != nil {
		r.API.Close()
		core.close()
		return nil, err
	}
	r.Dispatcher = &DingTalkDispatcher{Stream: r.Stream, Dedup: core.Dedup, RobotAdmission: r.PrepareRobotMessage, CardAdmission: r.PrepareCardCallback}
	return r, nil
}

func (r *DingTalkRuntime) chat(id string) *dingTalkRuntimeChat {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.chats[id]; c != nil {
		return c
	}
	c := &dingTalkRuntimeChat{state: ChatState{State: "idle"}}
	r.chats[id] = c
	return c
}
func (r *DingTalkRuntime) State(id string) ChatState {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Pending = append([]string{}, s.Pending...)
	return s
}
func (r *DingTalkRuntime) Busy(id string) bool {
	s := r.State(id)
	return s.State != "idle" || s.PendingPermissionCount > 0 || len(s.Pending) > 0
}
func (r *DingTalkRuntime) handler(id string) BridgeHandler {
	return func(ctx context.Context, m protocol.JSONObject) error {
		if ctx.Err() != nil || r.ctx.Err() != nil {
			return nil
		}
		return r.HandleServerMessage(id, m)
	}
}
func (r *DingTalkRuntime) Notice(id, text string) error { return r.noticeText(id, NewText(text)) }
func (r *DingTalkRuntime) noticeText(id string, text Text) error {
	c := r.chat(id)
	c.mu.Lock()
	webhook := c.webhook
	c.mu.Unlock()
	if webhook == "" {
		r.Log("[DingTalk] Missing sessionWebhook for " + id + "; cannot send response")
		return nil
	}
	return r.API.SendText(r.ctx, webhook, text)
}
func (r *DingTalkRuntime) retire(b *MessageBuffer) {
	if b != nil {
		r.mu.Lock()
		r.retired = append(r.retired, b)
		r.mu.Unlock()
	}
}
func (r *DingTalkRuntime) getBuffer(id string) *MessageBuffer {
	c := r.chat(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buffer == nil {
		c.buffer = NewMessageBuffer(func(text Text, complete bool) error { return r.flushCard(id, text, complete) }, 1200*time.Millisecond, 200, func(err error) { r.Log("[DingTalk][AICard] flush failed: " + err.Error()) })
	}
	return c.buffer
}
func (r *DingTalkRuntime) getCard(id string) (*DingTalkCard, error) {
	c := r.chat(id)
	c.mu.Lock()
	if c.target == nil {
		c.mu.Unlock()
		return nil, nil
	}
	future := c.card
	if future == nil {
		future = &dingTalkCardFuture{done: make(chan struct{})}
		c.card = future
		target := c.target.Clone()
		c.mu.Unlock()
		future.card, future.err = r.API.CreateCard(r.ctx, target, protocol.NewJSONObject())
		close(future.done)
	} else {
		c.mu.Unlock()
	}
	select {
	case <-future.done:
		return future.card, future.err
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
}
func (r *DingTalkRuntime) flushCard(id string, delta Text, complete bool) error {
	c := r.chat(id)
	c.mu.Lock()
	full := c.text.Join(delta)
	c.text = full
	c.textPresent = true
	c.mu.Unlock()
	if len(full.Trim()) == 0 {
		return nil
	}
	card, err := r.getCard(id)
	if err != nil {
		return err
	}
	if card == nil {
		if complete {
			return r.noticeText(id, full)
		}
		return nil
	}
	if complete {
		err = r.API.FinishCard(r.ctx, card, full)
	} else {
		err = r.API.StreamCard(r.ctx, card, full.Join(NewText(" ▍")), false)
	}
	if err != nil {
		r.Log("[DingTalk][AICard] stream failed, falling back to markdown: " + err.Error())
		c.mu.Lock()
		c.card = nil
		c.mu.Unlock()
		if complete {
			return r.noticeText(id, full)
		}
		return nil
	}
	if complete {
		c.mu.Lock()
		c.card = nil
		c.text = nil
		c.textPresent = false
		b := c.buffer
		c.buffer = nil
		c.mu.Unlock()
		if b != nil {
			b.Reset()
			r.retire(b)
		}
	}
	return nil
}
func (r *DingTalkRuntime) resetStream(id string) {
	c := r.chat(id)
	c.mu.Lock()
	b := c.buffer
	c.buffer = nil
	c.card = nil
	c.text = nil
	c.textPresent = false
	c.mu.Unlock()
	if b != nil {
		b.Reset()
		r.retire(b)
	}
}
func (r *DingTalkRuntime) finishStream(id string) error {
	c := r.chat(id)
	c.mu.Lock()
	b := c.buffer
	c.mu.Unlock()
	if b != nil {
		b.Complete()
	}
	c.mu.Lock()
	finalize := c.card != nil || c.textPresent
	c.mu.Unlock()
	if finalize {
		if err := r.flushCard(id, Text{}, true); err != nil {
			return err
		}
	}
	r.resetStream(id)
	return nil
}
func (r *DingTalkRuntime) ClearChat(id string) {
	r.resetStream(id)
	c := r.chat(id)
	c.mu.Lock()
	pending := append([]string{}, c.state.Pending...)
	c.state.State = "idle"
	c.state.Verb = ""
	c.state.Pending = nil
	c.state.PendingPermissionCount = 0
	c.mu.Unlock()
	r.mu.Lock()
	for _, key := range pending {
		delete(r.permissionChats, key)
	}
	r.mu.Unlock()
}
func (r *DingTalkRuntime) HandleServerMessage(id string, m protocol.JSONObject) error {
	c := r.chat(id)
	c.mu.Lock()
	if c.state.SyncPermission(m) {
		if len(c.state.Pending) == 0 {
			c.state.Pending = nil
		}
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
		}
		if m.String("blockType") == "tool_use" {
			c.state.State = "tool_executing"
		}
	case "tool_use_complete":
		c.state.State = "streaming"
	case "message_complete", "error":
		c.state.State = "idle"
		c.state.Verb = ""
	case "system_notification":
		if m.String("subtype") == "init" {
			model := object(m.Get("data")).Get("model")
			if isString(model) && len(TextJSON(model).Trim()) > 0 {
				c.state.Model = TextJSON(model).String()
			}
		}
	}
	c.mu.Unlock()
	switch kind {
	case "content_delta":
		if isString(m.Get("text")) && len(TextJSON(m.Get("text"))) > 0 {
			r.getBuffer(id).Append(TextJSON(m.Get("text")))
		}
	case "permission_request":
		return r.sendPermissionRequest(id, m)
	case "message_complete":
		return r.finishStream(id)
	case "error":
		c.mu.Lock()
		b := c.buffer
		c.card = nil
		c.text = nil
		c.textPresent = false
		c.mu.Unlock()
		if b != nil {
			b.Reset()
		}
		return r.noticeText(id, NewText("❌ ").Join(dingTalkCoerce(m.Get("message"))))
	}
	return nil
}
func (r *DingTalkRuntime) sendPermissionRequest(id string, m protocol.JSONObject) error {
	c := r.chat(id)
	c.mu.Lock()
	c.state.PendingPermissionCount++
	c.state.State = "permission_pending"
	c.mu.Unlock()
	if err := r.finishStream(id); err != nil {
		return err
	}
	requestID := m.String("requestId")
	c.mu.Lock()
	found := false
	for _, key := range c.state.Pending {
		found = found || key == requestID
	}
	if !found {
		c.state.Pending = append(c.state.Pending, requestID)
	}
	var target *protocol.JSONObject
	if c.target != nil {
		v := c.target.Clone()
		target = &v
	}
	c.mu.Unlock()
	r.mu.Lock()
	r.permissionChats[requestID] = id
	r.mu.Unlock()
	request := NewText("🔐 需要权限确认 [").Join(TextJSON(m.Get("requestId"))).Join(NewText("]\n工具: ")).Join(TextJSON(m.Get("toolName"))).Join(NewText("\n")).Join(PreviewInput(m.Get("input"), 300))
	instructions := NewText(PermissionInstructions(requestID))
	template := PlatformCredential(r.Config, "dingtalk", "permissionCardTemplateId", "DINGTALK_PERMISSION_CARD_TEMPLATE_ID", "")
	if !isString(template) {
		return errors.New("permissionCardTemplateId.trim is not a function")
	}
	if len(TextJSON(template).Trim()) > 0 && target != nil {
		card, err := r.API.CreateCard(r.ctx, *target, protocol.NewJSONObject(protocol.JSONField{Name: "cardTemplateId", Value: raw(TextJSON(template).Trim())}, protocol.JSONField{Name: "outTrackId", Value: raw(NewText("permission_").Join(TextJSON(m.Get("requestId"))))}, protocol.JSONField{Name: "callbackRouteKey", Value: raw("permission")}, protocol.JSONField{Name: "cardParamMap", Value: raw(BuildDingTalkPermissionCardParams(TextJSON(m.Get("toolName")), m.Get("input"), TextJSON(m.Get("requestId"))))}))
		if err != nil {
			return err
		}
		if card != nil {
			return r.noticeText(id, request.Join(NewText("\n\n已发送钉钉权限卡片；如果卡片不可见，也可以")).Join(instructions))
		}
	}
	return r.noticeText(id, request.Join(NewText("\n\n")).Join(instructions))
}
func (r *DingTalkRuntime) applyPermission(id string, d PermissionDecision) (bool, string) {
	c := r.chat(id)
	c.mu.Lock()
	found := false
	for _, key := range c.state.Pending {
		found = found || key == d.RequestID
	}
	c.mu.Unlock()
	if !found {
		return false, "未找到待确认的权限请求：" + d.RequestID
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("permission_response")}, protocol.JSONField{Name: "requestId", Value: raw(d.RequestID)}, protocol.JSONField{Name: "allowed", Value: raw(d.Allowed)})
	if d.Rule != "" {
		message.Set("rule", raw(d.Rule))
	}
	if !r.Bridge.Send(id, message) {
		return false, "权限响应发送失败，请检查会话状态。"
	}
	c.mu.Lock()
	for i, key := range c.state.Pending {
		if key == d.RequestID {
			c.state.Pending = append(c.state.Pending[:i], c.state.Pending[i+1:]...)
			break
		}
	}
	if c.state.PendingPermissionCount > 0 {
		c.state.PendingPermissionCount--
	}
	c.mu.Unlock()
	r.mu.Lock()
	delete(r.permissionChats, d.RequestID)
	r.mu.Unlock()
	return true, ""
}
func (r *DingTalkRuntime) HandleCardCallback(value json.RawMessage) error {
	next, err := r.PrepareCardCallback(r.ctx, value)
	if err != nil || next == nil {
		return err
	}
	return next(r.ctx)
}
func (r *DingTalkRuntime) PrepareCardCallback(ctx context.Context, value json.RawMessage) (func(context.Context) error, error) {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil, nil
	}
	defer done(nil)
	action := ParseDingTalkPermissionCardAction(value)
	if action == nil {
		return nil, nil
	}
	request := action.String("requestId")
	id := action.String("chatId")
	r.mu.Lock()
	fallback := r.permissionChats[request]
	r.mu.Unlock()
	if id != "" {
		r.mu.Lock()
		c := r.chats[id]
		r.mu.Unlock()
		if c == nil {
			id = ""
		} else {
			c.mu.Lock()
			has := c.state.Pending != nil
			c.mu.Unlock()
			if !has {
				id = ""
			}
		}
	}
	if id == "" {
		id = fallback
	}
	if id == "" {
		r.Log("[DingTalk][Card] permission request not found: " + request)
		return nil, nil
	}
	d := PermissionDecision{RequestID: request, Allowed: string(action.Get("allowed")) == "true", Rule: action.String("rule")}
	sent, notice := r.applyPermission(id, d)
	if sent {
		return func(context.Context) error { return r.Notice(id, PermissionDecisionStatus(d)) }, nil
	}
	r.launchNotice(id, notice)
	return nil, nil
}
func (r *DingTalkRuntime) Existing(id string) (SessionRestore, error) {
	return RestoreSession(r.ctx, id, r.Store, r.ServerClient, r.Bridge, r.handler(id), func() { r.ClearChat(id) })
}
func (r *DingTalkRuntime) create(id, work string) (bool, error) {
	r.selection.Clear(id)
	r.Bridge.Reset(id)
	r.ClearChat(id)
	sid, err := r.ServerClient.CreateSession(r.ctx, work)
	if err == nil {
		err = r.Store.Set(id, sid, work)
	}
	if err != nil {
		return false, r.Notice(id, "❌ 无法创建会话: "+err.Error())
	}
	r.Bridge.Connect(id, sid, r.handler(id))
	if !r.Bridge.WaitOpen(r.ctx, id) {
		return false, r.Notice(id, "⚠️ 连接服务器超时，请重试。")
	}
	return true, nil
}
func (r *DingTalkRuntime) ensure(id string) (bool, error) {
	result, err := r.Existing(id)
	if err != nil {
		return false, err
	}
	if result.Status == "restored" {
		return true, nil
	}
	if result.Status == "unavailable" {
		return false, r.Notice(id, SessionReconnectNotice)
	}
	return r.create(id, r.Config.WorkDir)
}
func (r *DingTalkRuntime) prepare(id string) error {
	r.selection.Clear(id)
	r.Bridge.Reset(id)
	if err := r.Store.Delete(id); err != nil {
		return err
	}
	r.ClearChat(id)
	c := r.chat(id)
	c.mu.Lock()
	c.state = ChatState{State: "idle"}
	c.mu.Unlock()
	return nil
}
func (r *DingTalkRuntime) projectInput(id, text string) (bool, error) {
	c := r.chat(id)
	c.mu.Lock()
	picker := c.projectPicker
	c.mu.Unlock()
	isNew := text == "/new" || text == "新会话" || strings.HasPrefix(text, "/new ")
	nonProject := map[string]bool{"/help": true, "帮助": true, "/status": true, "状态": true, "/clear": true, "清空": true, "/stop": true, "停止": true, "/projects": true, "项目列表": true}
	if !isNew && (!picker || text == "" || nonProject[text]) {
		return false, nil
	}
	query := text
	if isNew {
		query = ""
		if strings.HasPrefix(text, "/new ") {
			query = protocol.TrimJavaScriptString(text[5:])
		}
	}
	if err := r.prepare(id); err != nil {
		return true, err
	}
	c.mu.Lock()
	c.projectPicker = false
	c.mu.Unlock()
	if query == "" {
		ok, err := r.create(id, r.Config.WorkDir)
		if err != nil || !ok {
			return true, err
		}
		return true, r.Notice(id, "✅ 已新建会话，可以开始对话了。")
	}
	project, ambiguous, err := r.ServerClient.MatchProject(r.ctx, query)
	if err != nil {
		return true, r.Notice(id, "❌ "+err.Error())
	}
	if project != nil {
		ok, err := r.create(id, project.String("realPath"))
		if err != nil || !ok {
			return true, err
		}
		branch := ""
		if project.String("branch") != "" {
			branch = " (" + project.String("branch") + ")"
		}
		return true, r.Notice(id, "✅ 已新建会话：**"+project.String("projectName")+"**"+branch)
	}
	if len(ambiguous) > 0 {
		lines := []string{}
		for i, p := range ambiguous {
			lines = append(lines, fmt.Sprintf("%d. **%s** — %s", i+1, p.String("projectName"), p.String("realPath")))
		}
		return true, r.Notice(id, "匹配到多个项目，请发送 /new <更完整名称或路径> 选择：\n\n"+strings.Join(lines, "\n"))
	}
	return true, r.Notice(id, "未找到匹配 \""+query+"\" 的项目。发送 /projects 查看完整列表。")
}
func (r *DingTalkRuntime) ShowProjects(id string) error {
	r.selection.Clear(id)
	projects, err := r.ServerClient.Projects(r.ctx)
	if err != nil {
		return r.Notice(id, "❌ 无法获取项目列表: "+err.Error())
	}
	if len(projects) == 0 {
		return r.Notice(id, "没有找到最近的项目。发送 /new 会使用默认工作目录："+r.Config.WorkDir+"\n也可以发送 /new /path/to/project 指定项目。")
	}
	c := r.chat(id)
	c.mu.Lock()
	c.projectPicker = true
	c.mu.Unlock()
	lines := []string{}
	for i, p := range projects {
		if i >= 10 {
			break
		}
		branch := ""
		if p.String("branch") != "" {
			branch = " (" + p.String("branch") + ")"
		}
		lines = append(lines, fmt.Sprintf("%d. **%s**%s\n   %s", i+1, p.String("projectName"), branch, p.String("realPath")))
	}
	return r.Notice(id, "选择项目（回复编号）：\n\n"+strings.Join(lines, "\n\n")+"\n\n也可以发送 /new <编号或名称>")
}
func (r *DingTalkRuntime) Status(id string) (string, error) {
	result, err := r.Existing(id)
	if err != nil {
		return "", err
	}
	if result.Status == "unavailable" {
		return SessionReconnectNotice, nil
	}
	if result.Status == "missing" {
		return emptyStatus, nil
	}
	s := result.Session
	project := filepath.Base(s.WorkDir)
	branch := ""
	if git, err := r.ServerClient.Git(r.ctx, s.SessionID); err == nil {
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
	if tasks, err := r.ServerClient.Tasks(r.ctx, s.SessionID); err == nil && len(tasks) > 0 {
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
func (r *DingTalkRuntime) route(id string, text Text, attachments []protocol.JSONObject) error {
	text = text.Trim()
	value := text.String()
	has := len(attachments) > 0
	if !has {
		if decision := ParsePermissionCommand(value, r.State(id).Pending); decision != nil {
			sent, notice := r.applyPermission(id, *decision)
			if sent {
				notice = PermissionDecisionStatus(*decision)
			}
			r.launchNotice(id, notice)
			return nil
		}
		if handled, err := r.selection.Handle(r.ctx, id, value); handled || err != nil {
			return err
		}
		if handled, err := r.projectInput(id, value); handled || err != nil {
			return err
		}
		switch value {
		case "/help", "帮助":
			return r.Notice(id, IMHelp())
		case "/status", "状态":
			status, err := r.Status(id)
			if err != nil {
				return err
			}
			return r.Notice(id, status)
		case "/projects", "项目列表":
			return r.ShowProjects(id)
		case "/clear", "清空", "/stop", "停止":
			result, err := r.Existing(id)
			if err != nil {
				return err
			}
			if result.Status != "restored" {
				notice := emptyStatus
				if result.Status == "unavailable" {
					notice = SessionReconnectNotice
				}
				return r.Notice(id, notice)
			}
			if value == "/stop" || value == "停止" {
				r.Bridge.Send(id, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("stop_generation")}))
				return r.Notice(id, "⏹ 已发送停止信号。")
			}
			r.ClearChat(id)
			if !r.Bridge.SendUser(id, "/clear", nil) {
				return r.Notice(id, "⚠️ 无法发送 /clear，请先发送 /new 重新连接会话。")
			}
			c := r.chat(id)
			c.mu.Lock()
			c.state.State = "thinking"
			c.mu.Unlock()
			return r.Notice(id, "🧹 已清空当前会话上下文。")
		}
	}
	ready, err := r.ensure(id)
	if err != nil || !ready {
		return err
	}
	if len(text) == 0 && has {
		text = NewText("(用户发送了附件)")
	}
	if len(text) == 0 && !has {
		return nil
	}
	var attachmentJSON json.RawMessage
	if has {
		attachmentJSON = raw(attachments)
	}
	if r.Bridge.SendUserText(id, text, attachmentJSON) {
		c := r.chat(id)
		c.mu.Lock()
		c.state.State = "thinking"
		c.mu.Unlock()
		return nil
	}
	return r.Notice(id, "⚠️ 消息发送失败，连接可能已断开。请发送 /new 重新开始。")
}
func (r *DingTalkRuntime) QueueUserMessage(id string, text Text, attachments []protocol.JSONObject) {
	c := r.chat(id)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	c.mu.Lock()
	previous := c.tail
	done := make(chan struct{})
	c.tail = done
	c.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer close(done)
		if previous != nil {
			select {
			case <-previous:
			case <-r.ctx.Done():
				return
			}
		}
		if r.ctx.Err() == nil {
			if err := r.route(id, text, attachments); err != nil {
				r.Log("[DingTalk] message error: " + err.Error())
			}
		}
	}()
}
func (r *DingTalkRuntime) collectAttachments(id, session string, candidates []protocol.JSONObject) ([]protocol.JSONObject, error) {
	if len(candidates) == 0 {
		return []protocol.JSONObject{}, nil
	}
	token, err := r.API.AccessToken(r.ctx)
	if err != nil {
		r.Log("[DingTalk] access token for attachment download failed: " + err.Error())
		return []protocol.JSONObject{}, r.Notice(id, "📎 附件下载授权失败，请稍后重试。")
	}
	type result struct {
		local LocalAttachment
		err   error
	}
	results := make([]result, len(candidates))
	var workers sync.WaitGroup
	for i, candidate := range candidates {
		workers.Add(1)
		go func(index int, candidate protocol.JSONObject) {
			defer workers.Done()
			results[index].local, results[index].err = r.API.DownloadCandidate(r.ctx, r.Attachments, candidate, session, token)
		}(i, candidate)
	}
	workers.Wait()
	refs := []protocol.JSONObject{}
	failures := 0
	for index, result := range results {
		if result.err != nil {
			failures++
			r.Log("[DingTalk] media download failed: " + result.err.Error())
			continue
		}
		local := result.local
		check := CheckAttachmentLimit(local.Kind, local.Size, local.MIME)
		if !check.OK {
			if err := r.Notice(id, check.Hint); err != nil {
				return nil, err
			}
			continue
		}
		ref := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(local.Kind)}, protocol.JSONField{Name: "name", Value: raw(GetDingTalkAttachmentName(candidates[index], local))}, protocol.JSONField{Name: "mimeType", Value: raw(local.MIME)})
		if local.Kind == "image" {
			ref.Set("data", raw(base64.StdEncoding.EncodeToString(local.Buffer)))
		} else {
			ref.Set("path", raw(local.Path))
		}
		refs = append(refs, ref)
	}
	if failures > 0 {
		text := "📎 附件下载失败，请稍后重试。"
		if failures < len(candidates) {
			text = fmt.Sprintf("📎 %d 个附件下载失败，已跳过。", failures)
		}
		if err := r.Notice(id, text); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
func (r *DingTalkRuntime) launchNotice(id, text string) {
	if text == "" {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		if err := r.Notice(id, text); err != nil && r.ctx.Err() == nil {
			r.Log("[DingTalk] notice failed: " + err.Error())
		}
	}()
}
func (r *DingTalkRuntime) HandleRobotMessage(value json.RawMessage) error {
	next, err := r.PrepareRobotMessage(r.ctx, value)
	if err != nil || next == nil {
		return err
	}
	return next(r.ctx)
}
func (r *DingTalkRuntime) PrepareRobotMessage(ctx context.Context, value json.RawMessage) (func(context.Context) error, error) {
	done, admitted := r.migration.Admit()
	if !admitted {
		return nil, nil
	}
	defer done(nil)
	if !IsDingTalkDirectMessage(value) {
		return nil, nil
	}
	chat, user := DingTalkChatID(value), DingTalkSenderID(value)
	text := ExtractDingTalkText(value)
	candidates := ExtractDingTalkAttachments(value)
	if chat == nil || user == nil || (len(text) == 0 && len(candidates) == 0) {
		return nil, nil
	}
	id := chat.String()
	data := object(value)
	c := r.chat(id)
	if credentialTruthy(data.Get("sessionWebhook")) {
		c.mu.Lock()
		c.webhook = dingTalkCoerce(data.Get("sessionWebhook")).String()
		c.mu.Unlock()
	}
	if !r.Pairing.Allowed("dingtalk", raw(*user)) {
		name := data.Get("senderNick")
		if !credentialTruthy(name) {
			name = raw("DingTalk User")
		}
		paired, err := r.Pairing.TryPair("dingtalk", text.String(), dingTalkCoerce(name).String(), raw(*user))
		if err != nil {
			return nil, err
		}
		if paired {
			return func(context.Context) error {
				return r.Notice(id, "✅ 配对成功！现在可以开始聊天了。\n\n发送消息即可与 Claude 对话。发送 /help 查看可用命令。")
			}, nil
		}
		return func(context.Context) error {
			return r.Notice(id, "🔒 未授权。请先在 Claude Code 桌面端完成钉钉扫码绑定，再生成 IM 配对码后发送给我。")
		}, nil
	}
	target := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "userId", Value: raw(*user)})
	c.mu.Lock()
	c.target = &target
	c.mu.Unlock()
	session := id
	if stored := r.Store.Get(id); stored != nil {
		session = stored.SessionID
	}
	return func(context.Context) error {
		attachments, err := r.collectAttachments(id, session, candidates)
		if err != nil {
			return err
		}
		r.QueueUserMessage(id, text, attachments)
		return nil
	}, nil
}
func (r *DingTalkRuntime) Start() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return context.Canceled
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() { defer r.wg.Done(); r.Attachments.GC() }()
	if err := r.Stream.Connect(r.ctx); err != nil {
		return err
	}
	r.Log("[DingTalk] Stream connected. Server: " + r.Config.ServerURL)
	return nil
}
func (r *DingTalkRuntime) Wait() error { return r.Stream.Wait() }
func (r *DingTalkRuntime) Close() {
	r.stop.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.cancel()
		r.Stream.Close()
		r.Bridge.Close()
		r.wg.Wait()
		r.mu.Lock()
		chats := []*dingTalkRuntimeChat{}
		for _, c := range r.chats {
			chats = append(chats, c)
		}
		buffers := append([]*MessageBuffer{}, r.retired...)
		r.mu.Unlock()
		for _, c := range chats {
			c.mu.Lock()
			if c.buffer != nil {
				buffers = append(buffers, c.buffer)
				c.buffer = nil
			}
			c.mu.Unlock()
		}
		for _, b := range buffers {
			b.Close()
		}
		r.API.Close()
		r.platformRuntimeCore.close()
	})
}
