package tui

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamnavigation"
	"runtime"
	"strings"
	"time"
)

const doublePressWindow = 800 * time.Millisecond

type Key struct {
	Name string
	Text string
	// Stroke is the terminal adapter's modifier-preserving key identity.
	// Tests and callers supplying Name can omit it.
	Stroke *Keystroke
}

type Entry struct {
	TurnID                string
	Kind                  string
	Level                 string
	Text                  string
	ToolID                string
	ToolName              string
	Input, Output, Result json.RawMessage
	IsError               bool
	Running               bool
	Cancelled             bool
	TranscriptOnly        bool
}

type actionKind uint8

const (
	actionSubmit actionKind = iota
	actionCancel
	actionPermission
	actionQuit
	actionSuspend
	actionRedraw
	actionEditor
	actionInvoke
	actionModelPicker
	actionThinkingPicker
	actionTeammate
)

type action struct {
	kind                 actionKind
	input                Input
	permission           string
	decision             PermissionDecision
	text                 string
	name                 string
	modelPickerResult    ModelPickerResult
	thinkingPickerResult ThinkingPickerResult
	questionIndex        int
	teammate             TeammateAction
}

// State is changed only on the terminal event loop. Session callbacks do not
// mutate it; the adapter sends ordered messages back through Bubble Tea.
type State struct {
	workflowKeywordEnabled    bool
	workflowKeywordSuppressed bool
	workflowKeywordActive     bool
	ForegroundTasks           bool
	Teammates                 *teamnavigation.State
	TeamInbox                 []protocol.JSONObject
	TeammateTurnID            string
	Editor                    Editor
	Entries                   []Entry
	Permissions               []PermissionRequest
	Queue                     []Input
	ActiveTurn                string
	Mode                      string
	PermissionMode            string
	Status                    string
	Notification              string
	notificationUntil         time.Time
	PastedContents            map[int]PastedContent
	pasteSequence             int
	pendingImageSpace         bool
	Width                     int
	Height                    int
	Transcript                bool
	Scroll                    int
	Closing                   bool
	Editing                   bool
	Searching                 bool
	Search                    string
	Completions               []Completion
	ArgumentHint              string
	Selected                  int
	PermissionSelected        int
	sequence                  uint64
	commands                  []Completion
	history                   []HistoryEntry
	historyIndex              int
	historyDraft              string
	historyDraftMode          string
	historyDraftPastes        map[int]PastedContent
	stash                     string
	stashMode                 string
	stashPastes               map[int]PastedContent
	lastPress                 map[string]time.Time
	cancelling                bool
	chordUntil                time.Time
	bindings                  []Keybinding
	pendingChord              []Keystroke
	WorktreeExit              *worktreeDialog
	ModelPicker               *modelPickerState
	ThinkingPicker            *thinkingPickerState
	question                  *questionState
	exitFromCommand           bool
}

func NewState(commands []Completion, width, height int) *State {
	return &State{
		Mode: "prompt", Width: max(width, 1), Height: max(height, 1),
		commands: CloneCompletions(commands), lastPress: make(map[string]time.Time),
		bindings: DefaultKeybindings(runtime.GOOS, true, KeybindingFeatures{}),
	}
}

func (s *State) Busy() bool { return s.ActiveTurn != "" || s.TeammateTurnID != "" }

func (s *State) press(name string, now time.Time) bool {
	previous, exists := s.lastPress[name]
	if exists && !now.Before(previous) && now.Sub(previous) <= doublePressWindow {
		delete(s.lastPress, name)
		return true
	}
	s.lastPress[name] = now
	return false
}

func (s *State) Tick(now time.Time) {
	if !s.notificationUntil.IsZero() && !now.Before(s.notificationUntil) {
		s.Notification = ""
		s.notificationUntil = time.Time{}
	}
	if s.pendingChord != nil && !now.Before(s.chordUntil) {
		s.pendingChord = nil
	}
	for key, at := range s.lastPress {
		if now.Sub(at) > doublePressWindow {
			delete(s.lastPress, key)
			if strings.HasPrefix(s.Status, "Press ") || s.Status == "Esc again to clear" {
				s.Status = ""
			}
		}
	}
}

