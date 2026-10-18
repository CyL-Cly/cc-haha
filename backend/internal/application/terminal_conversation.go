package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/textsanitization"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var terminalAgentColors = []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}
var terminalExportExtension = regexp.MustCompile(`\.[^.]+$`)
var terminalCodeLanguage = regexp.MustCompile(`[^a-zA-Z0-9]`)

func terminalExportChoices() []tui.LocalPanelRow {
	return []tui.LocalPanelRow{{ID: "clipboard", Label: "Copy to clipboard", Detail: "Copy the conversation to your system clipboard"}, {ID: "file", Label: "Save to file", Detail: "Save the conversation to a file in the current directory"}}
}

func (s *Session) terminalConversationCommands() []tui.Completion {
	commands := []tui.Completion{{Name: "copy", Description: "Copy Claude's last response to clipboard (or /copy N for the Nth-latest)", Kind: "local-jsx"}, {Name: "export", Description: "Export the current conversation to a file or clipboard", ArgumentHint: "[filename]", Kind: "local-jsx"}, {Name: "rename", Description: "Rename the current conversation", ArgumentHint: "[name]", Kind: "local-jsx"}, {Name: "color", Description: "Set the prompt bar color for this session", ArgumentHint: "<color|default>", Kind: "local-jsx"}}
	if s.native.Environment["USER_TYPE"] == "ant" {
		commands = append(commands, tui.Completion{Name: "tag", Description: "Toggle a searchable tag on the current session", ArgumentHint: "<tag-name>", Kind: "local-jsx"})
	}
	return commands
}

func (s *Session) TerminalPresentation(ctx context.Context) (json.RawMessage, error) {
	value, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return nil, err
	}
	color := value.Metadata.Get("agentColor")
	if string(color) == `"default"` {
		color = raw("")
	}
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: value.Metadata.Get("agentName")}, protocol.JSONField{Name: "color", Value: color})), nil
}

func terminalAssistantTexts(messages []protocol.JSONObject) []string {
	texts := []string{}
	for index := len(messages) - 1; index >= 0 && len(texts) < 20; index-- {
		message := messages[index]
		if message.String("type") != "assistant" || string(message.Get("isApiErrorMessage")) == "true" {
			continue
		}
		envelope, _ := message.Object("message")
		var blocks []protocol.JSONObject
		if json.Unmarshal(envelope.Get("content"), &blocks) != nil {
			continue
		}
		parts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				parts = append(parts, block.String("text"))
			}
		}
		if text := strings.Join(parts, "\n\n"); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

type terminalCopyCode struct{ Text, Language string }

func terminalCopyCodeBlocks(markdown string) []terminalCopyCode {
	for _, tag := range []string{"commit_analysis", "context", "function_analysis", "pr_analysis"} {
		markdown = regexp.MustCompile(`(?s)<`+tag+`>.*?</`+tag+`>\n?`).ReplaceAllString(markdown, "")
	}
	source := []byte(protocol.TrimJavaScriptString(markdown))
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	blocks := []terminalCopyCode{}
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		language := ""
		switch code := node.(type) {
		case *ast.FencedCodeBlock:
			if code.Info != nil {
				language = string(code.Info.Text(source))
			}
		case *ast.CodeBlock:
		default:
			continue
		}
		blocks = append(blocks, terminalCopyCode{Text: strings.TrimSuffix(string(node.Lines().Value(source)), "\n"), Language: language})
	}
	return blocks
}
func terminalCopyFilename(language string) string {
	language = terminalCodeLanguage.ReplaceAllString(language, "")
	if language != "" && language != "plaintext" {
		return "copy." + language
	}
	return "copy.txt"
}
func terminalCopyAge(arg string) (float64, error) {
	var number float64
	var err error
	if len(arg) > 2 && (strings.HasPrefix(arg, "0x") || strings.HasPrefix(arg, "0X") || strings.HasPrefix(arg, "0b") || strings.HasPrefix(arg, "0B") || strings.HasPrefix(arg, "0o") || strings.HasPrefix(arg, "0O")) {
		var integer uint64
		integer, err = strconv.ParseUint(arg, 0, 64)
		number = float64(integer)
	} else {
		number, err = strconv.ParseFloat(arg, 64)
	}
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 {
		return 0, fmt.Errorf("Usage: /copy [N] where N is 1 (latest), 2, 3, … Got: %s", arg)
	}
	return number, nil
}

