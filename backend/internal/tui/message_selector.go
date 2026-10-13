package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"regexp"
	"strings"
	"time"
)

type MessageChoice struct {
	ID, Text       string
	CanRestoreCode bool
}
type MessageSelectorSnapshot struct {
	Choices            []MessageChoice
	FileHistoryEnabled bool
}
type MessageSelection struct{ ID, Action, Feedback string }
type MessageSelectionResult struct {
	History               []protocol.JSONObject
	Prefill, Mode, Notice string
	PastedContents        map[int]PastedContent
	PermissionMode        string
}
type MessageSelectorSession interface {
	MessageSelectorSnapshot(context.Context) (MessageSelectorSnapshot, error)
	SelectMessage(context.Context, MessageSelection, Emit) (MessageSelectionResult, error)
}
type messageSelectorState struct {
	snapshot                MessageSelectorSnapshot
	selected, option        int
	confirm, feedback, busy bool
	input                   Editor
	error                   string
}

func (s *State) OpenMessageSelector(snapshot MessageSelectorSnapshot) {
	s.MessageSelector = &messageSelectorState{snapshot: snapshot, selected: len(snapshot.Choices)}
	s.Completions = nil
	s.Status = ""
}
func (p *messageSelectorState) options() []string {
	out := []string{"conversation"}
	if p.selected < len(p.snapshot.Choices) && p.snapshot.Choices[p.selected].CanRestoreCode {
		out = []string{"both", "conversation", "code"}
	}
	return append(out, "summarize", "nevermind")
}
func (s *State) handleMessageSelectorKey(key Key, now time.Time) []action {
	p := s.MessageSelector
	if p == nil {
		return nil
	}
	if p.busy {
		if key.Name == "esc" || key.Name == "ctrl+c" {
			return []action{{kind: actionCancel}}
		}
		return nil
	}
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		s.MessageSelector = nil
		return nil
	}
	if key.Name == "esc" {
		if p.feedback {
			p.feedback = false
			return nil
		}
		if p.confirm {
			p.confirm = false
			return nil
		}
		s.MessageSelector = nil
		return nil
	}
	if p.feedback {
		switch key.Name {
		case "enter":
			p.busy = true
			return []action{{kind: actionMessageSelection, messageSelection: MessageSelection{ID: p.snapshot.Choices[p.selected].ID, Action: "summarize", Feedback: strings.TrimSpace(p.input.Text())}}}
		case "backspace":
			p.input.Backspace()
		case "left":
			p.input.Left()
		case "right":
			p.input.Right()
		default:
			if key.Text != "" {
				p.input.Insert(key.Text)
			}
		}
		return nil
	}
	limit := len(p.snapshot.Choices)
	if p.confirm {
		limit = len(p.options()) - 1
	}
	index := &p.selected
	if p.confirm {
		index = &p.option
	}
	switch key.Name {
	case "up", "k", "ctrl+p":
		*index = max(0, *index-1)
	case "down", "j", "ctrl+n":
		*index = min(limit, *index+1)
	case "ctrl+up", "shift+up", "meta+up", "shift+k":
		*index = 0
	case "ctrl+down", "shift+down", "meta+down", "shift+j":
		*index = limit
	case "enter":
		if !p.confirm {
			if p.selected == len(p.snapshot.Choices) {
				s.MessageSelector = nil
				return nil
			}
			if !p.snapshot.FileHistoryEnabled {
				p.busy = true
				return []action{{kind: actionMessageSelection, messageSelection: MessageSelection{ID: p.snapshot.Choices[p.selected].ID, Action: "conversation"}}}
			}
			p.confirm = true
			p.option = 0
			return nil
		}
		actionName := p.options()[p.option]
		if actionName == "nevermind" {
			s.MessageSelector = nil
			return nil
		}
		if actionName == "summarize" {
			p.feedback = true
			return nil
		}
		p.busy = true
		return []action{{kind: actionMessageSelection, messageSelection: MessageSelection{ID: p.snapshot.Choices[p.selected].ID, Action: actionName}}}
	}
	return nil
}
func (p *messageSelectorState) lines(width int) []string {
	out := wrap("Rewind or summarize conversation", width)
	if p.busy {
		return append(out, wrap("Working… Esc to cancel", width)...)
	}
	if p.error != "" {
		out = append(out, wrap(p.error, width)...)
	}
	if p.feedback {
		return append(out, wrap("Summarize from here: "+p.input.Text()+"\nAdd context (optional) · Enter to summarize · Esc to return", width)...)
	}
	if p.confirm {
		choice := p.snapshot.Choices[p.selected]
		out = append(out, wrap("Selected: "+choice.Text, width)...)
		labels := map[string]string{"both": "Restore code and conversation", "conversation": "Restore conversation", "code": "Restore code", "summarize": "Summarize from here", "nevermind": "Never mind"}
		for i, option := range p.options() {
			prefix := "  "
			if i == p.option {
				prefix = "> "
			}
			out = append(out, wrap(prefix+labels[option], width)...)
		}
		return append(out, wrap("↑/↓: choose · Enter: confirm · Esc: back", width)...)
	}
	first := max(0, min(p.selected-3, len(p.snapshot.Choices)+1-7))
	last := min(len(p.snapshot.Choices), first+6)
	for i := first; i <= last; i++ {
		label := "(current)"
		if i < len(p.snapshot.Choices) {
			label = p.snapshot.Choices[i].Text
		}
		prefix := "  "
		if i == p.selected {
			prefix = "> "
		}
		out = append(out, wrap(fmt.Sprintf("%s%s", prefix, label), width)...)
	}
	return append(out, wrap("↑/↓: select message · Enter: choose · Esc: close", width)...)
}