func (s *State) Resize(width, height int) {
	s.Width, s.Height = max(width, 1), max(height, 1)
}

func (s *State) Paste(text string) {
	defer s.syncWorkflowKeyword(time.Now())
	if !s.Closing && !s.Editing && len(s.Permissions) > 0 && s.Permissions[0].ToolName == "AskUserQuestion" {
		q := s.questionFor(s.Permissions[0])
		if q.err == nil && q.inputFocused() {
			q.editors[q.index].Paste(text)
			q.updateOtherSelection()
		}
		return
	}
	if s.Closing || s.Editing || len(s.Permissions) != 0 || s.Transcript || s.ModelPicker != nil || s.ThinkingPicker != nil {
		return
	}
	if s.Searching {
		var e Editor
		e.Paste(text)
		s.Search += e.Text()
		s.searchHistory(false)
		return
	}
	s.pendingImageSpace = false
	text = normalizedPaste(text)
	if s.Editor.Text() == "" && strings.HasPrefix(text, "!") {
		s.Mode = "bash"
		text = strings.TrimPrefix(text, "!")
	}
	if collapsedPaste(text, s.Height) {
		id := s.addPaste(PastedContent{Type: "text", Content: text})
		s.Editor.Insert(pastedTextRef(id, text))
	} else {
		s.Editor.Insert(text)
	}
	s.historyIndex = 0
	s.updateCompletions()
}

func (s *State) updateCompletions() {
	s.Completions = nil
	s.Selected = 0
	text := s.Editor.Text()
	s.ArgumentHint = s.commandArgumentHint(text)
	if s.Mode != "prompt" || !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n\t") {
		return
	}
	for _, command := range s.commands {
		if command.Hidden {
			continue
		}
		name := "/" + strings.TrimPrefix(command.Name, "/")
		if strings.HasPrefix(name, text) && name != text {
			s.Completions = append(s.Completions, command)
		}
	}
}

func (s *State) historyMove(direction int) {
	if len(s.history) == 0 {
		return
	}
	if s.historyIndex == 0 && direction > 0 {
		s.historyDraft = s.Editor.Text()
		s.historyDraftMode = s.Mode
		s.historyDraftPastes = clonePastes(s.PastedContents)
	}
	index := min(max(s.historyIndex+direction, 0), len(s.history))
	if index == s.historyIndex {
		return
	}
	s.historyIndex = index
	if index == 0 {
		s.Editor.Set(s.historyDraft)
		s.Mode = s.historyDraftMode
		s.PastedContents = clonePastes(s.historyDraftPastes)
	} else {
		s.restoreHistory(s.history[len(s.history)-index])
	}
	s.updateCompletions()
}

func (s *State) restoreHistory(entry HistoryEntry) {
	text := entry.Display
	s.PastedContents = clonePastes(entry.PastedContents)
	s.Mode = "prompt"
	if strings.HasPrefix(text, "!") {
		s.Mode = "bash"
		text = strings.TrimPrefix(text, "!")
	}
	s.Editor.Set(text)
}

func (s *State) searchHistory(next bool) {
	start := len(s.history) - 1
	if next && s.historyIndex != 0 {
		start = len(s.history) - s.historyIndex - 1
	}
	for i := start; i >= 0; i-- {
		if strings.Contains(s.history[i].Display, s.Search) {
			s.historyIndex = len(s.history) - i
			s.restoreHistory(s.history[i])
			return
		}
	}
	s.Status = "No matching history"
}

