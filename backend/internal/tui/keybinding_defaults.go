package tui

// KeybindingFeatures are the Source build gates. They are explicit host inputs,
// rather than inferred from environment variables or hidden user state.
type KeybindingFeatures struct {
	Kairos, KairosBrief, QuickSearch, TerminalPanel, MessageActions, VoiceMode bool
}

func DefaultKeybindings(platform string, terminalVT bool, features KeybindingFeatures) []Keybinding {
	var bindings []Keybinding
	add := func(context string, entries ...string) {
		for i := 0; i < len(entries); i += 2 {
			action := entries[i+1]
			bindings = append(bindings, Keybinding{Context: context, Chord: ParseChord(entries[i]), Action: &action})
		}
	}
	imagePaste := "ctrl+v"
	if platform == "windows" {
		imagePaste = "alt+v"
	}
	cycleMode := "shift+tab"
	if platform == "windows" && !terminalVT {
		cycleMode = "meta+m"
	}
	add("Global", "ctrl+c", "app:interrupt", "ctrl+d", "app:exit", "ctrl+l", "app:redraw", "ctrl+t", "app:toggleTodos", "ctrl+o", "app:toggleTranscript")
	if features.Kairos || features.KairosBrief {
		add("Global", "ctrl+shift+b", "app:toggleBrief")
	}
	add("Global", "ctrl+shift+o", "app:toggleTeammatePreview", "ctrl+r", "history:search")
	if features.QuickSearch {
		add("Global", "ctrl+shift+f", "app:globalSearch", "cmd+shift+f", "app:globalSearch", "ctrl+shift+p", "app:quickOpen", "cmd+shift+p", "app:quickOpen")
	}
	if features.TerminalPanel {
		add("Global", "meta+j", "app:toggleTerminal")
	}
	add("Chat", "escape", "chat:cancel", "ctrl+x ctrl+k", "chat:killAgents", cycleMode, "chat:cycleMode", "meta+p", "chat:modelPicker", "meta+o", "chat:fastMode", "meta+t", "chat:thinkingToggle", "meta+w", "chat:workflowKeywordToggle", "enter", "chat:submit", "up", "history:previous", "down", "history:next", "ctrl+_", "chat:undo", "ctrl+shift+-", "chat:undo", "ctrl+x ctrl+e", "chat:externalEditor", "ctrl+g", "chat:externalEditor", "ctrl+s", "chat:stash", imagePaste, "chat:imagePaste")
	if features.MessageActions {
		add("Chat", "shift+up", "chat:messageActions")
	}
	if features.VoiceMode {
		add("Chat", "space", "voice:pushToTalk")
	}
	add("Autocomplete", "tab", "autocomplete:accept", "escape", "autocomplete:dismiss", "up", "autocomplete:previous", "down", "autocomplete:next")
	add("Settings", "escape", "confirm:no", "up", "select:previous", "down", "select:next", "k", "select:previous", "j", "select:next", "ctrl+p", "select:previous", "ctrl+n", "select:next", "space", "select:accept", "enter", "settings:close", "/", "settings:search", "r", "settings:retry")
	add("Confirmation", "y", "confirm:yes", "n", "confirm:no", "enter", "confirm:yes", "escape", "confirm:no", "up", "confirm:previous", "down", "confirm:next", "tab", "confirm:nextField", "space", "confirm:toggle", "shift+tab", "confirm:cycleMode", "ctrl+e", "confirm:toggleExplanation", "ctrl+d", "permission:toggleDebug")
	add("Tabs", "tab", "tabs:next", "shift+tab", "tabs:previous", "right", "tabs:next", "left", "tabs:previous")
	add("Transcript", "ctrl+e", "transcript:toggleShowAll", "ctrl+c", "transcript:exit", "escape", "transcript:exit", "q", "transcript:exit")
	add("HistorySearch", "ctrl+r", "historySearch:next", "escape", "historySearch:accept", "tab", "historySearch:accept", "ctrl+c", "historySearch:cancel", "enter", "historySearch:execute")
	add("Task", "ctrl+b", "task:background")
	add("ThemePicker", "ctrl+t", "theme:toggleSyntaxHighlighting")
	add("Scroll", "pageup", "scroll:pageUp", "pagedown", "scroll:pageDown", "wheelup", "scroll:lineUp", "wheeldown", "scroll:lineDown", "ctrl+home", "scroll:top", "ctrl+end", "scroll:bottom", "ctrl+shift+c", "selection:copy", "cmd+c", "selection:copy")
	add("Help", "escape", "help:dismiss")
	add("Attachments", "right", "attachments:next", "left", "attachments:previous", "backspace", "attachments:remove", "delete", "attachments:remove", "down", "attachments:exit", "escape", "attachments:exit")
	add("Footer", "up", "footer:up", "ctrl+p", "footer:up", "down", "footer:down", "ctrl+n", "footer:down", "right", "footer:next", "left", "footer:previous", "enter", "footer:openSelected", "escape", "footer:clearSelection")
	add("MessageSelector", "up", "messageSelector:up", "down", "messageSelector:down", "k", "messageSelector:up", "j", "messageSelector:down", "ctrl+p", "messageSelector:up", "ctrl+n", "messageSelector:down", "ctrl+up", "messageSelector:top", "shift+up", "messageSelector:top", "meta+up", "messageSelector:top", "shift+k", "messageSelector:top", "ctrl+down", "messageSelector:bottom", "shift+down", "messageSelector:bottom", "meta+down", "messageSelector:bottom", "shift+j", "messageSelector:bottom", "enter", "messageSelector:select")
	if features.MessageActions {
		add("MessageActions", "up", "messageActions:prev", "down", "messageActions:next", "k", "messageActions:prev", "j", "messageActions:next", "meta+up", "messageActions:top", "meta+down", "messageActions:bottom", "super+up", "messageActions:top", "super+down", "messageActions:bottom", "shift+up", "messageActions:prevUser", "shift+down", "messageActions:nextUser", "escape", "messageActions:escape", "ctrl+c", "messageActions:ctrlc", "enter", "messageActions:enter", "c", "messageActions:c", "p", "messageActions:p")
	}
	add("DiffDialog", "escape", "diff:dismiss", "left", "diff:previousSource", "right", "diff:nextSource", "up", "diff:previousFile", "down", "diff:nextFile", "enter", "diff:viewDetails")
	add("ModelPicker", "left", "modelPicker:decreaseEffort", "right", "modelPicker:increaseEffort")
	add("Select", "up", "select:previous", "down", "select:next", "j", "select:next", "k", "select:previous", "ctrl+n", "select:next", "ctrl+p", "select:previous", "enter", "select:accept", "escape", "select:cancel")
	add("Plugin", "space", "plugin:toggle", "i", "plugin:install")
	return bindings
}
