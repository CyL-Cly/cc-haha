package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// LocalPanel is an immutable projection of the existing application's local
// command state. Row identities and actions are supplied by that owner.
type LocalPanel struct {
	OriginKnown                  bool               `json:"-"`
	LocalJSXCommand              bool               `json:"-"`
	TeamsFrame                   TerminalTeamsFrame `json:"-"`
	KeepPromptInput              bool
	Submenu                      bool
	DismissAction                string
	HideDetails                  bool
	RawInputKeys                 []string
	ID, Kind, Title, Description string
	Rows                         []LocalPanelRow
	Fields                       []LocalPanelField
	Actions                      []LocalPanelAction
	Notice                       string
	NoticeUntil                  time.Time
	Presentation                 json.RawMessage
	SelectedRow                  string
	RowDescriptions              bool
	VisibleRows                  int
	ThemePicker                  bool
	HelpDialog                   bool
	PluginOptions                bool
	ConfirmationOnly             bool
	MCPCatalog                   bool
	MCPAuthentication            bool
	MCPAuthenticationCopy        bool
	MCPExitScope                 string
	PreserveFields               bool
	SubmitAction                 string
	SettingsTabs                 []string
	SettingsHeaderFocused        bool
}
type LocalPanelRow struct {
	ID, Label, Detail string
	SearchText        string
	Actions           []LocalPanelAction
}
type LocalPanelField struct {
	Name, Label, Value string
	Description        string
	ValueJSON          json.RawMessage
	Sensitive          bool
}
type LocalPanelAction struct{ Key, Name, Label string }
type LocalPanelResult struct {
	TeamsFrame      TerminalTeamsFrame `json:"-"`
	TeamsIndex      int                `json:"-"`
	TeamsLevel      string             `json:"-"`
	ID, Action, Row string
	Fields          map[string]string
	FieldsJSON      map[string]json.RawMessage
}
type LocalPanelSession interface {
	ApplyLocalPanel(context.Context, LocalPanelResult, Emit) error
}

func ordinaryPanelName(name string) bool {
	switch name {
	case "resume", "continue", "branch", "fork", "buddy", "btw", "mobile", "ios", "android", "diff", "context", "effort", "stats", "help", "skills", "permissions", "allowed-tools", "tasks", "bashes", "agents", "mcp", "memory", "status", "usage", "output-style", "fast", "hooks", "config", "settings", "theme", "workflows", "copy", "export", "rename", "color", "tag", "plugin", "plugins", "marketplace", "reload-plugins", "add-dir", "vim", "clear", "reset", "new":
		return true
	}
	return false
}

func terminalNativeLocalCommand(command Completion) bool {
	if command.Kind != "local" {
		return false
	}
	switch command.Name {
	case "vim", "clear", "reset", "new":
		return true
	}
	return false
}

type localPanelState struct {
	terminalResume       *terminalResumeUI
	btwStarted           time.Time
	btwBodyInitialized   bool
	teamsPromptExpanded  bool
	props                LocalPanel
	selected, field      int
	scroll               int
	editors              []Editor
	pluginOptionValues   [][]uint16
	detail               bool
	settingsSearch       Editor
	searchFocused        bool
	settingsRows         []LocalPanelRow
	settingsSelected     int
	themePreview         string
	themeSyntaxTheme     string
	themeSyntaxAvailable bool
	headerFocused        bool
	helpTab              string
	helpExitTimes        map[string]time.Time
	helpExitPending      string
	helpPaint            *terminalHelpPaint
	mcpExitTimes         map[string]time.Time
	mcpExitPending       string
}