func (s *State) submit() []action {
	if strings.TrimSpace(s.Editor.Text()) == "" {
		return nil
	}
	s.sequence++
	display := s.Editor.Text()
	pastes := FilterReferencedPastes(display, s.PastedContents)
	input := Input{TurnID: fmt.Sprintf("terminal-%d", s.sequence), Text: ExpandPastedTextRefs(display, pastes), PreExpansionText: display, PastedContents: pastes, Mode: s.Mode}
	input.SuppressWorkflowKeyword = s.workflowKeywordSuppressed
	if actions, exit := s.exitInput(input); exit {
		return actions
	}
	if actions, viewing := s.teammateSubmit(input); viewing {
		s.Editor.Reset()
		s.PastedContents = nil
		s.pendingImageSpace = false
		s.Completions = nil
		s.ArgumentHint = ""
		return actions
	}
	historyText := display
	if input.Mode == "bash" {
		historyText = "!" + historyText
	}
	s.history = append(s.history, HistoryEntry{Display: historyText, PastedContents: clonePastes(pastes)})
	s.historyIndex = 0
	s.Editor.Reset()
	s.PastedContents = nil
	s.pendingImageSpace = false
	s.Mode = "prompt"
	s.Completions = nil
	s.Scroll = 0
	s.ArgumentHint = ""
	if s.Busy() {
		s.Queue = append(s.Queue, input)
		s.Status = fmt.Sprintf("%d queued", len(s.Queue))
		return nil
	}
	return s.start(input)
}

func (s *State) start(input Input) []action {
	s.ActiveTurn, s.cancelling, s.Status = input.TurnID, false, "Working…"
	text := input.Text
	if input.Mode == "bash" {
		text = "! " + text
	}
	s.Entries = append(s.Entries, Entry{TurnID: input.TurnID, Kind: "user", Text: text})
	return []action{{kind: actionSubmit, input: input}}
}

func (s *State) cancel() []action {
	if !s.Busy() || s.cancelling {
		return nil
	}
	s.cancelling = true
	s.Permissions = nil
	s.Status = "Cancelling…"
	return []action{{kind: actionCancel}}
}

func (s *State) EOF() []action {
	if s.Closing {
		return nil
	}
	s.Closing = true
	s.Queue = nil
	result := s.cancel()
	return append(result, action{kind: actionQuit})
}

func (s *State) HandleKey(key Key, now time.Time) []action {
	defer s.syncWorkflowKeyword(now)
	if s.Closing || s.Editing {
		return nil
	}
	s.Tick(now)
	if s.ThinkingPicker != nil {
		return s.handleThinkingPickerBindings(key, now)
	}
	if len(s.Permissions) > 0 && s.Permissions[0].ToolName == "AskUserQuestion" {
		return s.handleQuestionBindings(key, now)
	}
	if len(s.Permissions) == 0 && s.ModelPicker == nil && s.WorktreeExit == nil && !s.Searching {
		if handled, actions := s.handleTeammateKey(key); handled {
			return actions
		}
	}
	// Interrupt/exit and readline Enter are reserved Source input handling.
	// Modal contexts still own their contextual Ctrl-C/Ctrl-D behavior.
	if (key.Name == "ctrl+c" || key.Name == "ctrl+d") && !s.Transcript && !s.Searching && len(s.Permissions) == 0 && s.ModelPicker == nil {
		return s.handleKey(key, now)
	}
	contexts := []string{"Global", "Scroll"}
	switch {
	case s.ModelPicker != nil:
		contexts = []string{"Global", "Select", "ModelPicker"}
	case s.Transcript:
		contexts = append(contexts, "Transcript")
	case len(s.Permissions) > 0:
		contexts = append(contexts, "Confirmation")
	case s.Searching:
		contexts = append(contexts, "HistorySearch")
	default:
		contexts = append(contexts, "Chat")
		if len(s.Completions) > 0 {
			contexts = append(contexts, "Autocomplete")
		}
	}
	if s.ForegroundTasks {
		contexts = append(contexts, "Task")
	}
	result := ResolveChord(keyStroke(key), contexts, s.bindings, s.pendingChord)
	switch result.Type {
	case "chord_started":
		s.pendingChord = result.Pending
		s.chordUntil = now.Add(time.Second)
		return nil
	case "chord_cancelled", "unbound":
		s.pendingChord = nil
		return nil
	case "match":
		s.pendingChord = nil
		if result.Action == "chat:workflowKeywordToggle" {
			s.toggleWorkflowKeyword(now)
			return nil
		}
		if s.ModelPicker != nil {
			return s.handleModelPickerAction(result.Action, now)
		}
		// A literal y must remain a host-provided choice key. Replacing it
		// with Enter would approve a selected choice the host did not offer
		// through y (including a rule or edited input).
		if result.Action == "confirm:yes" && key.Name == "y" && len(s.Permissions) > 0 {
			return s.handleKey(key, now)
		}
		if name, ok := nativeActionKey(result.Action); ok {
			return s.handleKey(Key{Name: name}, now)
		}
		return []action{{kind: actionInvoke, name: result.Action}}
	}
	if s.ModelPicker != nil {
		return s.handleModelPickerKey(key)
	}
	return s.handleKey(key, now)
}