func terminalExportFilename(arg string) string {
	if strings.HasSuffix(arg, ".txt") {
		return arg
	}
	return terminalExportExtension.ReplaceAllString(arg, "") + ".txt"
}
func terminalFirstExportPrompt(messages []protocol.JSONObject) string {
	for _, message := range messages {
		if message.String("type") == "user" {
			envelope, _ := message.Object("message")
			content := envelope.Get("content")
			var value string
			if json.Unmarshal(content, &value) != nil {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(content, &blocks)
				for _, block := range blocks {
					if block.String("type") == "text" {
						value = block.String("text")
						break
					}
				}
			}
			value = strings.Split(protocol.TrimJavaScriptString(value), "\n")[0]
			units := utf16.Encode([]rune(value))
			if len(units) > 50 {
				value = string(utf16.Decode(units[:49])) + "…"
			}
			return value
		}
	}
	return ""
}
func terminalExportSlug(text string) string {
	text = strings.ToLower(text)
	var cleaned strings.Builder
	for _, r := range text {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			cleaned.WriteRune(r)
		} else if protocol.TrimJavaScriptString(string(r)) == "" {
			cleaned.WriteByte('-')
		}
	}
	text = cleaned.String()
	text = regexp.MustCompile(`-+`).ReplaceAllString(text, "-")
	return strings.Trim(text, "-")
}

func (s *Session) terminalConversationNotice(emit tui.Emit, text string) error {
	if emit != nil {
		return emit(tui.Event{Kind: tui.Notice, Text: text})
	}
	return nil
}
func (s *Session) terminalCopyToClipboard(ctx context.Context, emit tui.Emit, text string) error {
	if emit == nil {
		return fmt.Errorf("terminal clipboard consumer unavailable")
	}
	reply := make(chan error, 1)
	if err := emit(tui.Event{Kind: tui.ClipboardTextRequested, Text: text, ClipboardTextReply: reply}); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (s *Session) finishTerminalCopy(ctx context.Context, emit tui.Emit, content, filename, panelID string, fileOnly, always bool) error {
	value, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return err
	}
	if !fileOnly {
		if err = s.terminalCopyToClipboard(ctx, emit, content); err != nil {
			return err
		}
	}
	path := filepath.Join(value.Temp, "claude", filename)
	writeErr := os.MkdirAll(filepath.Dir(path), 0700)
	if writeErr == nil {
		writeErr = os.WriteFile(path, []byte(content), 0600)
	}
	notice := fmt.Sprintf("Copied to clipboard (%d characters, %d lines)", len(utf16.Encode([]rune(content))), strings.Count(content, "\n")+1)
	if fileOnly {
		notice = "Written to " + path
		if writeErr != nil {
			notice = "Failed to write file: " + writeErr.Error()
		}
	} else if writeErr == nil {
		notice += "\nAlso written to " + path
	}
	if always {
		if err = s.process.SetTerminalConfigSetting(ctx, "copyFullResponse", raw(true)); err != nil {
			return err
		}
		notice += "\nPreference saved. Use /config to change copyFullResponse"
	}
	if err = s.closeTerminalConversationPanel(panelID, emit); err != nil {
		return err
	}
	return s.terminalConversationNotice(emit, notice)
}

