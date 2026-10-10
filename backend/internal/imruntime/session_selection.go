package imruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func canonicalPath(path string) string {
	if resolved := existingDir(path, ""); resolved != "" {
		return resolved
	}
	resolved, _ := filepath.Abs(path)
	return resolved
}

var oneLineSpace = regexp.MustCompile(`[\r\n\t]+`)

func OneLine(text string, limit int) string {
	return NewText(oneLineSpace.ReplaceAllString(text, " ")).Slice(0, limit).String()
}

type SessionSelectionDeps struct {
	Client        *HTTPClient
	Bridge        *Bridge
	Store         *SessionStore
	Handler       func(string) BridgeHandler
	Clear         func(string)
	Busy          func(string) bool
	Send          func(string, string) error
	ClearProjects func(string)
	Now           func() time.Time
}

func RestoreSelectedSession(ctx context.Context, deps SessionSelectionDeps, chat string, session protocol.JSONObject) (bool, string) {
	busy := func() bool { return deps.Busy != nil && deps.Busy(chat) }
	if busy() {
		return false, "当前会话正在运行或等待审批，请先处理审批或发送 /stop，等停止后再切换。"
	}
	exists, err := deps.Client.SessionExists(ctx, session.String("id"))
	if err != nil {
		return false, "无法检查会话，当前绑定未改变：" + err.Error() + "。请重试。"
	}
	if session.String("workDir") == "" || !exists {
		return false, "该会话已不存在、目录不可用或不允许通过 IM 访问。请发送 /sessions 刷新列表。"
	}
	if busy() {
		return false, "当前会话正在运行，请先发送 /stop，等停止后再切换。"
	}
	previous := deps.Store.Get(chat)
	var mu sync.Mutex
	buffered := []protocol.JSONObject{}
	buffering := true
	deps.Bridge.Reset(chat)
	handler := deps.Handler(chat)
	deps.Bridge.Connect(chat, session.String("id"), func(ctx context.Context, message protocol.JSONObject) error {
		mu.Lock()
		if buffering {
			buffered = append(buffered, message)
			mu.Unlock()
			return nil
		}
		mu.Unlock()
		return handler(ctx, message)
	})
	if !deps.Bridge.WaitOpen(ctx, chat) {
		err = fmt.Errorf("连接服务器超时")
	} else {
		err = deps.Store.Set(chat, session.String("id"), session.String("workDir"))
	}
	if err != nil {
		deps.Bridge.Reset(chat)
		if previous != nil {
			deps.Bridge.Connect(chat, previous.SessionID, handler)
		}
		return false, "恢复失败，已保留原会话绑定：" + err.Error() + "。请重试。"
	}
	deps.Clear(chat)
	mu.Lock()
	buffering = false
	messages := append([]protocol.JSONObject{}, buffered...)
	mu.Unlock()
	deps.Bridge.SetHandler(chat, handler)
	for _, message := range messages {
		_ = handler(ctx, message)
	}
	title := session.String("title")
	if title == "" {
		title = "未命名会话"
	}
	return true, "已恢复会话：" + OneLine(title, 80) + "\n" + session.String("workDir") + "\n会话 ID：" + session.String("id") + "\n可以继续发送消息。"
}

type sessionPicker struct {
	Kind               string
	Projects, Sessions []protocol.JSONObject
	Project            string
	Page               int
	Expires            time.Time
}
type SessionSelection struct {
	Deps    SessionSelectionDeps
	Pickers map[string]*sessionPicker
}

func NewSessionSelection(deps SessionSelectionDeps) *SessionSelection {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &SessionSelection{deps, map[string]*sessionPicker{}}
}
func (s *SessionSelection) Clear(chat string) { delete(s.Pickers, chat) }

var sessionsCommand = regexp.MustCompile(`^(?:/sessions|会话列表)(?:\s+(.*))?$`)
var resumeCommand = regexp.MustCompile(`^(?:/resume|继续会话)(?:\s+(.*))?$`)
var digitReply = regexp.MustCompile(`^\d+$`)