// SetKeybindings atomically replaces a host-resolved default+user binding set.
// It owns copies so a file watcher cannot mutate state off the terminal loop.
func (s *State) SetKeybindings(bindings []Keybinding) {
	s.bindings = make([]Keybinding, len(bindings))
	for i, binding := range bindings {
		binding.Chord = append([]Keystroke(nil), binding.Chord...)
		if binding.Action != nil {
			value := *binding.Action
			binding.Action = &value
		}
		s.bindings[i] = binding
	}
	s.pendingChord = nil
}

func nativeActionKey(action string) (string, bool) {
	switch action {
	case "app:interrupt":
		return "ctrl+c", true
	case "app:exit":
		return "ctrl+d", true
	case "app:redraw":
		return "ctrl+l", true
	case "app:toggleTranscript":
		return "ctrl+o", true
	case "history:search", "historySearch:next":
		return "ctrl+r", true
	case "chat:cancel", "autocomplete:dismiss", "transcript:exit", "historySearch:accept", "confirm:no":
		return "esc", true
	case "chat:submit", "historySearch:execute", "confirm:yes":
		return "enter", true
	case "history:previous", "autocomplete:previous", "confirm:previous":
		return "up", true
	case "history:next", "autocomplete:next", "confirm:next":
		return "down", true
	case "chat:undo":
		return "ctrl+_", true
	case "chat:externalEditor":
		return "ctrl+g", true
	case "chat:stash":
		return "ctrl+s", true
	case "autocomplete:accept":
		return "tab", true
	case "historySearch:cancel":
		return "ctrl+c", true
	case "scroll:pageUp":
		return "pageup", true
	case "scroll:pageDown":
		return "pagedown", true
	case "scroll:lineUp":
		return "wheelup", true
	case "scroll:lineDown":
		return "wheeldown", true
	case "scroll:top":
		return "ctrl+home", true
	case "scroll:bottom":
		return "ctrl+end", true
	default:
		return "", false
	}
}