func (s *Session) invokeTerminalConversationCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "copy" && name != "export" && name != "rename" && name != "color" && name != "tag" {
		return false, nil
	}
	if name == "tag" && s.native.Environment["USER_TYPE"] != "ant" {
		return false, nil
	}
	value, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return true, err
	}
	args = protocol.TrimJavaScriptString(args)
	switch name {
	case "rename":
		if value.Teammate {
			return true, s.terminalConversationNotice(emit, "Cannot rename: This session is a swarm teammate. Teammate names are set by the team leader.")
		}
		newName := raw(args)
		if args == "" {
			newName, err = s.process.GenerateTerminalConversationName(ctx)
			if err != nil {
				return true, err
			}
			if newName == nil || string(newName) == `""` {
				return true, s.terminalConversationNotice(emit, "Could not generate a name: no conversation context yet. Usage: /rename <name>")
			}
		}
		for _, kind := range []string{"custom-title", "agent-name"} {
			if err = s.process.SaveTerminalConversationMetadata(ctx, kind, newName); err != nil {
				return true, err
			}
		}
		if err = s.emitTerminalPresentation(ctx, emit); err != nil {
			return true, err
		}
		var text string
		_ = json.Unmarshal(newName, &text)
		return true, s.terminalConversationNotice(emit, "Session renamed to: "+text)
	case "color":
		if value.Teammate {
			return true, s.terminalConversationNotice(emit, "Cannot set color: This session is a swarm teammate. Teammate colors are assigned by the team leader.")
		}
		list := strings.Join(terminalAgentColors, ", ") + ", default"
		if args == "" {
			return true, s.terminalConversationNotice(emit, "Please provide a color. Available colors: "+list)
		}
		color := strings.ToLower(args)
		reset := false
		for _, alias := range []string{"default", "reset", "none", "gray", "grey"} {
			reset = reset || color == alias
		}
		valid := reset
		for _, candidate := range terminalAgentColors {
			valid = valid || color == candidate
		}
		if !valid {
			return true, s.terminalConversationNotice(emit, fmt.Sprintf("Invalid color %q. Available colors: %s", color, list))
		}
		if reset {
			color = "default"
		}
		if err = s.process.SaveTerminalConversationMetadata(ctx, "agent-color", raw(color)); err != nil {
			return true, err
		}
		if err = s.emitTerminalPresentation(ctx, emit); err != nil {
			return true, err
		}
		notice := "Session color set to: " + color
		if reset {
			notice = "Session color reset to default"
		}
		return true, s.terminalConversationNotice(emit, notice)
	case "copy":
		texts := terminalAssistantTexts(value.Messages)
		if len(texts) == 0 {
			return true, s.terminalConversationNotice(emit, "No assistant message to copy")
		}
		age := 1.0
		if args != "" {
			age, err = terminalCopyAge(args)
			if err != nil {
				return true, s.terminalConversationNotice(emit, err.Error())
			}
		}
		if age > float64(len(texts)) {
			noun := "messages"
			if len(texts) == 1 {
				noun = "message"
			}
			return true, s.terminalConversationNotice(emit, fmt.Sprintf("Only %d assistant %s available to copy", len(texts), noun))
		}
		content := texts[int(age)-1]
		if len(terminalCopyCodeBlocks(content)) == 0 || value.CopyFullResponse {
			return true, s.finishTerminalCopy(ctx, emit, content, "response.md", "", false, false)
		}
		state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "copy"}, selected: strconv.Itoa(int(age)), rows: map[string]protocol.JSONObject{}}
		if err = s.refreshTerminalConversationPanel(ctx, state); err != nil {
			return true, err
		}
		return true, s.openTerminalConversationPanel(state, emit)
	case "export":
		presentation, err := s.process.TerminalExportPresentation(ctx)
		if err != nil {
			return true, err
		}
		content := tui.RenderConversationText(value.Messages, presentation)
		if err = s.process.CommitTerminalExportRenderEffects(ctx, presentation); err != nil {
			return true, err
		}
		if args != "" {
			return true, s.writeTerminalExport(ctx, args, content, "", emit)
		}
		filename := "conversation-" + value.Now.Format("2006-01-02-150405") + ".txt"
		if slug := terminalExportSlug(terminalFirstExportPrompt(value.Messages)); slug != "" {
			filename = value.Now.Format("2006-01-02-150405") + "-" + slug + ".txt"
		}
		state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "export", Title: "Export Conversation", Description: "Select export method:", Rows: terminalExportChoices(), Actions: []tui.LocalPanelAction{{Key: "enter", Name: "select", Label: "select"}}}, rows: map[string]protocol.JSONObject{"content": protocol.NewJSONObject(field("text", content), field("filename", filename))}}
		return true, s.openTerminalConversationPanel(state, emit)
	case "tag":
		if terminalTagHelpArgument(args) {
			return true, s.terminalConversationNotice(emit, "Usage: /tag <tag-name>\n\nToggle a searchable tag on the current session.\nRun the same command again to remove the tag.\nTags are displayed after the branch name in /resume and can be searched with /.\n\nExamples:\n  /tag bugfix        # Add tag\n  /tag bugfix        # Remove tag (toggle)\n  /tag feature-auth\n  /tag wip")
		}
		sanitized, err := textsanitization.String(args)
		if err != nil {
			return true, err
		}
		tag := protocol.TrimJavaScriptString(sanitized)
		if tag == "" {
			return true, s.terminalConversationNotice(emit, "Tag name cannot be empty")
		}
		if value.Metadata.String("tag") == tag {
			state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "tag", Title: "Remove tag?", Description: "Current tag: #" + tag, Rows: []tui.LocalPanelRow{{ID: "yes", Label: "Yes, remove tag"}, {ID: "no", Label: "No, keep tag"}}, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "select", Label: "select"}}}, selected: tag}
			return true, s.openTerminalConversationPanel(state, emit)
		}
		if err = s.process.SaveTerminalConversationMetadata(ctx, "tag", raw(tag)); err != nil {
			return true, err
		}
		return true, s.terminalConversationNotice(emit, "Tagged session with #"+tag)
	}
	return false, nil
}

