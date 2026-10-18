package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
)

func (p *localPanelState) terminalDiffData() terminaldiff.Dialog {
	var data terminaldiff.Dialog
	_ = json.Unmarshal(p.props.Presentation, &data)
	return data
}
func (p *localPanelState) retainTerminalDiffState(old *localPanelState) {
	if old == nil || old.props.ID != p.props.ID || old.props.Kind != "diff" || p.props.Kind != "diff" {
		return
	}
	p.scroll, p.selected, p.detail = old.scroll, old.selected, old.detail
	p.mcpExitTimes, p.mcpExitPending = old.mcpExitTimes, old.mcpExitPending
	if count := len(p.terminalDiffData().Sources); p.scroll >= count {
		p.scroll = max(0, count-1)
		p.selected = 0
	}
}
func (s *State) handleTerminalDiffKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "diff" {
		return false, nil
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
	data := p.terminalDiffData()
	if len(data.Sources) == 0 {
		return true, nil
	}
	p.scroll = min(p.scroll, len(data.Sources)-1)
	resolution := ResolveChord(keyStroke(key), []string{"Confirmation", "DiffDialog"}, s.bindings, s.pendingChord)
	if resolution.Type == "chord_started" {
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	}
	s.pendingChord = nil
	if resolution.Type != "match" {
		return true, nil
	}
	files := data.Sources[p.scroll].Data.Files
	switch resolution.Action {
	case "diff:previousSource":
		if p.detail {
			p.detail = false
		} else if p.scroll > 0 {
			p.scroll--
			p.selected = 0
		}
	case "diff:nextSource":
		if !p.detail && p.scroll < len(data.Sources)-1 {
			p.scroll++
			p.selected = 0
		}
	case "diff:back":
		p.detail = false
	case "diff:viewDetails":
		if !p.detail && p.selected >= 0 && p.selected < len(files) {
			p.detail = true
		}
	case "diff:previousFile":
		if !p.detail {
			p.selected = max(0, p.selected-1)
		}
	case "diff:nextFile":
		if !p.detail {
			p.selected = min(len(files)-1, p.selected+1)
		}
	case "diff:dismiss", "confirm:no":
		if p.detail {
			p.detail = false
		} else {
			s.LocalPanel = nil
			return true, []action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: p.props.ID, Action: "diff-close"}}}
		}
	}
	return true, nil
}

func (p *localPanelState) terminalDiffLines(width, height int, theme string, bindings ...[]Keybinding) []string {
	data := p.terminalDiffData()
	if len(data.Sources) == 0 {
		return []string{"Loading diff…"}
	}
	source := data.Sources[min(p.scroll, len(data.Sources)-1)]
	d := source.Data
	header := "Uncommitted changes (git diff HEAD)"
	if source.TurnIndex > 0 {
		header = fmt.Sprintf("Turn %d", source.TurnIndex)
		if source.UserPromptPreview != "" {
			header += " \"" + source.UserPromptPreview + "\""
		}
	}
	out := []string{header}
	if len(data.Sources) > 1 {
		tabs := []string{}
		for i, v := range data.Sources {
			label := "Current"
			if v.TurnIndex > 0 {
				label = fmt.Sprintf("T%d", v.TurnIndex)
			}
			if i == p.scroll {
				label = "[" + label + "]"
			}
			tabs = append(tabs, label)
		}
		out = append(out, strings.Join(tabs, " · "))
	}
	if d.Stats != nil {
		label := fmt.Sprintf("%d files changed", d.Stats.FilesCount)
		if d.Stats.FilesCount == 1 {
			label = "1 file changed"
		}
		if d.Stats.LinesAdded > 0 {
			label += fmt.Sprintf(" +%d", d.Stats.LinesAdded)
		}
		if d.Stats.LinesRemoved > 0 {
			label += fmt.Sprintf(" -%d", d.Stats.LinesRemoved)
		}
		out = append(out, label)
	}
	if len(d.Files) == 0 {
		empty := "Working tree is clean"
		if d.Loading {
			empty = "Loading diff…"
		} else if source.TurnIndex > 0 {
			empty = "No file changes in this turn"
		} else if d.Stats != nil && d.Stats.FilesCount > 0 {
			empty = "Too many files to display details"
		}
		out = append(out, "", empty)
	} else if !p.detail {
		start := max(0, p.selected-2)
		end := min(len(d.Files), start+5)
		if end-start < 5 {
			start = max(0, end-5)
		}
		if start > 0 {
			out = append(out, fmt.Sprintf(" ↑ %d more files", start))
		}
		for i := start; i < end; i++ {
			f := d.Files[i]
			prefix := "  "
			if i == p.selected {
				prefix = "❯ "
			}
			suffix := ""
			if f.LinesAdded > 0 {
				suffix = fmt.Sprintf("+%d", f.LinesAdded)
			}
			if f.LinesRemoved > 0 {
				if suffix != "" {
					suffix += " "
				}
				suffix += fmt.Sprintf("-%d", f.LinesRemoved)
			}
			if f.IsUntracked {
				suffix = "untracked"
			} else if f.IsBinary {
				suffix = "Binary file"
			} else if f.IsLargeFile {
				suffix = "Large file modified"
			}
			if f.IsTruncated {
				suffix += " (truncated)"
			}
			out = append(out, prefix+displayText(f.Path)+"  "+suffix)
		}
		if end < len(d.Files) {
			out = append(out, fmt.Sprintf(" ↓ %d more files", len(d.Files)-end))
		}
	} else if p.selected >= 0 && p.selected < len(d.Files) {
		f := d.Files[p.selected]
		out = append(out, "", displayText(f.Path))
		switch {
		case f.IsUntracked:
			out = append(out, "New file not yet staged.", "Run `git add "+displayText(f.Path)+"` to see line counts.")
		case f.IsBinary:
			out = append(out, "Binary file - cannot display diff")
		case f.IsLargeFile:
			out = append(out, "Large file - diff exceeds 1 MB limit")
		default:
			if f.IsTruncated {
				out = append(out, "(truncated)")
			}
			for _, h := range f.Hunks {
				out = append(out, terminalStructuredDiffStrings(h, max(1, width-4), theme)...)
			}
		}
	}
	dismiss := "esc"
	if len(bindings) > 0 {
		if display, ok := BindingDisplay("diff:dismiss", "DiffDialog", bindings[0]); ok {
			dismiss = display
		}
	}
	guide := "↑/↓ select · Enter view · " + dismiss + " close"
	if len(data.Sources) > 1 && !p.detail {
		guide = "←/→ source · " + guide
	}
	if p.detail {
		guide = "← back · " + dismiss + " close"
	}
	if p.mcpExitPending != "" {
		guide = p.mcpExitPending
	}
	return append(out, "", guide)
}