func (s *State) handleKey(key Key, now time.Time) []action {
	if s.Closing || s.Editing {
		return nil
	}
	pendingImageSpace := s.pendingImageSpace
	s.pendingImageSpace = false
	s.Tick(now)
	if key.Name == "ctrl+z" {
		return []action{{kind: actionSuspend}}
	}
	if key.Name == "ctrl+l" {
		return []action{{kind: actionRedraw}}
	}
	if s.Transcript {
		switch key.Name {
		case "ctrl+o", "ctrl+c", "esc", "q":
			s.Transcript, s.Scroll = false, 0
		case "pageup", "ctrl+u":
			s.Scroll += max(1, s.Height/2)
		case "pagedown", "ctrl+d":
			s.Scroll = max(0, s.Scroll-max(1, s.Height/2))
		case "up", "k":
			s.Scroll++
		case "down", "j":
			s.Scroll = max(0, s.Scroll-1)
		case "ctrl+home":
			s.Scroll = 1 << 30
		case "ctrl+end":
			s.Scroll = 0
		}
		return nil
	}
	if len(s.Permissions) > 0 {
		request := s.Permissions[0]
		if request.DisplayOnly {
			return nil
		}
		if len(request.Choices) > 0 {
			switch key.Name {
			case "up", "k":
				s.PermissionSelected = (s.PermissionSelected + len(request.Choices) - 1) % len(request.Choices)
				return nil
			case "down", "j":
				s.PermissionSelected = (s.PermissionSelected + 1) % len(request.Choices)
				return nil
			}
			for i, choice := range request.Choices {
				if (choice.Key != "" && key.Name == choice.Key) || (key.Name == "enter" && i == s.PermissionSelected) {
					s.Permissions = s.Permissions[1:]
					s.PermissionSelected = 0
					return []action{{kind: actionPermission, permission: request.ID, decision: choice.Decision}}
				}
			}
			if key.Name != "esc" && key.Name != "n" && key.Name != "ctrl+c" {
				return nil
			}
		}
		switch key.Name {
		case "y", "enter", "n", "esc":
			request := s.Permissions[0]
			s.Permissions = s.Permissions[1:]
			s.PermissionSelected = 0
			return []action{{kind: actionPermission, permission: request.ID, decision: PermissionDecision{Allowed: key.Name == "y" || key.Name == "enter"}}}
		case "ctrl+c":
			return s.cancel()
		}
		return nil
	}
	if s.Searching {
		switch key.Name {
		case "ctrl+r":
			s.searchHistory(true)
		case "esc", "tab":
			s.Searching = false
		case "enter":
			s.Searching = false
			return s.submit()
		case "ctrl+c":
			s.Searching = false
			s.Editor.Set(s.historyDraft)
			s.Mode = s.historyDraftMode
			s.PastedContents = clonePastes(s.historyDraftPastes)
		case "backspace":
			var e Editor
			e.Set(s.Search)
			e.Backspace()
			s.Search = e.Text()
			s.searchHistory(false)
		default:
			if key.Text != "" {
				s.Search += key.Text
				s.searchHistory(false)
			}
		}
		return nil
	}
	if len(s.Completions) > 0 {
		switch key.Name {
		case "tab", "enter":
			selected := s.Completions[s.Selected]
			s.Editor.Set("/" + strings.TrimPrefix(selected.Name, "/") + " ")
			s.Completions = nil
			s.ArgumentHint = s.commandArgumentHint(s.Editor.Text())
			if key.Name == "enter" && (selected.Kind != "prompt" || !selected.RequiresArguments) {
				return s.submit()
			}
			return nil
		case "up":
			s.Selected = (s.Selected + len(s.Completions) - 1) % len(s.Completions)
			return nil
		case "down":
			s.Selected = (s.Selected + 1) % len(s.Completions)
			return nil
		case "esc":
			s.Completions = nil
			return nil
		}
	}
	s.Editor.prepareKey(key.Name == "ctrl+k" || key.Name == "ctrl+u" || key.Name == "ctrl+w" || key.Name == "ctrl+backspace" || key.Name == "alt+backspace" || key.Name == "alt+delete", key.Name == "ctrl+y" || key.Name == "alt+y")
	switch key.Name {
	case "ctrl+g":
		s.Editing = true
		return []action{{kind: actionEditor, text: ExpandPastedTextRefs(s.Editor.Text(), s.PastedContents)}}
	case "ctrl+c":
		if s.Busy() {
			return s.cancel()
		}
		if s.press("ctrl+c", now) {
			return s.EOF()
		}
		s.Editor.Reset()
		s.PastedContents = nil
		s.pendingImageSpace = false
		s.Status = "Press Ctrl-C again to exit"
	case "ctrl+d":
		if s.Editor.Text() != "" {
			s.Editor.Delete()
		} else if s.press("ctrl+d", now) {
			return s.EOF()
		} else {
			s.Status = "Press Ctrl-D again to exit"
		}
	case "esc":
		if s.Mode != "prompt" && s.Editor.Text() == "" {
			s.Mode = "prompt"
			return nil
		}
		if s.Busy() {
			return s.cancel()
		}
		if s.press("esc", now) {
			if strings.TrimSpace(s.Editor.Text()) != "" {
				s.history = append(s.history, HistoryEntry{Display: s.Editor.Text(), PastedContents: clonePastes(s.PastedContents)})
			}
			s.Editor.Reset()
			s.PastedContents = nil
			s.pendingImageSpace = false
			s.Status = ""
		} else if s.Editor.Text() != "" {
			s.Status = "Esc again to clear"
		}
	case "enter":
		if s.Editor.Cursor() > 0 && s.Editor.Text()[s.Editor.Cursor()-1] == '\\' {
			s.Editor.Backspace()
			s.Editor.Insert("\n")
		} else {
			return s.submit()
		}
	case "shift+enter", "alt+enter", "ctrl+j":
		s.Editor.Insert("\n")
	case "left", "ctrl+b":
		s.Editor.Left()
	case "right", "ctrl+f":
		s.Editor.Right()
	case "ctrl+left", "alt+left", "alt+b":
		s.Editor.WordLeft()
	case "ctrl+right", "alt+right", "alt+f":
		s.Editor.WordRight()
	case "home", "ctrl+a":
		s.Editor.Home()
	case "end", "ctrl+e":
		s.Editor.End()
	case "backspace", "ctrl+h":
		s.Editor.Backspace()
	case "delete":
		s.Editor.Delete()
	case "alt+delete":
		s.Editor.KillToEnd()
	case "ctrl+backspace", "alt+backspace", "ctrl+w":
		s.Editor.KillWordBefore()
	case "alt+d":
		s.Editor.KillWordAfter()
	case "ctrl+k":
		s.Editor.KillToEnd()
	case "ctrl+u":
		s.Editor.KillToStart()
	case "ctrl+y":
		s.Editor.Yank()
	case "alt+y":
		s.Editor.YankPop()
	case "ctrl+_", "ctrl+shift+-":
		s.Editor.Undo()
	case "up", "ctrl+p":
		if !s.Editor.Vertical(-1) {
			s.historyMove(1)
		}
	case "down", "ctrl+n":
		if !s.Editor.Vertical(1) {
			s.historyMove(-1)
		}
	case "ctrl+r":
		s.Searching, s.Search, s.historyDraft, s.historyIndex = true, "", s.Editor.Text(), 0
		s.historyDraftMode = s.Mode
		s.historyDraftPastes = clonePastes(s.PastedContents)
		s.searchHistory(false)
	case "ctrl+s":
		if s.stash == "" {
			s.stash = s.Editor.Text()
			s.stashMode = s.Mode
			s.stashPastes = clonePastes(s.PastedContents)
			s.Editor.Reset()
			s.PastedContents = nil
			s.pendingImageSpace = false
			s.Mode = "prompt"
		} else {
			text := s.Editor.Text()
			mode := s.Mode
			pastes := clonePastes(s.PastedContents)
			s.Editor.Set(s.stash)
			s.Mode = s.stashMode
			s.PastedContents = clonePastes(s.stashPastes)
			s.stashPastes = pastes
			s.stash = text
			s.stashMode = mode
		}
	case "ctrl+o":
		s.Transcript, s.Scroll = true, 0
	case "pageup":
		s.Scroll += max(1, s.Height/2)
	case "pagedown":
		s.Scroll = max(0, s.Scroll-max(1, s.Height/2))
	case "wheelup":
		s.Scroll++
	case "wheeldown":
		s.Scroll = max(0, s.Scroll-1)
	case "ctrl+home":
		s.Scroll = 1 << 30
	case "ctrl+end":
		s.Scroll = 0
	case "tab":
	default:
		if key.Text != "" {
			if pendingImageSpace && styleNonSpacePrintable(key) {
				s.Editor.Insert(" ")
			}
			if s.Editor.Text() == "" && s.Mode == "prompt" && key.Text == "!" {
				s.Mode = "bash"
			} else {
				s.Editor.Insert(key.Text)
			}
			s.Scroll = 0
		}
	}
	s.pendingImageSpace = false
	s.updateCompletions()
	return nil
}

