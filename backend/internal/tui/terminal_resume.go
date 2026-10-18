package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
)

func (p *localPanelState) resumeView() protocol.JSONObject {
	var view protocol.JSONObject
	_ = json.Unmarshal(p.props.Presentation, &view)
	return view
}
func (p *localPanelState) resumeUI() *terminalResumeUI {
	if p.terminalResume == nil {
		p.terminalResume = &terminalResumeUI{mode: "list"}
	}
	return p.terminalResume
}
func (p *localPanelState) retainTerminalResumeState(old *localPanelState) {
	if old == nil || old.props.ID != p.props.ID || p.props.Kind != "resume" || old.props.Kind != "resume" {
		return
	}
	view := p.resumeView()
	prior := old.resumeView()
	if string(view.Get("loading")) == "true" || string(view.Get("resuming")) == "true" || string(prior.Get("loading")) == "true" {
		return
	}
	p.terminalResume = old.terminalResume
	p.mcpExitTimes, p.mcpExitPending = old.mcpExitTimes, old.mcpExitPending
}
func (p *localPanelState) resumeLogs() []protocol.JSONObject {
	var rows []protocol.JSONObject
	_ = json.Unmarshal(p.resumeView().Get("logs"), &rows)
	return rows
}
func terminalResumeTags(logs []protocol.JSONObject) []string {
	seen := map[string]bool{}
	tags := []string{}
	for _, log := range logs {
		if tag := log.String("tag"); tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	c, err := sourcecollation.New()
	if err == nil {
		defer c.Close()
		sort.SliceStable(tags, func(i, j int) bool {
			return c.Compare(utf16.Encode([]rune(tags[i])), utf16.Encode([]rune(tags[j]))) < 0
		})
	}
	return tags
}
func (p *localPanelState) visibleTerminalResume() []int {
	u := p.resumeUI()
	view := p.resumeView()
	logs := p.resumeLogs()
	tags := terminalResumeTags(logs)
	query := lowerJS(u.search.Text())
	out := []int{}
	for i, log := range logs {
		if u.tag > 0 && u.tag <= len(tags) && log.String("tag") != tags[u.tag-1] {
			continue
		}
		if u.branchOnly && view.String("currentBranch") != "" && log.String("gitBranch") != view.String("currentBranch") {
			continue
		}
		if string(view.Get("hasMultipleWorktrees")) == "true" && !u.allWorktrees && log.String("projectPath") != view.String("currentDirectory") {
			continue
		}
		if query != "" {
			pr := ""
			if v := log.Get("prNumber"); len(v) > 0 && string(v) != "0" {
				pr = "pr #" + string(v) + " " + log.String("prRepository")
			}
			matched := false
			for _, text := range []string{resumeSourceDisplayTitle(log), log.String("gitBranch"), log.String("tag"), pr} {
				if strings.Contains(lowerJS(text), query) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}
func terminalResumeEdit(e *Editor, key Key) {
	switch key.Name {
	case "backspace":
		e.Backspace()
	case "delete":
		e.Delete()
	case "left":
		e.Left()
	case "right":
		e.Right()
	case "home", "ctrl+a":
		e.Home()
	case "end", "ctrl+e":
		e.End()
	case "ctrl+u":
		e.KillToStart()
	case "ctrl+k":
		e.KillToEnd()
	default:
		if key.Text != "" {
			e.Insert(key.Text)
		}
	}
}
func (s *State) handleTerminalResumeKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "resume" {
		return false, nil
	}
	u := p.resumeUI()
	view := p.resumeView()
	actionFor := func(name, row string, fields map[string]string) []action {
		return []action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: p.props.ID, Action: name, Row: row, Fields: fields}}}
	}
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		if p.mcpExitTimes == nil {
			p.mcpExitTimes = map[string]time.Time{}
		}
		if previous, ok := p.mcpExitTimes[key.Name]; ok && !now.Before(previous) && now.Sub(previous) <= doublePressWindow {
			return true, s.EOF()
		}
		p.mcpExitTimes[key.Name] = now
		name := "Ctrl-D"
		if key.Name == "ctrl+c" {
			name = "Ctrl-C"
		}
		p.mcpExitPending = "Press " + name + " again to exit"
		return true, nil
	}
	contexts := []string{"Select"}
	if u.mode == "preview" {
		contexts = []string{"Confirmation"}
	} else if u.mode == "rename" {
		contexts = []string{"Settings"}
	} else if u.mode == "search" {
		contexts = nil
	}
	resolution := BindingResolution{}
	// Literal TextInput characters are consumed before Settings/navigation
	// bindings. Only the active original view registers cancellation contexts.
	if key.Text == "" || u.mode == "list" || u.mode == "preview" {
		resolution = ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	}
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	}
	s.pendingChord = nil
	dismiss := resolution.Type == "match" && (resolution.Action == "confirm:no" || resolution.Action == "select:cancel")
	if dismiss || key.Name == "esc" && u.mode == "search" {
		if u.mode == "preview" || u.mode == "rename" {
			u.mode = "list"
			u.rename.Reset()
			return true, nil
		}
		if u.mode == "search" {
			if u.search.Text() != "" {
				u.search.Reset()
			} else {
				u.mode = "list"
			}
			u.focused = 0
			return true, nil
		}
		s.LocalPanel = nil
		return true, actionFor("resume-cancel", "", nil)
	}
	if string(view.Get("loading")) == "true" || string(view.Get("resuming")) == "true" {
		return true, nil
	}
	rows := p.visibleTerminalResume()
	u.focused = min(max(0, u.focused), max(0, len(rows)-1))
	selected := ""
	if len(rows) > 0 {
		selected = strconv.Itoa(rows[u.focused])
	}
	if u.mode == "rename" {
		if key.Name == "enter" {
			title := protocol.TrimJavaScriptString(u.rename.Text())
			u.mode = "list"
			u.rename.Reset()
			if title != "" {
				return true, actionFor("resume-rename", selected, map[string]string{"title": title})
			}
			return true, nil
		}
		terminalResumeEdit(&u.rename, key)
		return true, nil
	}
	if u.mode == "preview" {
		if resolution.Type == "match" && resolution.Action == "confirm:yes" || key.Name == "enter" {
			return true, actionFor("resume-preview-select", selected, nil)
		}
		return true, nil
	}
	if u.mode == "search" {
		if key.Name == "enter" || key.Name == "down" || key.Name == "up" || key.Name == "ctrl+n" || key.Name == "backspace" && u.search.Text() == "" {
			u.mode = "list"
			return true, nil
		}
		terminalResumeEdit(&u.search, key)
		u.focused = 0
		return true, nil
	}
	tags := terminalResumeTags(p.resumeLogs())
	if key.Name == "tab" || key.Name == "shift+tab" {
		if len(tags) > 0 {
			offset := 1
			if key.Name == "shift+tab" {
				offset = -1
			}
			u.tag = (u.tag + len(tags) + 1 + offset) % (len(tags) + 1)
			u.focused = 0
		}
		return true, nil
	}
	switch key.Name {
	case "ctrl+a":
		return true, actionFor("resume-all", "", nil)
	case "ctrl+b":
		u.branchOnly = !u.branchOnly
		u.focused = 0
		return true, nil
	case "ctrl+w":
		if string(view.Get("hasMultipleWorktrees")) == "true" {
			u.allWorktrees = !u.allWorktrees
			u.focused = 0
		}
		return true, nil
	case "ctrl+r":
		if selected != "" {
			u.mode = "rename"
			u.rename.Reset()
		}
		return true, nil
	case "ctrl+v":
		if selected != "" {
			// SessionPreview is a new React mount. A previous preview's full
			// log cannot be painted while this mount's async read is pending.
			view.Delete("preview")
			p.props.Presentation, _ = view.MarshalJSON()
			u.mode = "preview"
			return true, actionFor("resume-preview", selected, nil)
		}
		return true, nil
	}
	name := key.Name
	if resolution.Type == "match" {
		switch resolution.Action {
		case "select:next", "confirm:next":
			name = "down"
		case "select:previous", "confirm:previous":
			name = "up"
		case "select:accept", "confirm:yes":
			name = "enter"
		}
	}
	switch name {
	case "up", "ctrl+p":
		if u.focused == 0 {
			u.mode = "search"
		} else {
			u.focused--
		}
	case "down", "ctrl+n":
		u.focused = min(max(0, len(rows)-1), u.focused+1)
	case "home":
		u.focused = 0
	case "end":
		u.focused = max(0, len(rows)-1)
	case "pageup":
		u.focused = max(0, u.focused-max(1, (s.Height-12)/3))
	case "pagedown":
		u.focused = min(max(0, len(rows)-1), u.focused+max(1, (s.Height-12)/3))
	case "enter":
		if selected != "" {
			return true, actionFor("resume-select", selected, nil)
		}
	default:
		normalized := key.Text
		normalized = strings.Map(func(r rune) rune {
			if r >= '０' && r <= '９' {
				return r - '０' + '0'
			}
			return r
		}, normalized)
		if len(normalized) > 0 && strings.Trim(normalized, "0123456789") == "" {
			if n, err := strconv.Atoi(normalized); err == nil && n > 0 && n <= len(rows) {
				return true, actionFor("resume-select", strconv.Itoa(rows[n-1]), nil)
			}
		}
		if key.Text == "/" {
			u.mode = "search"
		} else if protocol.TrimJavaScriptString(key.Text) != "" {
			u.mode = "search"
			u.search.Set(key.Text)
			u.focused = 0
		}
	}
	return true, nil
}
func terminalResumeDate(raw json.RawMessage) time.Time {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return time.UnixMilli(int64(n))
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := time.Parse(time.RFC3339Nano, str)
		return v
	}
	return time.Time{}
}
func terminalResumeMetadata(log protocol.JSONObject, now time.Time) string {
	seconds := math.Trunc(terminalResumeDate(log.Get("modified")).Sub(now).Seconds())
	relative := "0 sec. ago"
	for _, v := range []struct {
		unit    string
		seconds float64
	}{{"year", 31536000}, {"month", 2592000}, {"week", 604800}, {"day", 86400}, {"hour", 3600}, {"minute", 60}, {"second", 1}} {
		if math.Abs(seconds) >= v.seconds {
			n := math.Trunc(seconds / v.seconds)
			unit := v.unit
			if math.Abs(n) != 1 {
				unit += "s"
			}
			if n < 0 {
				relative = fmt.Sprintf("%.0f %s ago", math.Abs(n), unit)
			} else {
				relative = fmt.Sprintf("in %.0f %s", n, unit)
			}
			break
		}
	}
	size := string(log.Get("messageCount")) + " messages"
	if len(log.Get("fileSize")) > 0 {
		var n float64
		_ = json.Unmarshal(log.Get("fileSize"), &n)
		size = presentationFileSize(n)
	}
	parts := []string{relative}
	if branch := log.String("gitBranch"); branch != "" {
		parts = append(parts, branch)
	}
	parts = append(parts, size)
	if tag := log.String("tag"); tag != "" {
		parts = append(parts, "#"+tag)
	}
	if agent := log.String("agentSetting"); agent != "" {
		parts = append(parts, "@"+agent)
	}
	if pr := log.Get("prNumber"); len(pr) > 0 && string(pr) != "0" {
		parts = append(parts, log.String("prRepository")+"#"+string(pr))
	}
	return strings.Join(parts, " · ")
}
func (p *localPanelState) terminalResumeLines(width, height int, now time.Time, bindings []Keybinding) []string {
	view := p.resumeView()
	if string(view.Get("loading")) == "true" {
		return []string{"Loading conversations…"}
	}
	if string(view.Get("resuming")) == "true" {
		return []string{"Resuming conversation…"}
	}
	u := p.resumeUI()
	logs := p.resumeLogs()
	rows := p.visibleTerminalResume()
	tags := terminalResumeTags(logs)
	u.focused = min(max(0, u.focused), max(0, len(rows)-1))
	dismiss := "Esc"
	if value, ok := BindingDisplay("confirm:no", "Confirmation", bindings); ok {
		dismiss = value
	}
	if u.mode == "preview" {
		var log protocol.JSONObject
		_ = json.Unmarshal(view.Get("preview"), &log)
		if !log.IsObject() {
			return []string{"Loading session…", dismiss + " cancel"}
		}
		var records []protocol.JSONObject
		_ = json.Unmarshal(log.Get("messages"), &records)
		state := NewState(nil, 0, 0)
		state.SetInitialHistory(records, true)
		out := []string{resumeSourceDisplayTitle(log), ""}
		for _, entry := range state.Entries {
			out = append(out, wrap(entry.Text, max(1, width-4))...)
		}
		out = out[:min(len(out), max(1, height-5))]
		return append(out, "", "Enter resume · "+dismiss+" back")
	}
	if u.mode == "rename" {
		return []string{"Rename session:", u.rename.Text(), "", "Enter save · " + dismiss + " cancel"}
	}
	out := []string{"Resume Session"}
	if len(tags) > 0 {
		labels := []string{"All"}
		labels = append(labels, tags...)
		for i, label := range labels {
			if i == u.tag {
				labels[i] = "[" + label + "]"
			}
		}
		out[0] = strings.Join(labels, "  ")
	}
	out = append(out, "", "Search: "+u.search.Text(), "")
	count := max(1, (height-12)/3)
	from := max(0, u.focused-count+1)
	to := min(len(rows), from+count)
	for i := from; i < to; i++ {
		log := logs[rows[i]]
		prefix := "  "
		if i == u.focused {
			prefix = "❯ "
		}
		out = append(out, prefix+strconv.Itoa(i+1)+". "+displayText(strings.Join(strings.Fields(resumeSourceDisplayTitle(log)), " ")))
		metadata := terminalResumeMetadata(log, now)
		if string(view.Get("allProjects")) == "true" && log.String("projectPath") != "" {
			metadata += " · " + log.String("projectPath")
		}
		out = append(out, "   "+displayText(metadata), "")
	}
	if len(rows) == 0 {
		out = append(out, "No sessions match your search.")
	}
	guide := "Ctrl+A show all projects"
	if string(view.Get("allProjects")) == "true" {
		guide = "Ctrl+A show current dir"
	}
	if view.String("currentBranch") != "" {
		guide += " · Ctrl+B toggle branch"
	}
	if string(view.Get("hasMultipleWorktrees")) == "true" {
		guide += " · Ctrl+W show all worktrees"
	}
	if u.mode == "search" {
		guide = "Type to Search · Enter select · " + dismiss + " clear"
	} else {
		guide += " · Ctrl+V preview · Ctrl+R rename · Type to search · " + dismiss + " cancel"
	}
	out = append(out, guide)
	if p.mcpExitPending != "" {
		out = append(out, p.mcpExitPending)
	}
	return out
}
