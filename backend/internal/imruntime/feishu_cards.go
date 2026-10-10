package imruntime

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"path"
	"regexp"
	"strings"
)

func FeishuPrettyPath(real, home Text, max int) Text {
	p := real
	if len(home) > 0 {
		if string(raw(p)) == string(raw(home)) {
			return NewText("~")
		}
		prefix := home.Join(NewText("/"))
		if p.HasPrefix(prefix) {
			p = NewText("~").Join(p.Slice(len(home), len(p)))
		}
	}
	if len(p) <= max {
		return p
	}
	tail := int(math.Floor(float64(max) * 0.65))
	head := max - tail - 1
	return p.Slice(0, head).Join(NewText("…")).Join(p.Slice(len(p)-tail, len(p)))
}
func feishuTargetPreview(t Text, max int) Text {
	if len(t) <= max {
		return t
	}
	return t.Slice(0, max-1).Join(NewText("…"))
}
func feishuStringField(p protocol.JSONObject, key string) *Text {
	if v := p.Get(key); isString(v) {
		t := TextJSON(v)
		return &t
	}
	return nil
}
func feishuWindowsPath(p string) bool {
	return regexp.MustCompile(`^[a-zA-Z]:[\\/]`).MatchString(p) || strings.HasPrefix(p, `\\`)
}
func feishuWindowsNormalize(p string) (string, []string) {
	p = strings.ReplaceAll(p, `\`, "/")
	root := ""
	rest := p
	if len(p) >= 3 && p[1] == ':' && p[2] == '/' {
		root = strings.ToLower(p[:2])
		rest = p[3:]
	} else if strings.HasPrefix(p, "//") {
		segments := strings.Split(strings.TrimLeft(p, "/"), "/")
		if len(segments) >= 2 {
			root = "//" + strings.ToLower(segments[0]+"/"+segments[1])
			rest = strings.Join(segments[2:], "/")
		}
	} else if strings.HasPrefix(p, "/") {
		root = "/"
		rest = strings.TrimLeft(p, "/")
	}
	clean := path.Clean("/" + rest)
	parts := []string{}
	for _, part := range strings.Split(strings.Trim(clean, "/"), "/") {
		if part != "" && part != "." {
			parts = append(parts, strings.ToLower(part))
		}
	}
	return root, parts
}
func FeishuOutsideWorkDir(file, work string) bool {
	if feishuWindowsPath(file) || feishuWindowsPath(work) {
		wr, wp := feishuWindowsNormalize(work)
		if !feishuWindowsPath(file) && !strings.HasPrefix(file, `\`) && !strings.HasPrefix(file, "/") {
			file = strings.TrimRight(work, `\/`) + "/" + file
		}
		fr, fp := feishuWindowsNormalize(file)
		if fr != wr {
			return true
		}
		same := 0
		for same < len(wp) && same < len(fp) && wp[same] == fp[same] {
			same++
		}
		if same < len(wp) {
			return true
		}
		return same < len(fp) && strings.HasPrefix(fp[same], "..")
	}
	abs := file
	if !path.IsAbs(file) {
		abs = path.Join(work, file)
	} else {
		abs = path.Clean(file)
	}
	norm := path.Clean(work)
	if abs == norm {
		return false
	}
	base := strings.TrimRight(norm, "/") + "/"
	if !strings.HasPrefix(abs, base) {
		return true
	}
	return strings.HasPrefix(strings.TrimPrefix(abs, base), "..")
}
func FeishuPermissionCard(tool Text, input json.RawMessage, request Text, work, home Text) json.RawMessage {
	p := object(input)
	icon, label := "🔧", tool
	var target, file *Text
	switch tool.String() {
	case "Write":
		icon = "✏️"
		label = NewText("写入文件")
		file = feishuStringField(p, "file_path")
		target = file
	case "Edit", "MultiEdit", "NotebookEdit":
		icon = "✏️"
		label = NewText("修改文件")
		file = feishuStringField(p, "file_path")
		if file == nil {
			file = feishuStringField(p, "notebook_path")
		}
		target = file
	case "Read":
		icon = "📖"
		label = NewText("读取文件")
		file = feishuStringField(p, "file_path")
		target = file
	case "Bash", "BashOutput":
		icon = "🖥️"
		label = NewText("执行命令")
		target = feishuStringField(p, "command")
	case "Grep":
		icon = "🔍"
		label = NewText("搜索内容")
		pattern := feishuStringField(p, "pattern")
		if pattern != nil && len(*pattern) > 0 {
			t := NewText("pattern: ").Join(*pattern)
			target = &t
		}
		file = feishuStringField(p, "path")
	case "Glob":
		icon = "📁"
		label = NewText("查找文件")
		pattern := feishuStringField(p, "pattern")
		if pattern != nil && len(*pattern) > 0 {
			t := NewText("pattern: ").Join(*pattern)
			target = &t
		}
		file = feishuStringField(p, "path")
	case "WebFetch":
		icon = "🌐"
		label = NewText("访问网页")
		target = feishuStringField(p, "url")
	case "WebSearch":
		icon = "🌐"
		label = NewText("搜索网页")
		target = feishuStringField(p, "query")
	}
	cross := len(work) > 0 && file != nil && len(*file) > 0 && FeishuOutsideWorkDir(file.String(), work.String())
	elements := []any{feishuObject(feishuField("tag", "markdown"), feishuField("content", NewText(icon+" **").Join(label).Join(NewText("**  `")).Join(tool).Join(NewText("`"))))}
	if target != nil && len(*target) > 0 {
		shown := *target
		if file != nil && len(*file) > 0 {
			shown = FeishuPrettyPath(shown, home, 80)
		} else {
			shown = feishuTargetPreview(shown, 160)
		}
		elements = append(elements, feishuObject(feishuField("tag", "markdown"), feishuField("content", NewText("```\n").Join(shown).Join(NewText("\n```"))), feishuField("margin", "4px 0 0 0")))
	}
	if cross {
		elements = append(elements, feishuObject(feishuField("tag", "markdown"), feishuField("content", "⚠️ **该操作位于当前项目目录之外**"), feishuField("margin", "8px 0 0 0"), feishuField("text_size", "notation")))
	}
	elements = append(elements, feishuObject(feishuField("tag", "hr"), feishuField("margin", "12px 0 0 0")))
	columns := []any{}
	for _, b := range []struct {
		text, kind    string
		allow, always bool
	}{{"✅ 允许", "primary", true, false}, {"♾️ 永久允许", "default", true, true}, {"❌ 拒绝", "danger", false, false}} {
		value := feishuObject(feishuField("action", "permit"), feishuField("requestId", request), feishuField("allowed", b.allow))
		if b.always {
			value.Set("rule", raw("always"))
		}
		columns = append(columns, feishuObject(feishuField("tag", "column"), feishuField("width", "weighted"), feishuField("weight", 1), feishuField("vertical_align", "center"), feishuField("elements", []any{feishuObject(feishuField("tag", "button"), feishuField("text", feishuObject(feishuField("tag", "plain_text"), feishuField("content", b.text))), feishuField("type", b.kind), feishuField("size", "medium"), feishuField("value", value))})))
	}
	elements = append(elements, feishuObject(feishuField("tag", "column_set"), feishuField("flex_mode", "stretch"), feishuField("horizontal_spacing", "8px"), feishuField("margin", "8px 0 0 0"), feishuField("columns", columns)))
	subtitle := tool
	template := "orange"
	if cross {
		subtitle = NewText("⚠️ 跨目录操作")
		template = "red"
	}
	return raw(feishuObject(feishuField("schema", "2.0"), feishuField("config", feishuObject(feishuField("wide_screen_mode", false), feishuField("update_multi", true))), feishuField("header", feishuObject(feishuField("title", feishuObject(feishuField("tag", "plain_text"), feishuField("content", "🔐 需要权限确认"))), feishuField("subtitle", feishuObject(feishuField("tag", "plain_text"), feishuField("content", subtitle))), feishuField("template", template), feishuField("padding", "12px 12px 12px 12px"), feishuField("icon", feishuObject(feishuField("tag", "standard_icon"), feishuField("token", "lock-chat_filled"))))), feishuField("body", feishuObject(feishuField("elements", elements)))))
}
