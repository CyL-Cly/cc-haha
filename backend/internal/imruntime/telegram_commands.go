package imruntime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type telegramSelection struct {
	Kind, Title string
	Items       []SelectionItem
	Page        int
	Expires     time.Time
}

const officialDefaultModel = "claude-opus-4-8"
const openAIDefaultModel = "gpt-5.3-codex"

func TelegramHelp() string {
	return "👋 Claude Code Bot 已就绪。\n\n可用命令：\n\n/new [项目] / 新会话 — 新建会话或切换项目\n/projects / 项目列表 — 查看最近项目\n/sessions [项目] / 会话列表 — 查看当前项目的旧会话\n/sessions projects — 选择其他项目的旧会话\n/resume <编号> / 继续会话 <编号> — 恢复列表中的会话\n/sessions next / /sessions prev — 翻页\n/cancel / 取消选择 — 退出选择，保留当前会话\n/status / 状态 — 查看当前会话状态\n/clear / 清空 — 清空当前会话上下文\n/stop / 停止 — 停止当前生成\n/help / 帮助 — 显示这份帮助\n权限审批：/allow <id>、/always <id>、/deny <id>\n\nTelegram 扩展命令：\n/resume — 按钮选择项目与历史会话\n/provider — 切换 Provider\n/model [model] — 查看或切换模型\n/skills — 查看当前项目可用 Skills"
}
func (r *TelegramRuntime) setModel(id, model string) {
	c := r.chat(id)
	c.mu.Lock()
	c.state.Model = model
	c.mu.Unlock()
}
func modelChanged(model string) string {
	return "✅ 已切换模型：" + model + "\n\n当前已运行的会话可能仍使用旧 runtime；发送 /new 后会按新模型启动。"
}
func providerChanged(label, model, suffix string) string {
	lines := []string{"✅ 已切换 Provider：" + strings.TrimPrefix(label, "✓ ")}
	if model != "" {
		lines = append(lines, "默认模型："+model)
	}
	return strings.Join(append(lines, suffix), "\n")
}
func (r *TelegramRuntime) applyProvider(id, value, label string) (string, string, error) {
	model := ""
	if value == "official" || value == "claude" {
		if err := r.Client.Activate(r.ctx, "official"); err != nil {
			return "", "", err
		}
		model = officialDefaultModel
		if err := r.Client.SetModel(r.ctx, model); err != nil {
			return "", "", err
		}
		r.setModel(id, model)
		return "Claude 官方", model, nil
	}
	openAI := value == "openai-official" || value == "openai"
	if openAI {
		value = "openai-official"
		model = openAIDefaultModel
	}
	if err := r.Client.Activate(r.ctx, value); err != nil {
		return "", "", err
	}
	if model == "" {
		catalog, err := r.Client.Providers(r.ctx)
		if err != nil {
			return "", "", err
		}
		for _, provider := range collectJSONArray(catalog.Get("providers")) {
			if provider.String("id") == value {
				model = protocol.TrimJavaScriptString(object(provider.Get("models")).String("main"))
				break
			}
		}
	}
	if model != "" {
		if err := r.Client.SetModel(r.ctx, model); err != nil {
			return "", "", err
		}
		r.setModel(id, model)
	}
	if label != "" {
		label = strings.TrimPrefix(label, "✓ ")
	} else if openAI {
		label = "ChatGPT Official"
	} else {
		label = value
	}
	return label, model, nil
}
func (r *TelegramRuntime) extendedCommand(id, command, query string) error {
	query = protocol.TrimJavaScriptString(query)
	switch command {
	case "provider":
		if query == "" {
			return r.showProviders(id)
		}
		label, model, err := r.applyProvider(id, query, "")
		if err != nil {
			return r.notice(id, "❌ Provider 切换失败："+err.Error())
		}
		return r.notice(id, providerChanged(label, model, "发送 /new 后新配置会用于新会话。"))
	case "model":
		if query == "" {
			return r.showModels(id)
		}
		if err := r.Client.SetModel(r.ctx, query); err != nil {
			return r.notice(id, "❌ 模型切换失败："+err.Error())
		}
		r.setModel(id, query)
		return r.notice(id, modelChanged(query))
	case "skills":
		return r.showSkills(id)
	case "status":
		text, err := r.status(id)
		if err != nil {
			return err
		}
		return r.notice(id, text)
	case "stop", "clear":
		result, err := r.existing(id)
		if err != nil {
			return err
		}
		if result.Status != "restored" {
			if result.Status == "unavailable" {
				return r.notice(id, SessionReconnectNotice)
			}
			return r.notice(id, emptyStatus)
		}
		if command == "stop" {
			r.Bridge.Send(id, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("stop_generation")}))
			return r.notice(id, "⏹ 已发送停止信号。")
		}
		r.ClearChat(id)
		if !r.Bridge.SendUser(id, "/clear", nil) {
			return r.notice(id, "⚠️ 无法发送 /clear，请先发送 /new 重新连接会话。")
		}
		c := r.chat(id)
		c.mu.Lock()
		c.state.State = "thinking"
		c.mu.Unlock()
		return r.notice(id, "🧹 已清空当前会话上下文。")
	}
	return nil
}
func selectionView(selection *telegramSelection) (string, protocol.JSONObject) {
	page := BuildSelectionPage(selection.Kind, selection.Items, float64(selection.Page), 8)
	lines := []string{}
	for i, item := range page.Visible {
		line := fmt.Sprintf("%d. %s", i+1, item.Label)
		if item.Description != "" {
			line += "\n   " + item.Description
		}
		lines = append(lines, line)
	}
	suffix := ""
	if page.TotalPages > 1 {
		suffix = fmt.Sprintf("\n\n第 %d/%d 页", page.Page+1, page.TotalPages)
	}
	rows := [][]map[string]string{}
	for _, row := range page.Rows {
		buttons := []map[string]string{}
		for _, button := range row {
			buttons = append(buttons, map[string]string{"text": button.Text, "callback_data": button.Callback})
		}
		rows = append(rows, buttons)
	}
	options := protocol.NewJSONObject(protocol.JSONField{Name: "reply_markup", Value: raw(map[string]any{"inline_keyboard": rows})})
	return selection.Title + "\n\n" + strings.Join(lines, "\n\n") + suffix, options
}
func (r *TelegramRuntime) sendSelection(id string, selection *telegramSelection) error {
	selection.Expires = r.options.Now().Add(15 * time.Minute)
	r.chat(id).selection = selection
	text, options := selectionView(selection)
	_, err := r.API.SendOptions(r.ctx, id, NewText(text), options)
	return err
}
func (r *TelegramRuntime) editSelection(id string, message int64, selection *telegramSelection) error {
	selection.Expires = r.options.Now().Add(15 * time.Minute)
	r.chat(id).selection = selection
	text, options := selectionView(selection)
	if err := r.API.EditOptions(r.ctx, id, message, NewText(text), options); err != nil {
		_, err = r.API.SendOptions(r.ctx, id, NewText(text), options)
		return err
	}
	return nil
}
func (r *TelegramRuntime) showProviders(id string) error {
	catalog, err := r.Client.Providers(r.ctx)
	if err != nil {
		return r.notice(id, "❌ 无法获取 Provider 列表："+err.Error())
	}
	active := catalog.String("activeId")
	prefix := func(value string) string {
		if active == value {
			return "✓ "
		}
		return ""
	}
	items := []SelectionItem{{Label: prefix("") + "Claude 官方", Value: "official", Description: "使用 Claude 官方或环境变量配置", Meta: map[string]string{"defaultModel": officialDefaultModel}}, {Label: prefix("openai-official") + "ChatGPT Official", Value: "openai-official", Description: "使用 ChatGPT 登录的 Codex 模型", Meta: map[string]string{"defaultModel": openAIDefaultModel}}}
	for _, provider := range collectJSONArray(catalog.Get("providers")) {
		model := protocol.TrimJavaScriptString(object(provider.Get("models")).String("main"))
		description := provider.String("id")
		var meta map[string]string
		if model != "" {
			description = "默认模型：" + model
			meta = map[string]string{"defaultModel": model}
		}
		items = append(items, SelectionItem{prefix(provider.String("id")) + provider.String("name"), provider.String("id"), description, meta})
	}
	return r.sendSelection(id, &telegramSelection{Kind: "provider", Title: "选择 Provider：", Items: items})
}
func (r *TelegramRuntime) showModels(id string) error {
	var catalog, current protocol.JSONObject
	var err error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); catalog, err = r.Client.Models(r.ctx) }()
	go func() { defer wg.Done(); current, _ = r.Client.CurrentModel(r.ctx) }()
	wg.Wait()
	if err != nil {
		return r.notice(id, "❌ 无法获取模型列表："+err.Error())
	}
	models := collectJSONArray(catalog.Get("models"))
	if len(models) == 0 {
		return r.notice(id, "没有可用模型。请先在桌面端配置 Provider。")
	}
	items := []SelectionItem{}
	active := object(current.Get("model")).String("id")
	for _, model := range models {
		name := model.String("name")
		if name == "" {
			name = model.String("id")
		}
		if model.String("id") == active {
			name = "✓ " + name
		}
		desc := []string{}
		for _, key := range []string{"id", "context", "description"} {
			if value := model.String(key); value != "" {
				desc = append(desc, value)
			}
		}
		items = append(items, SelectionItem{Label: name, Value: model.String("id"), Description: strings.Join(desc, " · ")})
	}
	provider := object(catalog.Get("provider"))
	name := "Claude 官方"
	if provider.Get("name") != nil && string(provider.Get("name")) != "null" {
		name = provider.String("name")
	}
	return r.sendSelection(id, &telegramSelection{Kind: "model", Title: "选择模型（" + name + "）：", Items: items})
}
func (r *TelegramRuntime) showSkills(id string) error {
	restored, err := r.existing(id)
	if err != nil {
		return err
	}
	if restored.Status == "unavailable" {
		return r.notice(id, SessionReconnectNotice)
	}
	cwd := r.options.Config.WorkDir
	if restored.Session != nil {
		cwd = restored.Session.WorkDir
	}
	if cwd == "" {
		return r.notice(id, "请先发送 /new 选择项目，再查看 Skills。")
	}
	catalog, err := r.Client.Skills(r.ctx, cwd)
	if err != nil {
		return r.notice(id, "❌ 无法获取 Skills："+err.Error())
	}
	items := []SelectionItem{}
	for _, skill := range collectJSONArray(catalog.Get("skills")) {
		if !truthy(skill.Get("userInvocable")) {
			continue
		}
		source := skill.String("source")
		if skill.String("pluginName") != "" {
			source += ":" + skill.String("pluginName")
		}
		label := skill.String("displayName")
		if label == "" {
			label = skill.String("name")
		}
		items = append(items, SelectionItem{Label: label, Value: skill.String("name"), Description: source + " · " + skill.String("description")})
	}
	if len(items) == 0 {
		return r.notice(id, "当前项目没有可用 Skills："+cwd)
	}
	return r.sendSelection(id, &telegramSelection{Kind: "skill", Title: "当前项目可用 Skills：\n" + cwd, Items: items})
}
func (r *TelegramRuntime) showResumeProjects(id string) error {
	c := r.chat(id)
	c.projectSelection = false
	c.sessionSelection.Clear(id)
	c.selection = nil
	projects, err := r.Client.Projects(r.ctx)
	if err != nil {
		return r.notice(id, "❌ 无法获取项目列表："+err.Error())
	}
	if len(projects) == 0 {
		return r.notice(id, "没有找到最近项目。请先发送 /new 创建会话。")
	}
	items := []SelectionItem{}
	for _, project := range projects {
		label := project.String("projectName")
		if project.String("branch") != "" {
			label += " (" + project.String("branch") + ")"
		}
		items = append(items, SelectionItem{Label: label, Value: project.String("realPath"), Description: project.String("realPath") + " · " + string(project.Get("sessionCount")) + " 个会话"})
	}
	return r.sendSelection(id, &telegramSelection{Kind: "resume_project", Title: "选择要恢复的项目：", Items: items})
}
func sessionSelectionItem(session protocol.JSONObject) SelectionItem {
	title := session.String("title")
	if title == "" {
		compact := session.String("id")
		if len(NewText(compact)) > 12 {
			compact = NewText(compact).Slice(0, 8).String()
		}
		title = "会话 " + compact
	}
	date := session.String("modifiedAt")
	if parsed, err := time.Parse(time.RFC3339Nano, date); err == nil {
		date = parsed.Local().Format("01/02 15:04")
	}
	var meta map[string]string
	if session.String("workDir") != "" {
		meta = map[string]string{"workDir": session.String("workDir")}
	}
	return SelectionItem{title, session.String("id"), date + " · " + string(session.Get("messageCount")) + " 条消息 · " + session.String("workDir"), meta}
}
func (r *TelegramRuntime) handleCallback(callback protocol.JSONObject) error {
	from := object(callback.Get("from"))
	message := object(callback.Get("message"))
	chat := object(message.Get("chat"))
	if len(from.OwnProperties()) == 0 || chat.String("type") != "private" {
		return nil
	}
	if !r.Dedup.TryRecord("telegram:callback:" + callback.String("id")) {
		return nil
	}
	id := jsonID(chat.Get("id"))
	c := r.chat(id)
	c.queue.Lock()
	defer c.queue.Unlock()
	callbackID := callback.String("id")
	var messageID int64
	_ = json.Unmarshal(message.Get("message_id"), &messageID)
	answer := func(text string) { _ = r.API.AnswerCallback(r.ctx, callbackID, text) }
	edit := func(text string) error { return r.API.EditMessage(r.ctx, id, messageID, NewText(text)) }
	data := callback.String("data")
	if selectionCallback := ParseSelectionCallback(data); selectionCallback != nil {
		if !r.Pairing.Allowed("telegram", from.Get("id")) {
			answer("未授权")
			return nil
		}
		selection := c.selection
		if selection == nil || selection.Kind != selectionCallback.Kind || selection.Expires.Before(r.options.Now()) {
			if selection != nil && selection.Expires.Before(r.options.Now()) {
				c.selection = nil
			}
			answer("选择已过期，请重新发送命令")
			return nil
		}
		if selectionCallback.Action == "noop" {
			answer("")
			return nil
		}
		if selectionCallback.Action == "page" {
			answer("")
			copy := *selection
			copy.Page = selectionCallback.Index
			return r.editSelection(id, messageID, &copy)
		}
		if selectionCallback.Index < 0 || selectionCallback.Index >= len(selection.Items) {
			answer("选项不存在，请重新发送命令")
			return nil
		}
		item := selection.Items[selectionCallback.Index]
		answer("处理中...")
		switch selection.Kind {
		case "provider":
			label, model, err := r.applyProvider(id, item.Value, item.Label)
			if err != nil {
				return edit("❌ Provider 切换失败：" + err.Error())
			}
			c.selection = nil
			return edit(providerChanged(label, model, "当前已运行的会话可能仍使用旧 runtime；发送 /new 后会按新配置启动。"))
		case "model":
			if err := r.Client.SetModel(r.ctx, item.Value); err != nil {
				return edit("❌ 模型切换失败：" + err.Error())
			}
			r.setModel(id, item.Value)
			c.selection = nil
			return edit("✅ 已切换模型：" + strings.TrimPrefix(item.Label, "✓ ") + "\n" + item.Value + "\n\n当前已运行的会话可能仍使用旧 runtime；发送 /new 后会按新模型启动。")
		case "skill":
			restored, err := r.existing(id)
			if err != nil {
				return err
			}
			if restored.Status == "unavailable" {
				return r.notice(id, SessionReconnectNotice)
			}
			if restored.Status == "missing" {
				c.selection = nil
				return edit("⚠️ 会话已失效，请发送 /new 重新选择项目后再调用 Skill。")
			}
			invocation := "/" + item.Value
			if !r.Bridge.SendUser(id, invocation, nil) {
				return edit("⚠️ Skill 发送失败。" + SessionReconnectNotice)
			}
			c.mu.Lock()
			c.state.State = "thinking"
			c.mu.Unlock()
			c.selection = nil
			lines := []string{"✅ 已调用 Skill：" + item.Label, invocation}
			if item.Description != "" {
				lines = append(lines, item.Description)
			}
			lines = append(lines, "任务已发送给当前 Agent，会继续使用这条会话的上下文、工具和权限设置。")
			return edit(strings.Join(lines, "\n"))
		case "resume_project":
			sessions, err := r.Client.ProjectHistory(r.ctx, item.Value, nil)
			if err != nil {
				return edit("❌ 无法获取会话列表：" + err.Error())
			}
			if len(sessions) == 0 {
				c.selection = nil
				return edit("没有可恢复会话：" + item.Label)
			}
			items := []SelectionItem{}
			for _, session := range sessions {
				items = append(items, sessionSelectionItem(session))
			}
			return r.editSelection(id, messageID, &telegramSelection{Kind: "resume_session", Title: "选择要恢复的会话：\n" + item.Label, Items: items})
		case "resume_session":
			work := item.Meta["workDir"]
			if work == "" {
				return edit("❌ 这个会话缺少工作目录，无法恢复。")
			}
			ok, text := RestoreSelectedSession(r.ctx, c.sessionSelection.Deps, id, protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: raw(item.Value)}, protocol.JSONField{Name: "workDir", Value: raw(work)}, protocol.JSONField{Name: "title", Value: raw(item.Label)}))
			if ok {
				c.selection = nil
			}
			return edit(text)
		}
		return nil
	}
	if !strings.HasPrefix(data, "permit:") {
		return nil
	}
	decision := ParsePermitCallback(data)
	if decision == nil {
		return nil
	}
	if !r.Pairing.Allowed("telegram", from.Get("id")) {
		answer("未授权")
		return nil
	}
	c.mu.Lock()
	found := false
	for _, request := range c.state.Pending {
		found = found || request == decision.RequestID
	}
	c.mu.Unlock()
	if !found {
		answer("权限请求已失效")
		return nil
	}
	if !r.sendPermission(id, *decision) {
		answer("权限响应发送失败")
		return nil
	}
	c.mu.Lock()
	removePending(&c.state, decision.RequestID)
	c.mu.Unlock()
	status := PermissionDecisionStatus(*decision)
	_ = edit(message.String("text") + "\n\n" + status)
	return r.API.AnswerCallback(r.ctx, callbackID, status)
}

