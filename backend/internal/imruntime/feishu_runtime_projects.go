package imruntime

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"strings"
)

func (r *FeishuRuntime) projectInput(id, text string) (bool, error) {
	text = protocol.TrimJavaScriptString(text)
	c := r.chat(id)
	c.mu.Lock()
	pending := c.projectPicker
	c.mu.Unlock()
	explicit := text == "/new" || text == "新会话" || strings.HasPrefix(text, "/new ")
	excluded := map[string]bool{"/help": true, "帮助": true, "/status": true, "状态": true, "/clear": true, "清空": true, "/stop": true, "停止": true, "/projects": true, "项目列表": true}
	if !explicit && (!pending || text == "" || excluded[text]) {
		return false, nil
	}
	query := text
	if explicit {
		query = ""
		if strings.HasPrefix(text, "/new ") {
			query = protocol.TrimJavaScriptString(text[5:])
		}
	}
	r.prepareNew(id)
	r.clearProjectPicker(id)
	if query == "" {
		ok, e := r.create(id, r.o.Config.WorkDir)
		if e != nil || !ok {
			return true, e
		}
		return true, r.Notice(id, "✅ 已新建会话，可以开始对话了。")
	}
	p, ambiguous, e := r.Client.MatchProject(r.ctx, query)
	if e != nil {
		return true, r.Notice(id, "❌ "+e.Error())
	}
	if p != nil {
		ok, e := r.create(id, p.String("realPath"))
		if e != nil || !ok {
			return true, e
		}
		branch := ""
		if p.String("branch") != "" {
			branch = " (" + p.String("branch") + ")"
		}
		return true, r.Notice(id, "✅ 已新建会话：**"+p.String("projectName")+"**"+branch)
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
func (r *FeishuRuntime) showProjects(id string) error {
	c := r.chat(id)
	c.selection.Clear(id)
	projects, e := r.Client.Projects(r.ctx)
	if e != nil {
		return r.Notice(id, "❌ 无法获取项目列表: "+e.Error())
	}
	if len(projects) == 0 {
		return r.Notice(id, "没有找到最近的项目。发送 /new 会使用默认工作目录："+r.o.Config.WorkDir+"\n也可以发送 /new /path/to/project 指定项目。")
	}
	c.mu.Lock()
	c.projectPicker = true
	c.mu.Unlock()
	mid, e := r.API.SendCard(r.ctx, NewText(id), r.projectCard(projects))
	if e != nil {
		return e
	}
	if credentialTruthy(mid) {
		return nil
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
		lines = append(lines, fmt.Sprintf("%d. **%s**%s\n   %s", i+1, p.String("projectName"), branch, p.String("realPath")))
	}
	return r.Notice(id, "选择项目（回复编号）：\n\n"+strings.Join(lines, "\n\n")+"\n\n💡 下次可直接 /new <编号、名称或绝对路径> 快速新建会话")
}
func feishuRuntimeObject(values ...any) protocol.JSONObject {
	fields := []protocol.JSONField{}
	for i := 0; i+1 < len(values); i += 2 {
		fields = append(fields, feishuField(values[i].(string), values[i+1]))
	}
	return feishuObject(fields...)
}
func (r *FeishuRuntime) projectCard(projects []protocol.JSONObject) []byte {
	object := feishuRuntimeObject
	elements := []any{}
	for i, p := range projects {
		if i >= 10 {
			break
		}
		branch := ""
		if p.String("branch") != "" {
			branch = "  ·  *" + p.String("branch") + "*"
		}
		margin, button := "10px 0 0 0", "default"
		if i == 0 {
			margin, button = "0px 0 0 0", "primary"
		}
		title := object("tag", "markdown", "content", "**"+p.String("projectName")+"**"+branch)
		location := object("tag", "markdown", "content", FeishuPrettyPath(NewText(p.String("realPath")), NewText(r.o.Config.Home), 56), "text_size", "notation", "margin", "2px 0 0 0")
		info := object("tag", "column", "width", "weighted", "weight", 1, "vertical_align", "center", "elements", []any{title, location})
		value := object("action", "pick_project", "realPath", p.String("realPath"), "projectName", p.String("projectName"))
		pick := object("tag", "button", "text", object("tag", "plain_text", "content", "选择"), "type", button, "size", "small", "value", value)
		action := object("tag", "column", "width", "auto", "vertical_align", "center", "elements", []any{pick})
		elements = append(elements, object("tag", "column_set", "flex_mode", "stretch", "horizontal_spacing", "8px", "margin", margin, "columns", []any{info, action}))
	}
	elements = append(elements, object("tag", "hr", "margin", "14px 0 0 0"), object("tag", "markdown", "content", "💡 点击右侧 **选择** 按钮，或发送 `/new <项目名>`", "text_size", "notation", "margin", "6px 0 0 0"))
	subtitle := fmt.Sprintf("共 %d 个最近项目", len(projects))
	if len(projects) > 10 {
		subtitle = fmt.Sprintf("共 %d 个最近项目，显示前 10", len(projects))
	}
	header := object("title", object("tag", "plain_text", "content", "📁 选择项目"), "subtitle", object("tag", "plain_text", "content", subtitle), "template", "blue")
	return raw(object("schema", "2.0", "config", object("wide_screen_mode", true, "update_multi", true), "header", header, "body", object("elements", elements)))
}
func (r *FeishuRuntime) status(id string) (string, error) {
	v, e := r.existing(id)
	if e != nil {
		return "", e
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
	if git, e := r.Client.Git(r.ctx, s.SessionID); e == nil {
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
	if tasks, e := r.Client.Tasks(r.ctx, s.SessionID); e == nil && len(tasks) > 0 {
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
