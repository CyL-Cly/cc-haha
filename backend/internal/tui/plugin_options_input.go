package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/rivo/uniseg"
)

func (p *localPanelState) ensurePluginOptionsValues() {
	if p.pluginOptionValues != nil {
		return
	}
	p.pluginOptionValues = make([][]uint16, len(p.props.Fields))
	for i, field := range p.props.Fields {
		encoded := field.ValueJSON
		if len(encoded) == 0 {
			encoded, _ = json.Marshal(field.Value)
		}
		p.pluginOptionValues[i], _ = protocol.DecodeJavaScriptJSONString(encoded)
	}
}
func (p *localPanelState) syncPluginOptionEditor() {
	encoded := protocol.QuoteJavaScriptUTF16(p.pluginOptionValues[p.field])
	var value string
	_ = json.Unmarshal(encoded, &value)
	p.editors[p.field].Set(value)
}
func (p *localPanelState) pluginOptionsResult(name string) LocalPanelResult {
	p.ensurePluginOptionsValues()
	result := p.result(name)
	result.FieldsJSON = map[string]json.RawMessage{}
	for i, field := range p.props.Fields {
		result.FieldsJSON[field.Name] = protocol.QuoteJavaScriptUTF16(p.pluginOptionValues[i])
	}
	return result
}
func (s *State) handleTerminalPluginOptionsKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || !p.props.PluginOptions || len(p.editors) == 0 {
		return false, nil
	}
	p.ensurePluginOptionsValues()
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Settings", "Confirmation"}, s.bindings, s.pendingChord)
	switch resolution.Type {
	case "chord_started":
		s.pendingChord = resolution.Pending
		s.chordUntil = now.Add(time.Second)
		return true, nil
	case "unbound", "chord_cancelled":
		s.pendingChord = nil
		return true, nil
	case "match":
		s.pendingChord = nil
		switch resolution.Action {
		case "confirm:no":
			return true, []action{{kind: actionLocalPanel, panel: p.pluginOptionsResult(p.props.DismissAction)}}
		case "confirm:nextField":
			if p.field < len(p.editors)-1 {
				p.field++
			}
			return true, nil
		case "confirm:yes":
			if p.field < len(p.editors)-1 {
				p.field++
				return true, nil
			}
			return true, []action{{kind: actionLocalPanel, panel: p.pluginOptionsResult("save")}}
		case "app:interrupt":
			return false, nil
		}
	}
	if key.Name == "backspace" || key.Name == "delete" {
		values := p.pluginOptionValues[p.field]
		if len(values) > 0 {
			p.pluginOptionValues[p.field] = values[:len(values)-1]
			p.syncPluginOptionEditor()
		}
		return true, nil
	}
	stroke := keyStroke(key)
	modified := strings.HasPrefix(key.Name, "ctrl+") || strings.HasPrefix(key.Name, "alt+") || strings.HasPrefix(key.Name, "meta+")
	if stroke != nil {
		modified = modified || stroke.Ctrl || stroke.Alt || stroke.Meta || stroke.Super
	}
	if key.Text != "" && !modified && key.Name != "tab" && key.Name != "enter" {
		encoded, _ := json.Marshal(key.Text)
		units, _ := protocol.DecodeJavaScriptJSONString(encoded)
		p.pluginOptionValues[p.field] = append(p.pluginOptionValues[p.field], units...)
		p.syncPluginOptionEditor()
	}
	return true, nil
}
func (p *localPanelState) pluginOptionsLines(width int) []string {
	p.ensurePluginOptionsValues()
	field := p.props.Fields[p.field]
	value := p.editors[p.field].Text()
	if field.Sensitive {
		value = strings.Repeat("*", uniseg.StringWidth(value))
	}
	lines := wrap(p.props.Title, width)
	lines = append(lines, wrap(p.props.Description, width)...)
	lines = append(lines, " ")
	lines = append(lines, wrap(field.Label, width)...)
	if field.Description != "" {
		lines = append(lines, wrap(field.Description, width)...)
	}
	lines = append(lines, " ")
	lines = append(lines, wrap("› "+value+"█", width)...)
	lines = append(lines, " ")
	lines = append(lines, wrap("Field "+strconv.Itoa(p.field+1)+" of "+strconv.Itoa(len(p.props.Fields)), width)...)
	if p.field < len(p.props.Fields)-1 {
		lines = append(lines, wrap("Tab: Next field · Enter: Save and continue", width)...)
	} else {
		lines = append(lines, wrap("Enter: Save configuration", width)...)
	}
	lines = append(lines, " ")
	lines = append(lines, wrap("Enter to confirm · Esc to cancel", width)...)
	return lines
}
