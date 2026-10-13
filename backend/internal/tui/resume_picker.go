package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

type ResumePickerSource interface {
	ReadResumePicker(context.Context, *bool, int) ([]protocol.JSONObject, bool, error)
	CurrentDirectory() string
}
type ResumePickerOptions struct {
	Input         io.Reader
	Output        io.Writer
	Environment   []string
	InitialSearch string
	Width, Height int
}
type ResumePickerResult struct {
	Cancelled    bool
	CrossProject bool
	Log          protocol.JSONObject
}
type resumePickerLoaded struct {
	rows  []protocol.JSONObject
	more  bool
	err   error
	reset bool
}
type resumePickerModel struct {
	source                    ResumePickerSource
	ctx                       context.Context
	rows                      []protocol.JSONObject
	more, loading             bool
	failure                   error
	allProjects, allWorktrees bool
	search                    string
	focus, width, height      int
	result                    ResumePickerResult
	pendingExit               time.Time
}

func (m *resumePickerModel) Init() tea.Cmd { return m.load(true) }
func (m *resumePickerModel) load(reset bool) tea.Cmd {
	m.loading = true
	all := m.allProjects
	return func() tea.Msg {
		var value *bool
		if reset {
			value = &all
		}
		rows, more, e := m.source.ReadResumePicker(m.ctx, value, 50)
		return resumePickerLoaded{rows, more, e, reset}
	}
}
func (m *resumePickerModel) visible() []protocol.JSONObject {
	out := []protocol.JSONObject{}
	projects := map[string]bool{}
	for _, log := range m.rows {
		if p := log.String("projectPath"); p != "" {
			projects[p] = true
		}
	}
	query := strings.ToLower(m.search)
	for _, log := range m.rows {
		if len(projects) > 1 && !m.allProjects && !m.allWorktrees && log.String("projectPath") != m.source.CurrentDirectory() {
			continue
		}
		if query != "" {
			pr := ""
			if p := log.Get("prNumber"); len(p) > 0 {
				pr = "pr #" + string(p) + " " + log.String("prRepository")
			}
			haystacks := []string{resumeSourceDisplayTitle(log), log.String("gitBranch"), log.String("tag"), pr}
			match := false
			for _, h := range haystacks {
				if strings.Contains(strings.ToLower(h), query) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, log)
	}
	return out
}
func (m *resumePickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch v := message.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	case resumePickerLoaded:
		m.loading = false
		m.failure = v.err
		m.rows = v.rows
		m.more = v.more
		if v.reset {
			m.focus = 0
		}
		if v.err != nil {
			return m, tea.Quit
		}
	case tea.KeyPressMsg:
		if m.loading {
			if v.String() == "ctrl+c" || v.String() == "esc" {
				m.result.Cancelled = true
				return m, tea.Quit
			}
			return m, nil
		}
		rows := m.visible()
		switch v.String() {
		case "esc":
			if m.search != "" {
				m.search = ""
				m.focus = 0
				return m, nil
			}
			m.result.Cancelled = true
			return m, tea.Quit
		case "ctrl+c", "ctrl+d":
			if len(rows) == 0 || !m.pendingExit.IsZero() && time.Since(m.pendingExit) < time.Second {
				m.result.Cancelled = true
				return m, tea.Quit
			}
			m.pendingExit = time.Now()
		case "up":
			if m.focus > 0 {
				m.focus--
			}
		case "down":
			if m.focus+1 < len(rows) {
				m.focus++
			} else if m.more {
				return m, m.load(false)
			}
		case "pgdown":
			m.focus = min(max(0, len(rows)-1), m.focus+max(1, m.height-8))
			if m.more && m.focus == len(rows)-1 {
				return m, m.load(false)
			}
		case "pgup":
			m.focus = max(0, m.focus-max(1, m.height-8))
		case "ctrl+a":
			m.allProjects = !m.allProjects
			return m, m.load(true)
		case "ctrl+w":
			m.allWorktrees = !m.allWorktrees
			m.focus = 0
		case "enter":
			if len(rows) > 0 {
				m.focus = min(m.focus, len(rows)-1)
				m.result.Log = rows[m.focus].Clone()
				if source, ok := m.source.(interface {
					IsCrossProject(protocol.JSONObject, bool) bool
				}); ok {
					m.result.CrossProject = source.IsCrossProject(m.result.Log, m.allProjects)
				}
				return m, tea.Quit
			}
		case "backspace":
			runes := []rune(m.search)
			if len(runes) > 0 {
				m.search = string(runes[:len(runes)-1])
				m.focus = 0
			}
		default:
			if v.Key().Text != "" {
				m.search += v.Key().Text
				m.focus = 0
			}
		}
	}
	return m, nil
}
func (m *resumePickerModel) View() tea.View {
	if m.loading {
		return tea.NewView(" Loading conversations…\n")
	}
	rows := m.visible()
	if len(rows) == 0 {
		return tea.NewView("No conversations found to resume.\nPress Ctrl+C to exit and start a new conversation.\n")
	}
	var b strings.Builder
	b.WriteString("Resume session\n\n")
	if m.search != "" {
		fmt.Fprintf(&b, "Search: %s\n\n", displayText(m.search))
	}
	count := max(1, m.height-8)
	m.focus = min(max(0, m.focus), len(rows)-1)
	from := max(0, m.focus-count+1)
	to := min(len(rows), from+count)
	for i := from; i < to; i++ {
		prefix := "  "
		if i == m.focus {
			prefix = "❯ "
		}
		label := displayText(resumeSourceDisplayTitle(rows[i]))
		if p := rows[i].Get("prNumber"); len(p) > 0 {
			label += "  PR #" + string(p)
		}
		fmt.Fprintf(&b, "%s%d. %s\n", prefix, i+1, label)
	}
	b.WriteString("\n↑/↓ select · Enter resume · Type to search · Esc cancel\nCtrl+A show ")
	if m.allProjects {
		b.WriteString("current dir")
	} else {
		b.WriteString("all projects")
	}
	b.WriteString(" · Ctrl+W toggle worktrees\n")
	if !m.pendingExit.IsZero() {
		b.WriteString("Press Ctrl+C again to exit\n")
	}
	return tea.NewView(b.String())
}
func RunResumePicker(ctx context.Context, source ResumePickerSource, o ResumePickerOptions) (result ResumePickerResult, err error) {
	if source == nil {
		return result, errors.New("resume picker requires actual session discovery")
	}
	if o.Input == nil {
		o.Input = os.Stdin
	}
	if o.Output == nil {
		o.Output = os.Stdout
	}
	terminals, e := captureTerminals(o.Input, o.Output)
	if e != nil {
		return result, e
	}
	defer func() {
		for _, t := range terminals {
			err = errors.Join(err, term.Restore(t.file.Fd(), t.state))
		}
	}()
	model := &resumePickerModel{source: source, ctx: ctx, search: o.InitialSearch, width: max(o.Width, 80), height: max(o.Height, 24)}
	program := tea.NewProgram(model, tea.WithContext(context.WithoutCancel(ctx)), tea.WithInput(o.Input), tea.WithOutput(o.Output), tea.WithEnvironment(o.Environment), tea.WithWindowSize(model.width, model.height))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			program.Quit()
		case <-done:
		}
	}()
	_, err = program.Run()
	if err == nil {
		err = model.failure
	}
	if err == nil && ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	return model.result, err
}