func (s *State) Apply(event Event) {
	if event.Kind == TeammateTurnStarted {
		s.TeammateTurnID = event.TurnID
		s.Status = "Working…"
		return
	}
	if event.Kind == TeammateInboxChanged {
		_ = json.Unmarshal(event.Result, &s.TeamInbox)
		return
	}
	if event.Kind == PermissionModeChanged {
		s.PermissionMode = event.PermissionMode
		return
	}
	if event.Kind == ThinkingPickerOpened && event.ThinkingPicker != nil {
		s.OpenThinkingPicker(*event.ThinkingPicker)
		return
	}
	if event.Kind == ThinkingPickerClosed {
		s.ThinkingPicker = nil
		return
	}
	if event.Kind == CommandsChanged {
		s.commands = CloneCompletions(event.Commands)
		s.updateCompletions()
		return
	}
	if !event.Background && event.Kind != TeammateMessages && event.Kind != PermissionAsked && event.Kind != PermissionClosed && (event.TurnID != s.ActiveTurn || s.ActiveTurn == "") {
		return
	}
	switch event.Kind {
	case TeammateMessages:
		var rows []protocol.JSONObject
		_ = json.Unmarshal(event.Result, &rows)
		for _, row := range rows {
			if !teammateVisible(row) {
				continue
			}
			s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: "teammate", Text: teammateTranscriptText(row), Input: rawTeammatePresentation(row)})
		}
	case LocalCommandOutput:
		s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: "local_output", Text: event.Text})
	case LocalUserMessages:
		if len(event.UserMessages) == 0 {
			return
		}
		replaced := false
		for index := range s.Entries {
			if s.Entries[index].TurnID == event.TurnID && s.Entries[index].Kind == "user" {
				s.Entries[index].Text = event.UserMessages[0]
				replaced = true
				break
			}
		}
		for index, text := range event.UserMessages {
			if index == 0 && replaced {
				continue
			}
			s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: "user", Text: text})
		}
	case LocalProcessedMessages:
		if len(event.LocalMessages) == 0 {
			return
		}
		retained := make([]Entry, 0, len(s.Entries)+len(event.LocalMessages))
		for _, entry := range s.Entries {
			if entry.TurnID != event.TurnID || entry.Kind != "user" {
				retained = append(retained, entry)
			}
		}
		for _, message := range event.LocalMessages {
			retained = append(retained, Entry{TurnID: event.TurnID, Kind: message.Kind, Level: message.Level, Text: message.Text})
		}
		s.Entries = retained
	case TextDelta, ThinkingDelta:
		kind := "assistant"
		if event.Kind == ThinkingDelta {
			kind = "thinking"
		}
		last := len(s.Entries) - 1
		if last >= 0 && s.Entries[last].TurnID == event.TurnID && s.Entries[last].Kind == kind {
			s.Entries[last].Text += event.Text
		} else {
			s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: kind, Text: event.Text})
		}
	case ToolStarted:
		s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: "tool", ToolID: event.ToolID, ToolName: event.ToolName, Text: toolInputPresentation(event.ToolName, event.Input), Input: append(json.RawMessage(nil), event.Input...), Running: true})
	case BashProgress, BashFinished:
		found := false
		for i := len(s.Entries) - 1; i >= 0; i-- {
			entry := &s.Entries[i]
			if entry.TurnID == event.TurnID && entry.Kind == "bash_output" {
				entry.Text = event.Text
				entry.Running = event.Kind == BashProgress
				entry.IsError = event.IsError
				found = true
				break
			}
		}
		if !found {
			s.Entries = append(s.Entries, Entry{TurnID: event.TurnID, Kind: "bash_output", Text: event.Text, Running: event.Kind == BashProgress, IsError: event.IsError})
		}
	case ToolProgress, ToolFinished:
		for i := len(s.Entries) - 1; i >= 0; i-- {
			entry := &s.Entries[i]
			if entry.TurnID == event.TurnID && entry.Kind == "tool" && entry.ToolID == event.ToolID {
				if event.Text != "" {
					entry.Text += "\n" + event.Text
				}
				if len(event.Output) > 0 {
					entry.Output = append(json.RawMessage(nil), event.Output...)
					entry.Result = append(json.RawMessage(nil), event.Result...)
					entry.IsError = event.IsError
					entry.Text += "\n" + toolResultPresentation(*entry, false)
				}
				if event.Kind == ToolFinished {
					entry.Running = false
				}
				break
			}
		}
	case PermissionAsked:
		if event.Permission != nil && event.Permission.ID != "" && !s.cancelling {
			for _, request := range s.Permissions {
				if request.ID == event.Permission.ID {
					return
				}
			}
			s.Permissions = append(s.Permissions, *event.Permission)
			if len(s.Permissions) == 1 {
				s.PermissionSelected = 0
			}
		}
	case PermissionClosed:
		if event.Permission != nil {
			for i, request := range s.Permissions {
				if request.ID == event.Permission.ID {
					s.Permissions = append(s.Permissions[:i], s.Permissions[i+1:]...)
					if i == 0 {
						s.PermissionSelected = 0
						s.question = nil
					}
					break
				}
			}
		}
	case Notice:
		s.applyNotice(event)
	case TurnFailed:
		if event.Err != nil {
			s.Status = event.Err.Error()
		}
	case TurnFinished:
		// Actual Submit return controls turn ownership and queued submission.
	}
}