const emptyStatus = "当前没有活动会话。\n\n发送 /sessions 选择旧会话，/new 新建会话，或 /projects 选择项目。"

func (r *TelegramRuntime) status(id string) (string, error) {
	restored, err := r.existing(id)
	if err != nil {
		return "", err
	}
	if restored.Status == "unavailable" {
		return SessionReconnectNotice, nil
	}
	if restored.Status == "missing" {
		return emptyStatus, nil
	}
	stored := restored.Session
	project := filepath.Base(stored.WorkDir)
	if project == "" {
		project = stored.WorkDir
	}
	branch := ""
	if git, err := r.Client.Git(r.ctx, stored.SessionID); err == nil {
		if git.String("repoName") != "" {
			project = git.String("repoName")
		} else if git.String("workDir") != "" {
			project = filepath.Base(git.String("workDir"))
		}
		branch = git.String("branch")
	}
	c := r.chat(id)
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	lines := []string{"当前会话状态："}
	if project != "" {
		label := "项目: " + project
		if branch != "" {
			label += " (" + branch + ")"
		}
		lines = append(lines, label)
	} else if branch != "" {
		lines = append(lines, "分支: "+branch)
	}
	session := stored.SessionID
	if len(NewText(session)) > 12 {
		session = NewText(session).Slice(0, 8).String() + "…"
	}
	lines = append(lines, "会话: "+session)
	if state.Model != "" {
		lines = append(lines, "模型: "+state.Model)
	}
	label := "空闲"
	switch state.State {
	case "thinking":
		label = "思考中"
	case "streaming":
		label = "生成中"
	case "tool_executing":
		label = "执行工具中"
	case "permission_pending":
		label = "等待权限确认"
	}
	if state.Verb != "" && state.Verb != "Thinking" {
		label += " (" + state.Verb + ")"
	}
	lines = append(lines, "状态: "+label)
	if state.PendingPermissionCount > 0 {
		lines = append(lines, fmt.Sprintf("审批: %d 个待确认", state.PendingPermissionCount))
	}
	if tasks, err := r.Client.Tasks(r.ctx, stored.SessionID); err == nil && len(tasks) > 0 {
		counts := map[string]int{}
		for _, task := range tasks {
			counts[task.String("status")]++
		}
		parts := []string{fmt.Sprintf("总计 %d", len(tasks))}
		for _, status := range []struct{ key, label string }{{"in_progress", "进行中"}, {"pending", "待处理"}, {"completed", "已完成"}} {
			if counts[status.key] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", status.label, counts[status.key]))
			}
		}
		lines = append(lines, "任务: "+strings.Join(parts, " · "))
	}
	return strings.Join(lines, "\n"), nil
}