func terminalTagHelpArgument(arg string) bool {
	switch arg {
	case "", "help", "-h", "--help", "list", "show", "display", "current", "view", "get", "check", "describe", "print", "version", "about", "status", "?":
		return true
	}
	return false
}

func (s *Session) emitTerminalPresentation(ctx context.Context, emit tui.Emit) error {
	if emit == nil {
		return nil
	}
	value, err := s.TerminalPresentation(ctx)
	if err != nil {
		return err
	}
	return emit(tui.Event{Kind: tui.SessionPresentationChanged, Result: value})
}
func (s *Session) openTerminalConversationPanel(state *terminalLocalPanelState, emit tui.Emit) error {
	s.mu.Lock()
	previous := s.terminalPanel
	s.mu.Unlock()
	if previous != state {
		s.closeTerminalBtwPanel(previous)
	}
	s.mu.Lock()
	s.terminalPanel = state
	s.mu.Unlock()
	if emit != nil {
		props := terminalPanelProjection(state.props)
		return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &props})
	}
	return nil
}
func (s *Session) closeTerminalConversationPanel(id string, emit tui.Emit) error {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	active := s.terminalPanel != nil && s.terminalPanel.props.ID == id
	if active {
		s.terminalPanel = nil
	}
	s.mu.Unlock()
	if active && emit != nil {
		return emit(tui.Event{Kind: tui.LocalPanelClosed})
	}
	return nil
}