func (s *State) openLocalPanel(props *LocalPanel) {
	if props == nil {
		s.LocalPanel = nil
		return
	}
	state := &localPanelState{props: *props, headerFocused: props.SettingsHeaderFocused, btwStarted: time.Now()}
	if props.HelpDialog {
		state.headerFocused = true
	}
	state.props.Rows = append([]LocalPanelRow{}, props.Rows...)
	state.settingsRows = append([]LocalPanelRow{}, props.Rows...)
	state.props.Fields = append([]LocalPanelField{}, props.Fields...)
	state.props.Actions = append([]LocalPanelAction{}, props.Actions...)
	state.props.SettingsTabs = append([]string{}, props.SettingsTabs...)
	state.props.Presentation = append(json.RawMessage(nil), props.Presentation...)
	state.editors = make([]Editor, len(props.Fields))
	for i, field := range props.Fields {
		state.editors[i].Set(field.Value)
	}
	if old := s.LocalPanel; old != nil && old.props.ID == props.ID {
		if props.MCPExitScope != "" && props.MCPExitScope == old.props.MCPExitScope {
			state.mcpExitTimes = old.mcpExitTimes
			state.mcpExitPending = old.mcpExitPending
		}
		if props.PreserveFields && old.props.Title == props.Title {
			for i, field := range props.Fields {
				for j, previous := range old.props.Fields {
					if field.Name == previous.Name {
						state.editors[i] = old.editors[j]
					}
				}
			}
		}
		if old.props.ThemePicker && props.ThemePicker {
			state.themePreview = old.themePreview
			state.themeSyntaxTheme = old.themeSyntaxTheme
			state.themeSyntaxAvailable = old.themeSyntaxAvailable
		}
		if props.Kind == "btw" && old.props.Kind == "btw" {
			state.scroll = old.scroll
			state.btwStarted = old.btwStarted
			state.btwBodyInitialized = old.btwBodyInitialized
		}
		state.selected = min(old.selected, max(len(props.Rows)-1, 0))
		state.settingsSelected = old.settingsSelected
		if old.props.Kind == "config" && !old.props.Submenu && props.Submenu {
			state.settingsSelected = old.selected
		} else if old.props.Kind == "config" && old.props.Submenu && !props.Submenu {
			state.selected = min(old.settingsSelected, max(len(props.Rows)-1, 0))
		}
		state.settingsSearch = old.settingsSearch
		state.headerFocused = old.headerFocused
		state.helpTab = old.helpTab
		state.helpExitTimes = old.helpExitTimes
		state.helpExitPending = old.helpExitPending
		state.helpPaint = old.helpPaint
		state.searchFocused = old.searchFocused
		state.filterSettingsRows()
	}
	if props.SelectedRow != "" && (s.LocalPanel == nil || s.LocalPanel.props.Title != props.Title || s.LocalPanel.props.Submenu != props.Submenu) {
		for i, row := range state.props.Rows {
			if row.ID == props.SelectedRow {
				state.selected = i
				break
			}
		}
	}
	state.retainTerminalDiffState(s.LocalPanel)
	state.retainTerminalResumeState(s.LocalPanel)
	state.retainTerminalTeamsState(s.LocalPanel)
	if old := s.LocalPanel; old != nil && old.props.ID == state.props.ID && old.props.OriginKnown && !state.props.OriginKnown {
		state.props.OriginKnown, state.props.LocalJSXCommand = true, old.props.LocalJSXCommand
	}
	s.LocalPanel = state
}
func (p *localPanelState) result(name string) LocalPanelResult {
	value := LocalPanelResult{ID: p.props.ID, Action: name, Fields: map[string]string{}}
	if len(p.props.Rows) > 0 {
		value.Row = p.props.Rows[p.selected].ID
	}
	for i, field := range p.props.Fields {
		value.Fields[field.Name] = p.editors[i].Text()
	}
	return value
}
func (s *State) handleLocalPanelKey(key Key, now time.Time) []action {
	p := s.LocalPanel
	if handled, actions := s.handleTerminalHelpKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalTeamsKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalBtwKey(key); handled {
		return actions
	}
	if handled, actions := s.handleTerminalResumeKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalBuddyPanelKey(key); handled {
		return actions
	}
	if handled, actions := s.handleTerminalMobileKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalDiffKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleMCPDialogExit(key, now); handled {
		return actions
	}
	if p.props.MCPAuthentication && (key.Name == "ctrl+c" || key.Name == "ctrl+d") {
		if s.press("mcp-auth-exit:"+key.Name, now) {
			return s.EOF()
		}
		name := "Ctrl-D"
		if key.Name == "ctrl+c" {
			name = "Ctrl-C"
		}
		s.applyNotice(Event{Text: "Press " + name + " again to exit", NoticeTimeout: doublePressWindow})
		return nil
	}
	if p.props.MCPAuthentication && p.props.MCPAuthenticationCopy && key.Text == "c" {
		if len(p.editors) > 0 {
			p.editors[p.field].Insert("c")
		}
		return []action{{kind: actionLocalPanel, panel: p.result("mcp-auth-copy")}}
	}
	if handled, actions := s.handleTerminalPluginOptionsKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalThemeKey(key, now); handled {
		return actions
	}
	if handled, actions := s.handleTerminalSettingsTabs(key, now); handled {
		return actions
	}
	if handled, actions := s.handleLocalSettingsKey(key, now); handled {
		return actions
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	contexts := []string{"Global", "Confirmation", "Select"}
	if p.props.ConfirmationOnly {
		contexts = []string{"Global", "Confirmation"}
	}
	if p.props.Kind == "help" {
		contexts = append(contexts, "Help")
	}
	if p.props.Kind == "plugin" && len(p.editors) == 0 {
		contexts = append(contexts, "Plugin")
	}
	if len(p.editors) == 0 {
		contexts = append(contexts, "Chat")
	}
	// Printable field input belongs to the editor. Confirmation/select defaults
	// bind n, y, j and k; resolving them here would dismiss a form while typing.
	resolution := BindingResolution{}
	rawKey := false
	for _, input := range p.props.RawInputKeys {
		if name == input {
			rawKey = true
			break
		}
	}
	if !rawKey && (len(p.editors) == 0 || key.Text == "") {
		resolution = ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	}
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	}
	if resolution.Type == "chord_cancelled" || resolution.Type == "unbound" {
		s.pendingChord = nil
		return nil
	}
	if resolution.Type == "match" {
		s.pendingChord = nil
		if p.props.ConfirmationOnly {
			for _, choice := range p.props.Actions {
				if choice.Key == resolution.Action {
					return []action{{kind: actionLocalPanel, panel: p.result(choice.Name)}}
				}
			}
			return nil
		}
		if len(p.editors) == 0 {
			if handled, actions := s.handleLocalDisplayAction(resolution.Action, now); handled {
				return actions
			}
		}
		switch resolution.Action {
		case "plugin:toggle":
			name = "space"
		case "plugin:install":
			name = "i"
		case "help:dismiss", "confirm:no":
			name = "esc"
		case "select:next", "select:previous", "confirm:next", "confirm:previous":
			if len(p.editors) == 0 {
				if resolution.Action == "select:next" || resolution.Action == "confirm:next" {
					name = "down"
				} else {
					name = "up"
				}
			}
		case "confirm:nextField":
			name = "tab"
		case "confirm:previousField":
			name = "shift+tab"
		case "select:accept", "confirm:yes":
			if len(p.editors) == 0 || key.Text == "" {
				name = "enter"
			}
		}
	}
	switch name {
	case "pageup", "wheelup":
		p.scroll = max(0, p.scroll-max(1, s.Height/2))
		return nil
	case "pagedown", "wheeldown":
		p.scroll += max(1, s.Height/2)
		return nil
	}
	if name == "esc" || name == "escape" || name == "ctrl+c" {
		if p.detail {
			p.detail = false
			return nil
		}
		if p.props.DismissAction != "" && name != "ctrl+c" {
			return []action{{kind: actionLocalPanel, panel: p.result(p.props.DismissAction)}}
		}
		s.LocalPanel = nil
		return []action{{kind: actionLocalPanel, panel: p.result("close")}}
	}
	if len(p.editors) > 0 {
		switch name {
		case "tab", "down":
			p.field = (p.field + 1) % len(p.editors)
		case "shift+tab", "up":
			p.field = (p.field + len(p.editors) - 1) % len(p.editors)
		case "enter":
			name := "save"
			if p.props.SubmitAction != "" {
				name = p.props.SubmitAction
			}
			result := p.result(name)
			if p.props.MCPAuthentication {
				p.editors[p.field].Reset()
			}
			return []action{{kind: actionLocalPanel, panel: result}}
		case "backspace":
			p.editors[p.field].Backspace()
		case "delete":
			p.editors[p.field].Delete()
		case "left":
			p.editors[p.field].Left()
		case "right":
			p.editors[p.field].Right()
		case "home", "ctrl+a":
			p.editors[p.field].Home()
		case "end", "ctrl+e":
			p.editors[p.field].End()
		case "ctrl+u":
			p.editors[p.field].KillToStart()
		default:
			if key.Text != "" {
				p.editors[p.field].Insert(key.Text)
			}
		}
		return nil
	}
	choices := append([]LocalPanelAction{}, p.props.Actions...)
	if len(p.props.Rows) > 0 {
		choices = append(choices, p.props.Rows[p.selected].Actions...)
	}
	for _, choice := range choices {
		if choice.Key == name {
			return []action{{kind: actionLocalPanel, panel: p.result(choice.Name)}}
		}
	}
	if rawKey {
		return nil
	}
	switch name {
	case "home", "ctrl+home":
		p.selected = 0
		p.detail = false
		return nil
	case "end", "ctrl+end":
		p.selected = max(len(p.props.Rows)-1, 0)
		p.detail = false
		return nil
	case "up", "k", "ctrl+p":
		p.selected = max(p.selected-1, 0)
		p.detail = false
		return nil
	case "down", "j", "ctrl+n":
		p.selected = min(p.selected+1, max(len(p.props.Rows)-1, 0))
		p.detail = false
		return nil
	case "enter":
		if p.props.HideDetails {
			return nil
		}
		p.detail = !p.detail
		return nil
	}
	return nil
}
func (p *localPanelState) lines(width, height int, themes ...string) []string {
	if p.props.Kind == "mobile" {
		return terminalMobileLines(p.props.Presentation, width)
	}
	if p.props.Kind == "diff" {
		theme := "dark"
		if len(themes) > 0 {
			theme = themes[0]
		}
		return p.terminalDiffLines(width, height, theme)
	}
	if p.props.PluginOptions && len(p.props.Fields) > 0 {
		return p.pluginOptionsLines(width)
	}
	lines := wrap(p.props.Title, width)
	if len(p.props.SettingsTabs) > 0 && !p.props.Submenu {
		labels := []string{}
		for _, kind := range p.props.SettingsTabs {
			label := strings.ToUpper(kind[:1]) + kind[1:]
			if kind == p.props.Kind {
				label = "[" + label + "]"
			}
			labels = append(labels, label)
		}
		lines = append(lines, wrap(strings.Join(labels, "  "), width)...)
	}
	lines = append(lines, wrap(p.props.Description, width)...)
	if p.props.Kind == "config" && len(p.editors) == 0 && !p.props.Submenu {
		query := p.settingsSearch.Text()
		prefix := "  "
		if p.searchFocused {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+"Search settings… "+query, width)...)
		if query != "" && len(p.props.Rows) == 0 {
			lines = append(lines, wrap("No settings match \""+query+"\"", width)...)
		}
	}
	if len(p.props.Fields) > 0 {
		for i, field := range p.props.Fields {
			prefix := "  "
			if i == p.field {
				prefix = "> "
			}
			value := p.editors[i].Text()
			if field.Sensitive {
				value = strings.Repeat("*", len([]rune(value)))
			}
			lines = append(lines, wrap(prefix+field.Label+": "+value, width)...)
		}
		lines = append(lines, "Tab next field · Enter save · Esc cancel")
	} else if p.props.Kind == "usage" && len(p.props.Presentation) > 0 {
		lines = append(lines, UsageVisualization(p.props.Presentation, width, themes...)...)
	} else if p.props.Kind == "stats" && len(p.props.Presentation) > 0 {
		lines = append(lines, StatsVisualization(p.props.Presentation, width)...)
	} else if p.detail && len(p.props.Rows) > 0 {
		row := p.props.Rows[p.selected]
		lines = append(lines, wrap(row.Label+"\n"+row.Detail, width)...)
	} else {
		if len(p.props.Rows) == 0 {
			lines = append(lines, "No entries")
		}
		limit := max(height-8, 3)
		if p.props.VisibleRows > 0 {
			limit = min(limit, p.props.VisibleRows)
		}
		start := max(p.selected-limit+1, 0)
		for i := start; i < min(start+limit, len(p.props.Rows)); i++ {
			prefix := "  "
			if i == p.selected && !p.headerFocused && !(p.props.Kind == "config" && !p.props.Submenu && p.searchFocused) {
				prefix = "> "
			}
			lines = append(lines, wrap(prefix+p.props.Rows[i].Label, width)...)
			if p.props.RowDescriptions && p.props.Rows[i].Detail != "" {
				lines = append(lines, wrap("  "+p.props.Rows[i].Detail, width)...)
			}
		}
	}
	if p.props.Notice != "" && (p.props.NoticeUntil.IsZero() || time.Now().Before(p.props.NoticeUntil)) {
		lines = append(lines, wrap(p.props.Notice, width)...)
	}
	if len(p.props.Fields) == 0 {
		hints := []string{"↑/↓ select", "Enter details"}
		if len(p.props.SettingsTabs) > 0 && !p.props.Submenu {
			if p.headerFocused {
				hints = []string{"←/→/Tab switch tabs", "↓ settings"}
			} else {
				hints = []string{"↑/↓ select", "↑ tabs"}
			}
		}
		choices := append([]LocalPanelAction{}, p.props.Actions...)
		if len(p.props.Rows) > 0 {
			choices = append(choices, p.props.Rows[p.selected].Actions...)
		}
		for _, choice := range choices {
			hints = append(hints, choice.Key+" "+choice.Label)
		}
		if p.props.DismissAction != "" {
			hints = append(hints, "Esc back")
		} else {
			hints = append(hints, "Esc close")
		}
		if p.mcpExitPending != "" {
			lines = append(lines, wrap(p.mcpExitPending, width)...)
		} else {
			lines = append(lines, wrap(strings.Join(hints, " · "), width)...)
		}
	}
	available := max(1, height-4)
	if len(lines) > available+2 {
		content := lines[1 : len(lines)-1]
		p.scroll = min(p.scroll, max(len(content)-available, 0))
		visible := []string{lines[0]}
		visible = append(visible, content[p.scroll:min(len(content), p.scroll+available)]...)
		visible = append(visible, lines[len(lines)-1])
		return visible
	}
	return lines
}