func (s *SessionSelection) Handle(ctx context.Context, chat, input string) (bool, error) {
	text := protocol.TrimJavaScriptString(input)
	list, resume := sessionsCommand.FindStringSubmatch(text), resumeCommand.FindStringSubmatch(text)
	cancel := text == "/cancel" || text == "取消选择"
	picker := s.Pickers[chat]
	number := digitReply.MatchString(text) && picker != nil
	if list == nil && resume == nil && !cancel && !number {
		if text != "" && !strings.HasPrefix(text, "/") && text != "帮助" && text != "状态" && text != "停止" && text != "清空" {
			s.Clear(chat)
		}
		return false, nil
	}
	err := func() error {
		if cancel {
			s.Clear(chat)
			s.Deps.ClearProjects(chat)
			return s.Deps.Send(chat, "已取消选择，当前会话未改变。")
		}
		if list != nil && (list[1] == "next" || list[1] == "prev") {
			if !s.require(chat, picker) {
				return nil
			}
			count := len(picker.Projects)
			if picker.Kind == "sessions" {
				count = len(picker.Sessions)
			}
			next := picker.Page - 1
			if list[1] == "next" {
				next = picker.Page + 1
			}
			last := (count+7)/8 - 1
			if next > last {
				next = last
			}
			if next < 0 {
				next = 0
			}
			picker.Page = next
			return s.show(chat, picker)
		}
		if list != nil || resume != nil && resume[1] == "" {
			s.Clear(chat)
			s.Deps.ClearProjects(chat)
			query := ""
			if list != nil {
				query = list[1]
			}
			return s.open(ctx, chat, query)
		}
		if !s.require(chat, picker) {
			return nil
		}
		query := text
		if resume != nil {
			query = resume[1]
		}
		index := -1
		if digitReply.MatchString(query) {
			var n int
			_, _ = fmt.Sscan(query, &n)
			index = n - 1
		}
		visible := index >= picker.Page*8 && index < picker.Page*8+8
		if picker.Kind == "projects" {
			if visible && index < len(picker.Projects) {
				return s.openProject(ctx, chat, picker.Projects[index].String("realPath"), nil)
			}
		} else if visible && index < len(picker.Sessions) {
			ok, message := RestoreSelectedSession(ctx, s.Deps, chat, picker.Sessions[index])
			if ok {
				s.Clear(chat)
			}
			return s.Deps.Send(chat, message)
		}
		return s.Deps.Send(chat, "编号无效，请使用当前页显示的编号，或发送 /sessions 刷新列表。")
	}()
	if err != nil {
		return true, s.Deps.Send(chat, "无法恢复会话："+err.Error()+"。请重试 /sessions。")
	}
	return true, nil
}
func (s *SessionSelection) require(chat string, picker *sessionPicker) bool {
	if picker != nil && picker.Expires.After(s.Deps.Now()) {
		return true
	}
	s.Clear(chat)
	_ = s.Deps.Send(chat, "选择列表不存在或已过期，请发送 /sessions 重新选择。")
	return false
}
func (s *SessionSelection) open(ctx context.Context, chat, query string) error {
	if query == "projects" {
		projects, err := s.Deps.Client.Projects(ctx)
		if err != nil {
			return err
		}
		return s.openProjects(chat, projects)
	}
	if query != "" {
		project, ambiguous, err := s.Deps.Client.MatchProject(ctx, query)
		if err != nil {
			return err
		}
		if project != nil {
			return s.openProject(ctx, chat, project.String("realPath"), nil)
		}
		if len(ambiguous) > 0 {
			return s.openProjects(chat, ambiguous)
		}
		return s.Deps.Send(chat, "未找到项目“"+OneLine(query, 100)+"”。发送 /sessions projects 选择项目，或 /sessions <绝对路径>。")
	}
	if current := s.Deps.Store.Get(chat); current != nil {
		sessions, err := s.Deps.Client.SessionHistory(ctx)
		if err != nil {
			return err
		}
		root := current.WorkDir
		for _, session := range sessions {
			if session.String("id") == current.SessionID && session.String("projectRoot") != "" {
				root = session.String("projectRoot")
				break
			}
		}
		return s.openProject(ctx, chat, root, sessions)
	}
	projects, err := s.Deps.Client.Projects(ctx)
	if err != nil {
		return err
	}
	return s.openProjects(chat, projects)
}
func (s *SessionSelection) openProjects(chat string, projects []protocol.JSONObject) error {
	if len(projects) == 0 {
		return s.Deps.Send(chat, "没有可访问的历史项目。发送 /new 新建会话，或 /sessions <项目绝对路径> 查找旧会话。")
	}
	picker := &sessionPicker{Kind: "projects", Projects: projects, Expires: s.Deps.Now().Add(15 * time.Minute)}
	s.Pickers[chat] = picker
	return s.show(chat, picker)
}
func (s *SessionSelection) openProject(ctx context.Context, chat, project string, all []protocol.JSONObject) error {
	sessions, err := s.Deps.Client.ProjectHistory(ctx, project, all)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		s.Clear(chat)
		return s.Deps.Send(chat, "该项目没有可恢复会话："+project+"\n发送 /sessions projects 选择其他项目，或 /new <项目> 新建会话。")
	}
	picker := &sessionPicker{Kind: "sessions", Project: project, Sessions: sessions, Expires: s.Deps.Now().Add(15 * time.Minute)}
	s.Pickers[chat] = picker
	return s.show(chat, picker)
}
func (s *SessionSelection) show(chat string, p *sessionPicker) error {
	start := p.Page * 8
	items := p.Projects
	title := "选择历史会话所在的项目："
	suffix := "回复编号查看会话。"
	if p.Kind == "sessions" {
		items = p.Sessions
		title = "历史会话：" + p.Project
		suffix = "回复编号或 /resume <编号> 继续旧会话。"
	}
	current := s.Deps.Store.Get(chat)
	currentID := ""
	if current != nil {
		currentID = current.SessionID
	}
	lines := []string{title, fmt.Sprintf("第 %d/%d 页", p.Page+1, (len(items)+7)/8), ""}
	end := start + 8
	if end > len(items) {
		end = len(items)
	}
	for i := start; i < end; i++ {
		item := items[i]
		line := fmt.Sprintf("%d. %s\n%s", i+1, OneLine(item.String("projectName"), 60), OneLine(item.String("realPath"), 180))
		if p.Kind == "sessions" {
			name := item.String("title")
			if name == "" {
				name = "未命名会话"
			}
			selected := ""
			if item.String("id") == currentID {
				selected = "（当前）"
			}
			line = fmt.Sprintf("%d. %s%s\n%s · %s 条消息 · %s", i+1, OneLine(name, 60), selected, strings.Replace(NewText(item.String("modifiedAt")).Slice(0, 16).String(), "T", " ", 1), string(item.Get("messageCount")), NewText(item.String("id")).Slice(0, 8).String())
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", suffix, "翻页：/sessions next、/sessions prev", "/cancel 取消；列表 15 分钟内有效。")
	return s.Deps.Send(chat, strings.Join(lines, "\n"))
}