func (s *Session) writeTerminalExport(ctx context.Context, filename, content, panelID string, emit tui.Emit) error {
	value, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(value.CWD, terminalExportFilename(filename))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err == nil {
		_, err = file.WriteString(content)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err := s.closeTerminalConversationPanel(panelID, emit); err != nil {
		return err
	}
	if err != nil {
		return s.terminalConversationNotice(emit, "Failed to export conversation: "+err.Error())
	}
	return s.terminalConversationNotice(emit, "Conversation exported to: "+path)
}

func (s *Session) refreshTerminalConversationPanel(ctx context.Context, state *terminalLocalPanelState) error {
	if state.props.Kind != "copy" {
		return nil
	}
	value, err := s.process.TerminalConversation(ctx)
	if err != nil {
		return err
	}
	age, _ := strconv.Atoi(state.selected)
	texts := terminalAssistantTexts(value.Messages)
	if age < 1 || age > len(texts) {
		return fmt.Errorf("selected assistant message no longer available")
	}
	content := texts[age-1]
	state.props.Title, state.props.Description = "Copy response", "Select content to copy:"
	state.props.Rows = []tui.LocalPanelRow{{ID: "full", Label: "Full response", Detail: fmt.Sprintf("%d chars, %d lines", len(utf16.Encode([]rune(content))), strings.Count(content, "\n")+1)}}
	state.rows = map[string]protocol.JSONObject{"full": protocol.NewJSONObject(field("text", content), field("filename", "response.md"))}
	for index, block := range terminalCopyCodeBlocks(content) {
		id := strconv.Itoa(index)
		label := strings.Split(block.Text, "\n")[0]
		if len([]rune(label)) > 60 {
			label = string([]rune(label)[:59]) + "…"
		}
		state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: id, Label: label, Detail: block.Language})
		state.rows[id] = protocol.NewJSONObject(field("text", block.Text), field("filename", terminalCopyFilename(block.Language)))
	}
	state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: "always", Label: "Always copy full response", Detail: "Skip this picker in the future (revert via /config)"})
	state.rows["always"] = state.rows["full"].Clone()
	state.props.Actions = []tui.LocalPanelAction{{Key: "enter", Name: "select", Label: "copy"}, {Key: "w", Name: "write-copy", Label: "write to file"}}
	return nil
}

func (s *Session) applyTerminalConversationPanel(ctx context.Context, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	s.mu.Lock()
	state := s.terminalPanel
	s.mu.Unlock()
	if state == nil || state.props.ID != result.ID || (state.props.Kind != "copy" && state.props.Kind != "export" && state.props.Kind != "tag") {
		return false, nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if result.Action == "close" {
		if state.props.Kind == "export" && state.form == "filename" {
			row := state.rows["content"]
			row.Set("filename", raw(result.Fields["filename"]))
			state.form = ""
			state.props.Fields = nil
			state.props.Rows = terminalExportChoices()
			return true, s.openTerminalConversationPanel(state, emit)
		}
		notice := "Copy cancelled"
		if state.props.Kind == "export" {
			notice = "Export cancelled"
		}
		if state.props.Kind == "tag" {
			notice = "Kept tag #" + state.selected
		}
		if err := s.closeTerminalConversationPanel(state.props.ID, emit); err != nil {
			return true, err
		}
		return true, s.terminalConversationNotice(emit, notice)
	}
	switch state.props.Kind {
	case "copy":
		row, ok := state.rows[result.Row]
		if !ok {
			return true, fmt.Errorf("copy selection unavailable")
		}
		return true, s.finishTerminalCopy(ctx, emit, row.String("text"), row.String("filename"), state.props.ID, result.Action == "write-copy", result.Row == "always" && result.Action != "write-copy")
	case "export":
		row := state.rows["content"]
		if state.form == "filename" {
			return true, s.writeTerminalExport(ctx, result.Fields["filename"], row.String("text"), state.props.ID, emit)
		}
		if result.Row == "clipboard" {
			if err := s.terminalCopyToClipboard(ctx, emit, row.String("text")); err != nil {
				return true, err
			}
			if err := s.closeTerminalConversationPanel(state.props.ID, emit); err != nil {
				return true, err
			}
			return true, s.terminalConversationNotice(emit, "Conversation copied to clipboard")
		}
		state.form = "filename"
		state.props.Fields = []tui.LocalPanelField{{Name: "filename", Label: "Enter filename:", Value: row.String("filename")}}
		state.props.Rows = nil
		return true, s.openTerminalConversationPanel(state, emit)
	case "tag":
		notice := "Kept tag #" + state.selected
		if result.Row == "yes" {
			if err := s.process.SaveTerminalConversationMetadata(ctx, "tag", raw("")); err != nil {
				return true, err
			}
			notice = "Removed tag #" + state.selected
		}
		if err := s.closeTerminalConversationPanel(state.props.ID, emit); err != nil {
			return true, err
		}
		return true, s.terminalConversationNotice(emit, notice)
	}
	return false, nil
}
