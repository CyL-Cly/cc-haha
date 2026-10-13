package tui

import (
	"context"
	"time"
)

type IDEOption struct{ Value, Label, Description string }
type IDEPicker struct {
	ID, Title, Description, Current, Mode string
	Options                               []IDEOption
	Unavailable                           []string
	AskAutoConnect, AskDisableAutoConnect bool
}
type IDEPickerResult struct {
	Presented          bool
	Cancelled          bool
	Value              string
	AutoConnect        *bool
	DisableAutoConnect bool
}
type IDEPickerSession interface {
	RespondIDEPicker(context.Context, string, IDEPickerResult, Emit) error
}
type idePickerState struct {
	shown    bool
	props    IDEPicker
	selected int
	phase    string
	choice   int
}

func (s *State) OpenIDEPicker(props IDEPicker) {
	copy := props
	copy.Options = append([]IDEOption{}, props.Options...)
	copy.Unavailable = append([]string{}, props.Unavailable...)
	p := &idePickerState{props: copy}
	for i, option := range props.Options {
		if option.Value == props.Current {
			p.selected = i
			break
		}
	}
	if props.Mode == "onboarding" {
		s.IDEOnboarding = p
	} else {
		s.IDEPicker = p
	}
	s.pendingChord = nil
}
func (s *State) handleIDEPickerKey(key Key, now time.Time) []action {
	p := s.IDEPicker
	if p == nil {
		p = s.IDEOnboarding
	}
	close := func(result IDEPickerResult) []action {
		if p.props.Mode == "onboarding" {
			s.IDEOnboarding = nil
		} else {
			s.IDEPicker = nil
		}
		return []action{{kind: actionIDEPicker, permission: p.props.ID, idePickerResult: result}}
	}
	switch key.Name {
	case "ctrl+c", "ctrl+d":
		if s.press(key.Name, now) {
			return s.EOF()
		}
		s.Status = "Press " + key.Name + " again to exit"
		return nil
	case "up", "ctrl+p":
		if p.phase != "" {
			p.choice = 1 - p.choice
		} else if len(p.props.Options) > 0 {
			p.selected = (p.selected + len(p.props.Options) - 1) % len(p.props.Options)
		}
		return nil
	case "down", "ctrl+n":
		if p.phase != "" {
			p.choice = 1 - p.choice
		} else if len(p.props.Options) > 0 {
			p.selected = (p.selected + 1) % len(p.props.Options)
		}
		return nil
	case "esc":
		if p.phase == "auto" {
			return nil
		} // Source onCancel repeats its still-unset preference dialog.
		if p.phase == "disable" {
			return close(IDEPickerResult{Value: "None"})
		}
		return close(IDEPickerResult{Cancelled: true})
	case "enter":
		if p.phase == "auto" {
			value := p.choice == 0
			return close(IDEPickerResult{Value: p.props.Options[p.selected].Value, AutoConnect: &value})
		}
		if p.phase == "disable" {
			return close(IDEPickerResult{Value: "None", DisableAutoConnect: p.choice == 1})
		}
		if p.props.Mode == "onboarding" {
			return close(IDEPickerResult{})
		}
		if len(p.props.Options) == 0 {
			return nil
		}
		value := p.props.Options[p.selected].Value
		if value != "None" && p.props.AskAutoConnect {
			p.phase = "auto"
			p.choice = 0
			return nil
		}
		if value == "None" && p.props.AskDisableAutoConnect {
			p.phase = "disable"
			p.choice = 0
			return nil
		}
		return close(IDEPickerResult{Value: value})
	}
	return nil
}

type IDEOnboardingSource interface {
	SubscribeIDEOnboarding(context.Context, func(*IDEPicker)) (*IDEPicker, func(), error)
}

func (s *State) ideOnboardingVisible() bool {
	return s.IDEOnboarding != nil && s.IDEPicker == nil && !s.Closing && !s.Editing && !s.Searching && !s.Transcript && s.MessageSelector == nil && s.WorktreeExit == nil && s.ModelPicker == nil && s.ThinkingPicker == nil && len(s.Permissions) == 0 && !s.pluginHintPromptActive
}
func (s *State) ideOnboardingTick() []action {
	if !s.ideOnboardingVisible() || s.IDEOnboarding.shown {
		return nil
	}
	s.IDEOnboarding.shown = true
	return []action{{kind: actionIDEPicker, permission: s.IDEOnboarding.props.ID, idePickerResult: IDEPickerResult{Presented: true}}}
}
func (p *idePickerState) lines(width int) []string {
	if p.phase != "" {
		title := "Do you wish to enable auto-connect to IDE?"
		options := []string{"Yes", "No"}
		if p.phase == "disable" {
			title = "Do you wish to disable auto-connect to IDE?"
			options = []string{"No", "Yes"}
		}
		lines := wrap(title, width)
		for i, option := range options {
			prefix := "  "
			if i == p.choice {
				prefix = "> "
			}
			lines = append(lines, wrap(prefix+option, width)...)
		}
		return append(lines, wrap("You can also configure this in /config or with the --ide flag", width)...)
	}
	lines := wrap(p.props.Title+"\n"+p.props.Description, width)
	for i, option := range p.props.Options {
		prefix := "  "
		if i == p.selected {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+option.Label, width)...)
		if option.Description != "" {
			lines = append(lines, wrap("  "+option.Description, width)...)
		}
	}
	for _, text := range p.props.Unavailable {
		lines = append(lines, wrap(text, width)...)
	}
	return append(lines, wrap("Enter: select · Esc: cancel", width)...)
}