func (s *State) applyNotice(event Event) {
	if event.NoticeTimeout > 0 {
		s.Notification = event.Text
		s.notificationUntil = time.Now().Add(event.NoticeTimeout)
	} else {
		s.Status = event.Text
	}
}

func (s *State) FinishTurn(id string, err error) []action {
	if id != s.ActiveTurn || id == "" {
		return nil
	}
	for i := range s.Entries {
		if s.Entries[i].TurnID == id {
			s.Entries[i].Running = false
			s.Entries[i].Cancelled = s.cancelling
		}
	}
	s.ActiveTurn, s.Permissions = "", nil
	if s.cancelling {
		s.Status = "Request interrupted by user"
	} else if err != nil {
		s.Status = err.Error()
	} else {
		s.Status = ""
	}
	if s.Closing || s.TeammateTurnID != "" || len(s.Queue) == 0 {
		return nil
	}
	if s.cancelling {
		queued := []string{}
		for _, input := range s.Queue {
			if input.Text != "" {
				queued = append(queued, input.Text)
			}
			if s.PastedContents == nil {
				s.PastedContents = map[int]PastedContent{}
			}
			for id, value := range input.PastedContents {
				if value.Type == "image" {
					s.PastedContents[id] = value
				}
			}
		}
		if s.Editor.Text() != "" {
			queued = append(queued, s.Editor.Text())
		}
		cursor := len(strings.Join(queued, "\n")) - len(s.Editor.Text()) + s.Editor.Cursor()
		s.Editor.Set(strings.Join(queued, "\n"))
		s.Editor.cursor = boundaryAtOrAfter(s.Editor.Text(), min(cursor, len(s.Editor.Text())))
		s.Mode = "prompt"
		s.Queue = nil
		return nil
	}
	next := s.Queue[0]
	s.Queue = s.Queue[1:]
	return s.start(next)
}

func (s *State) FinishTeammateTurn(event Event) []action {
	if event.TurnID == "" || event.TurnID != s.TeammateTurnID {
		return nil
	}
	s.TeammateTurnID = ""
	if s.ActiveTurn != "" {
		return nil
	}
	s.ActiveTurn = event.TurnID
	return s.FinishTurn(event.TurnID, event.Err)
}
