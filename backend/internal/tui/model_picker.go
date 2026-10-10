package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ModelOption contains the Source modelOptions display data and the shared
// host's resolution of its effort policy. A nil Value is the default model.
type ModelOption struct {
	Value          *string
	Label          string
	Description    string
	SupportsEffort bool
	SupportsMax    bool
	DefaultEffort  string
}

// ModelPicker is a snapshot of Source ModelPicker props and AppState. Raw
// aliases stay raw; model resolution and settings writes belong to the host.
type ModelPicker struct {
	ID                string
	Initial           *string
	InitialLabel      string
	SessionModel      *string
	SessionModelLabel string
	Options           []ModelOption
	Effort            *string
	Standalone        bool
	SkipSettingsWrite bool
	HeaderText        string
}

type ModelPickerResult struct {
	Cancelled        bool
	Model            *string
	Effort           *string
	HasToggledEffort bool
}

// ModelPickerSession consumes the original picker callback through the same
// Session that owns the Agent. This is not a model or permission executor.
type ModelPickerSession interface {
	RespondModelPicker(context.Context, string, ModelPickerResult, Emit) error
}

type modelPickerState struct {
	props      ModelPicker
	focused    int
	from, to   int
	effort     *string
	initialSet bool
	toggled    bool
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sameModel(left, right *string) bool {
	value := func(model *string) string {
		if model == nil {
			return "__NO_PREFERENCE__"
		}
		return *model
	}
	return value(left) == value(right)
}

func (s *State) OpenModelPicker(props ModelPicker) {
	props.Initial = cloneString(props.Initial)
	props.SessionModel = cloneString(props.SessionModel)
	props.Effort = cloneString(props.Effort)
	props.Options = append([]ModelOption(nil), props.Options...)
	foundInitial := props.Initial == nil
	for i := range props.Options {
		props.Options[i].Value = cloneString(props.Options[i].Value)
		foundInitial = foundInitial || props.Options[i].Value != nil && props.Initial != nil && *props.Options[i].Value == *props.Initial
	}
	if !foundInitial {
		props.Options = append(props.Options, ModelOption{Value: cloneString(props.Initial), Label: props.InitialLabel, Description: "Current model"})
	}
	count := min(10, len(props.Options))
	picker := &modelPickerState{props: props, focused: -1, to: count, effort: cloneString(props.Effort), initialSet: props.Effort != nil}
	for i := range props.Options {
		if sameModel(props.Options[i].Value, props.Initial) {
			picker.focused = i
		}
	}
	if picker.focused >= 0 {
		size := picker.optionMapSize()
		if picker.focused >= count {
			picker.to = min(size, picker.focused+1)
			picker.from = max(0, picker.to-count)
		}
		picker.from = max(0, min(picker.from, size-1))
		picker.to = min(size, max(count, picker.to))
	} else if len(props.Options) > 0 {
		picker.focused = picker.optionMapIndex(0)
	}
	// CustomSelect calls onFocus on its initial controlled focus as well as
	// subsequent navigation. This initializes the Source default effort.
	if !picker.initialSet && picker.focused >= 0 {
		picker.setDefaultEffort()
	}
	s.ModelPicker = picker
	s.pendingChord = nil
}

// Source OptionMap retains the original linked row list, while duplicate
// values replace the Map entry and reduce Map.size. Preserve both semantics.
func (p *modelPickerState) optionMapIndex(index int) int {
	for i := len(p.props.Options) - 1; i >= 0; i-- {
		if sameModel(p.props.Options[i].Value, p.props.Options[index].Value) {
			return i
		}
	}
	return index
}

func (p *modelPickerState) optionMapSize() int {
	count := 0
	for i := range p.props.Options {
		if p.optionMapIndex(i) == i {
			count++
		}
	}
	return count
}

func (p *modelPickerState) focusedOption() ModelOption {
	for _, option := range p.props.Options {
		if sameModel(option.Value, p.props.Options[p.focused].Value) {
			return option
		}
	}
	return p.props.Options[p.focused]
}

func (p *modelPickerState) setDefaultEffort() {
	value := p.focusedOption().DefaultEffort
	if value == "" {
		value = "high"
	}
	p.effort = &value
}

func (p *modelPickerState) focus(index int) {
	index = p.optionMapIndex(index)
	if index == p.focused {
		return
	}
	p.focused = index
	if !p.toggled && !p.initialSet {
		p.setDefaultEffort()
	}
}

func (p *modelPickerState) navigate(direction int, page bool) {
	count := len(p.props.Options)
	if p.focused < 0 || count == 0 {
		return
	}
	visible := min(10, count)
	size := p.optionMapSize()
	if page {
		index := max(0, p.focused+direction*visible)
		if direction > 0 {
			index = min(size-1, index)
		}
		p.focus(index)
		if direction > 0 {
			p.to = min(size, index+1)
			p.from = max(0, p.to-visible)
		} else {
			p.from = index
			p.to = min(size, p.from+visible)
		}
		return
	}
	index := (p.focused + direction + count) % count
	wrap := direction > 0 && index == 0 || direction < 0 && index == count-1
	p.focus(index)
	if wrap {
		if direction > 0 {
			p.from, p.to = 0, visible
		} else {
			p.to = size
			p.from = max(0, size-visible)
		}
	} else if direction > 0 && index >= p.to {
		p.to = min(size, p.to+1)
		p.from = p.to - visible
	} else if direction < 0 && index <= p.from {
		p.from = max(0, p.from-1)
		p.to = p.from + visible
	}
}

func (p *modelPickerState) cycle(direction int) {
	if p.focused < 0 {
		return
	}
	option := p.focusedOption()
	if !option.SupportsEffort {
		return
	}
	levels := []string{"low", "medium", "high"}
	if option.SupportsMax {
		levels = append(levels, "max")
	}
	current := option.DefaultEffort
	if current == "" {
		current = "high"
	}
	if p.effort != nil {
		current = *p.effort
	}
	index := 2
	for i, level := range levels {
		if level == current {
			index = i
			break
		}
	}
	value := levels[(index+direction+len(levels))%len(levels)]
	p.effort = &value
	p.toggled = true
}

func (s *State) modelPickerResult(index int, cancelled bool) []action {
	p := s.ModelPicker
	if p == nil {
		return nil
	}
	result := ModelPickerResult{Cancelled: cancelled, Effort: cloneString(p.effort), HasToggledEffort: p.toggled}
	if !cancelled {
		if index < 0 || index >= len(p.props.Options) {
			return nil
		}
		result.Model = cloneString(p.props.Options[index].Value)
		if sameModel(result.Model, nil) {
			result.Model = nil
		}
	}
	s.ModelPicker = nil
	return []action{{kind: actionModelPicker, permission: p.props.ID, modelPickerResult: result}}
}

func (s *State) handleModelPickerAction(name string, now time.Time) []action {
	p := s.ModelPicker
	if p == nil {
		return nil
	}
	switch name {
	case "select:next":
		p.navigate(1, false)
	case "select:previous":
		p.navigate(-1, false)
	case "select:accept":
		return s.modelPickerResult(p.focused, false)
	case "select:cancel":
		return s.modelPickerResult(0, true)
	case "modelPicker:decreaseEffort":
		p.cycle(-1)
	case "modelPicker:increaseEffort":
		p.cycle(1)
	case "app:interrupt", "app:exit":
		key := "ctrl+c"
		if name == "app:exit" {
			key = "ctrl+d"
		}
		if s.press(key, now) {
			return s.EOF()
		}
		if p.props.Standalone {
			s.Status = "Press " + strings.ToUpper(key) + " again to exit"
		}
	default:
		return []action{{kind: actionInvoke, name: name}}
	}
	return nil
}

func (s *State) handleModelPickerKey(key Key) []action {
	p := s.ModelPicker
	if p == nil {
		return nil
	}
	switch key.Name {
	case "pageup":
		p.navigate(-1, true)
	case "pagedown":
		p.navigate(1, true)
	default:
		text := key.Text
		if text == "" && len(key.Name) == 1 {
			text = key.Name
		}
		text = strings.Map(func(r rune) rune {
			if r >= '０' && r <= '９' {
				return r - '０' + '0'
			}
			return r
		}, text)
		if text == "" {
			return nil
		}
		for _, r := range text {
			if r < '0' || r > '9' {
				return nil
			}
		}
		if number, err := strconv.Atoi(text); err == nil && number > 0 && number <= len(p.props.Options) {
			return s.modelPickerResult(number-1, false)
		}
	}
	return nil
}

func (p *modelPickerState) lines(width int) []string {
	header := p.props.HeaderText
	if header == "" {
		header = "Switch between Claude models. Applies to this session and future Claude Code sessions. For other/previous model names, specify with --model."
	}
	lines := append(wrap("Select model", width), wrap(header, width)...)
	if p.props.SessionModel != nil && *p.props.SessionModel != "" {
		lines = append(lines, wrap("Currently using "+p.props.SessionModelLabel+" for this session (set by plan mode). Selecting a model will undo this.", width)...)
	}
	from, to := p.from, p.to
	if from < 0 {
		from = max(0, len(p.props.Options)+from)
	}
	if to < 0 {
		to = max(0, len(p.props.Options)+to)
	}
	for i := min(from, len(p.props.Options)); i < min(to, len(p.props.Options)); i++ {
		option := p.props.Options[i]
		prefix := "  "
		if p.focused >= 0 && sameModel(option.Value, p.props.Options[p.focused].Value) {
			prefix = "> "
		}
		lines = append(lines, wrap(fmt.Sprintf("%s%d. %s", prefix, i+1, option.Label), width)...)
		if option.Description != "" {
			lines = append(lines, wrap("   "+option.Description, width)...)
		}
	}
	if hidden := len(p.props.Options) - min(10, len(p.props.Options)); hidden > 0 {
		lines = append(lines, wrap(fmt.Sprintf("   and %d more…", hidden), width)...)
	}
	if p.focused >= 0 {
		option := p.focusedOption()
		label := "Effort not supported"
		if option.Label != "" {
			label += " for " + option.Label
		}
		if option.SupportsEffort {
			effort := ""
			if p.effort != nil {
				effort = *p.effort
			}
			if effort == "max" && !option.SupportsMax {
				effort = "high"
			}
			label = effort + " effort"
			defaultEffort := option.DefaultEffort
			if defaultEffort == "" {
				defaultEffort = "high"
			}
			if effort == defaultEffort {
				label += " (default)"
			}
			label += " · ← → to adjust"
		}
		lines = append(lines, wrap(label, width)...)
	}
	return append(lines, wrap("↑/↓ to select · Enter to confirm · Esc to cancel", width)...)
}