// SelectableMessageText matches the Source selector's authored-user boundary.
func SelectableMessageText(message protocol.JSONObject) (string, bool) {
	if message.String("type") != "user" || string(message.Get("isMeta")) == "true" || string(message.Get("isCompactSummary")) == "true" || string(message.Get("isVisibleInTranscriptOnly")) == "true" {
		return "", false
	}
	api, _ := message.Object("message")
	content := api.Get("content")
	text := api.String("content")
	var blocks []protocol.JSONObject
	if json.Unmarshal(content, &blocks) == nil {
		if len(blocks) > 0 && blocks[0].String("type") == "text" {
			switch blocks[0].String("text") {
			case "[Request interrupted by user]", "[Request interrupted by user for tool use]", "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.", "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.", "No response requested.":
				return "", false
			}
		}
		if len(blocks) > 0 && blocks[0].String("type") == "tool_result" {
			return "", false
		}
		if len(blocks) > 0 && blocks[len(blocks)-1].String("type") == "text" {
			text = blocks[len(blocks)-1].String("text")
		}
	}
	text = messageTrim(text)
	for _, tag := range []string{"<local-command-stdout>", "<local-command-stderr>", "<bash-stdout>", "<bash-stderr>", "<task-notification>", "<tick>", "<teammate-message"} {
		if strings.Contains(text, tag) {
			return "", false
		}
	}
	return text, true
}

// MessageResubmit restores authored input rather than the selector's display
// label. Command wrappers, bash mode, IDE context and pasted image state use
// the same boundaries as Source textForResubmit/restoreMessageSync.
func MessageResubmit(message protocol.JSONObject) (text, mode string, pastes map[int]PastedContent) {
	mode = "prompt"
	api, _ := message.Object("message")
	text = api.String("content")
	var blocks []protocol.JSONObject
	if json.Unmarshal(api.Get("content"), &blocks) == nil {
		parts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				parts = append(parts, block.String("text"))
			}
		}
		text = messageTrim(strings.Join(parts, "\n"))
	}
	if bash := messageTag(text, "bash-input"); bash != "" {
		text, mode = bash, "bash"
	} else if command := messageTag(text, "command-name"); command != "" {
		text = command + " " + messageTag(text, "command-args")
	} else {
		for _, tag := range []string{"ide_opened_file", "ide_selection"} {
			pattern := regexp.MustCompile(`<` + tag + `(?:[\s\x{00a0}\x{feff}][^>]*)?>[\s\S]*?</` + tag + `>\n?`)
			text = pattern.ReplaceAllString(text, "")
		}
		text = messageTrim(text)
	}
	var ids []int
	_ = json.Unmarshal(message.Get("imagePasteIds"), &ids)
	imageIndex := 0
	for _, block := range blocks {
		if block.String("type") != "image" {
			continue
		}
		id := imageIndex + 1
		if imageIndex < len(ids) {
			id = ids[imageIndex]
		}
		imageIndex++
		source, ok := block.Object("source")
		if !ok || source.String("type") != "base64" {
			continue
		}
		if pastes == nil {
			pastes = map[int]PastedContent{}
		}
		pastes[id] = PastedContent{ID: id, Type: "image", Content: source.String("data"), MediaType: source.String("media_type")}
	}
	return text, mode, pastes
}

func messageTag(text, tag string) string {
	pattern := regexp.MustCompile(`(?i)<` + tag + `(?:[\s\x{00a0}\x{feff}]+[^>]*)?>([\s\S]*?)</` + tag + `>`)
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		if match[1] != "" {
			return match[1]
		}
	}
	return ""
}
func messageTrim(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}

func (s *State) handleMessageSelectorBindings(key Key, now time.Time) []action {
	p := s.MessageSelector
	if p.busy || p.feedback {
		return s.handleMessageSelectorKey(key, now)
	}
	contexts := []string{"Global", "Confirmation", "MessageSelector"}
	if p.confirm {
		contexts = []string{"Global", "Select", "Confirmation"}
	}
	result := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	switch result.Type {
	case "unbound", "chord_cancelled":
		s.pendingChord = nil
		return nil
	case "chord_started":
		s.pendingChord = result.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	case "match":
		s.pendingChord = nil
		keys := map[string]string{"messageSelector:up": "up", "messageSelector:down": "down", "messageSelector:top": "ctrl+up", "messageSelector:bottom": "ctrl+down", "messageSelector:select": "enter", "select:previous": "up", "select:next": "down", "select:accept": "enter", "select:cancel": "esc", "confirm:no": "esc", "confirm:yes": "enter", "app:interrupt": "ctrl+c", "app:exit": "ctrl+d"}
		if name, ok := keys[result.Action]; ok {
			key = Key{Name: name}
		}
	}
	return s.handleMessageSelectorKey(key, now)
}
