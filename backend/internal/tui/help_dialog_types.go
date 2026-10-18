package tui

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

type TerminalHelpData struct {
	Version   string
	Commands  []protocol.JSONObject
	Shortcuts TerminalShortcutHelp
}
