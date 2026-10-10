package imruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (r *FeishuRuntime) applyPermission(id string, d PermissionDecision, rawAllowed ...json.RawMessage) bool {
	c := r.chat(id)
	c.mu.Lock()
	found := false
	for _, old := range c.state.Pending {
		if old == d.RequestID {
			found = true
		}
	}
	c.mu.Unlock()
	if !found {
		r.launch(func() { _ = r.Notice(id, "未找到待确认的权限请求："+d.RequestID) })
		return false
	}
	allowed := raw(d.Allowed)
	if len(rawAllowed) > 0 {
		allowed = rawAllowed[0]
	}
	m := feishuObject(feishuField("type", "permission_response"), feishuField("requestId", d.RequestID), protocol.JSONField{Name: "allowed", Value: allowed})
	if d.Rule != "" {
		m.Set("rule", raw(d.Rule))
	}
	if !r.Bridge.Send(id, m) {
		r.launch(func() { _ = r.Notice(id, "权限响应发送失败，请检查会话状态。") })
		return false
	}
	c.mu.Lock()
	for i, v := range c.state.Pending {
		if v == d.RequestID {
			c.state.Pending = append(c.state.Pending[:i], c.state.Pending[i+1:]...)
			break
		}
	}
	if c.state.PendingPermissionCount > 0 {
		c.state.PendingPermissionCount--
	}
	c.mu.Unlock()
	return true
}
func (r *FeishuRuntime) HandleCardAction(event protocol.JSONObject) (json.RawMessage, error) {
	value := object(object(event.Get("action")).Get("value"))
	contextValue := object(event.Get("context"))
	chat := contextValue.Get("open_chat_id")
	if !credentialTruthy(chat) {
		return nil, nil
	}
	id := jsString(chat)
	switch value.String("action") {
	case "permit":
		request := value.Get("requestId")
		if !credentialTruthy(request) {
			return nil, nil
		}
		allow := value.Get("allowed")
		if len(allow) == 0 || string(allow) == "null" {
			allow = raw(false)
		}
		d := PermissionDecision{RequestID: jsString(request), Allowed: credentialTruthy(allow)}
		if value.String("rule") == "always" {
			d.Rule = "always"
		}
		if !r.applyPermission(id, d, allow) {
			return json.RawMessage(`{"toast":{"type":"warning","content":"权限响应发送失败"}}`), nil
		}
		notice := "❌ 已拒绝"
		toast := notice
		if d.Allowed {
			notice = "✅ 已允许"
			toast = notice
			if d.Rule == "always" {
				notice = "♾️ 已永久允许（本次会话内不再询问相同操作）"
				toast = "♾️ 永久允许"
			}
		}
		if e := r.Notice(id, notice); e != nil {
			return nil, e
		}
		return raw(feishuObject(feishuField("toast", feishuObject(feishuField("type", "info"), feishuField("content", toast))))), nil
	case "pick_project":
		path := value.Get("realPath")
		if !credentialTruthy(path) {
			return nil, nil
		}
		name := value.Get("projectName")
		if len(name) == 0 || string(name) == "null" {
			name = path
		}
		if len(name) == 0 || string(name) == "null" {
			name = raw("(unknown)")
		}
		done := r.enqueue(id, func() error {
			r.clearProjectPicker(id)
			ok, e := r.create(id, jsString(path))
			if e != nil || !ok {
				return e
			}
			return r.Notice(id, "✅ 已新建会话：**"+jsString(name)+"**")
		})
		select {
		case <-done:
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
		return raw(feishuObject(feishuField("toast", feishuObject(feishuField("type", "info"), feishuField("content", "📁 "+jsString(name)))))), nil
	}
	return nil, nil
}
func (r *FeishuRuntime) HandleServerMessage(id string, m protocol.JSONObject) error {
	c := r.chat(id)
	c.mu.Lock()
	if c.state.SyncPermission(m) {
		c.mu.Unlock()
		return nil
	}
	kind := m.String("type")
	if kind == "status" {
		c.state.State = m.String("state")
		c.state.Verb = m.String("verb")
	}
	if kind == "message_complete" || kind == "error" {
		c.state.State = "idle"
		c.state.Verb = ""
	}
	c.mu.Unlock()
	switch kind {
	case "content_start":
		if m.String("blockType") == "text" {
			if e := r.getCard(id).EnsureCreated(r.ctx); e != nil {
				r.o.Log("[Feishu] ensureCreated on content_start failed: " + e.Error())
			}
		} else if m.String("blockType") == "tool_use" {
			c.mu.Lock()
			card := c.card
			c.mu.Unlock()
			if card != nil {
				card.StartTool(TextJSON(m.Get("toolUseId")), TextJSON(m.Get("toolName")))
			}
		}
	case "content_delta":
		if text := m.Get("text"); isString(text) && credentialTruthy(text) {
			card := r.getCard(id)
			r.launch(func() {
				if e := card.EnsureCreated(r.ctx); e != nil {
					r.o.Log("[Feishu] ensureCreated on delta failed: " + e.Error())
				}
			})
			card.AppendText(TextJSON(text))
			c.mu.Lock()
			uploads := c.watcher.Feed(TextJSON(text))
			c.mu.Unlock()
			for _, p := range uploads {
				p := p
				r.launch(func() { r.dispatchImage(id, p) })
			}
		}
	case "thinking":
		if text := m.Get("text"); isString(text) && credentialTruthy(text) {
			c.mu.Lock()
			card := c.card
			c.mu.Unlock()
			if card != nil {
				card.AppendReasoning(TextJSON(text))
			}
		}
	case "tool_use_complete":
		c.mu.Lock()
		card := c.card
		c.mu.Unlock()
		if card != nil {
			card.CompleteTool(TextJSON(m.Get("toolUseId")), TextJSON(m.Get("toolName")))
		}
	case "permission_request":
		request := m.String("requestId")
		c.mu.Lock()
		c.state.PendingPermissionCount++
		c.state.State = "permission_pending"
		found := false
		for _, v := range c.state.Pending {
			found = found || v == request
		}
		if !found {
			c.state.Pending = append(c.state.Pending, request)
		}
		c.mu.Unlock()
		work := Text{}
		if s := r.Store.Get(id); s != nil {
			work = NewText(s.WorkDir)
		}
		card := FeishuPermissionCard(TextJSON(m.Get("toolName")), m.Get("input"), TextJSON(m.Get("requestId")), work, NewText(r.o.Config.Home))
		mid, e := r.API.SendCard(r.ctx, NewText(id), card)
		if e != nil {
			return e
		}
		if !credentialTruthy(mid) {
			return r.Notice(id, feishuRuntimePermissionText(m.String("toolName"), m.Get("input"), request)+"\n\n"+PermissionInstructions(request))
		}
	case "message_complete":
		card := r.detachCard(id)
		if card != nil {
			defer card.Close()
			return card.Finalize(r.ctx)
		}
	case "error":
		message := m.Get("message")
		text := "undefined"
		if len(message) > 0 {
			text = jsString(message)
		}
		if invalidThinking.MatchString(text) {
			card := r.detachCard(id)
			if card != nil {
				r.launch(func() { _ = card.Abort(r.ctx, fmt.Errorf("session reset")); card.Close() })
			}
			work := r.o.Config.WorkDir
			if s := r.Store.Get(id); s != nil && s.WorkDir != "" {
				work = s.WorkDir
			}
			if work != "" {
				_ = r.Notice(id, "⚠️ 会话上下文已失效，正在自动重建...")
				r.Bridge.Reset(id)
				_ = r.Store.Delete(id)
				c.mu.Lock()
				pending := c.state.Pending
				c.watcher = ImageWatcher{}
				c.uploaded = map[string]json.RawMessage{}
				c.state = ChatState{State: "idle", Pending: pending}
				c.mu.Unlock()
				ok, e := r.create(id, work)
				if e != nil {
					return e
				}
				if ok {
					return r.Notice(id, "✅ 已重建会话，请重新发送消息。")
				}
				return r.Notice(id, "❌ 重建会话失败，请发送 /new 手动新建。")
			}
			return r.Notice(id, "⚠️ 会话上下文已失效，请发送 /new 新建会话。")
		}
		card := r.detachCard(id)
		if card != nil {
			defer card.Close()
			if len(message) == 0 || string(protocol.StringifyValidatedJavaScriptJSON(message)) == "null" {
				text = "unknown error"
			}
			return card.Abort(r.ctx, fmt.Errorf("%s", text))
		}
		return r.Notice(id, "❌ "+text)
	case "system_notification":
		if m.String("subtype") == "init" {
			model := object(m.Get("data")).Get("model")
			if isString(model) && len(TextJSON(model).Trim()) > 0 {
				c.mu.Lock()
				c.state.Model = TextJSON(model).String()
				c.mu.Unlock()
			}
		}
	}
	return nil
}
func (r *FeishuRuntime) dispatchImage(id string, p PendingUpload) {
	c := r.chat(id)
	c.mu.Lock()
	_, exists := c.uploaded[p.ID]
	c.mu.Unlock()
	if exists {
		return
	}
	image, e := r.materializeOutbound(p.Source)
	if e != nil {
		r.o.Log("[Feishu] dispatchOutboundImage failed: " + e.Error())
		return
	}
	check := CheckAttachmentLimit("image", int64(len(image.Buffer)), image.MIME)
	if !check.OK {
		r.o.Log("[Feishu] Outbound image rejected: " + check.Hint)
		return
	}
	key, e := r.Media.UploadImage(r.ctx, image.Buffer, image.MIME)
	if e != nil {
		r.o.Log("[Feishu] dispatchOutboundImage failed: " + e.Error())
		return
	}
	c.mu.Lock()
	c.uploaded[p.ID] = key
	c.mu.Unlock()
	if e = r.Media.SendImageMessage(r.ctx, NewText(id), key); e != nil {
		r.o.Log("[Feishu] dispatchOutboundImage failed: " + e.Error())
	}
}

func feishuRuntimePermissionText(tool string, input json.RawMessage, request string) string {
	preview := NewText("(unserializable)")
	if isString(input) {
		preview = TextJSON(input)
	} else if len(input) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(input), "", "  ") == nil {
			preview = NewText(pretty.String())
		}
	}
	if len(preview) > 300 {
		preview = preview.Slice(0, 300).Join(NewText("…"))
	}
	return "🔐 需要权限确认 [" + request + "]\n工具: " + tool + "\n" + preview.String()
}
