package tui

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

type terminalResumeUI struct {
	search                   Editor
	rename                   Editor
	mode                     string
	focused, tag             int
	branchOnly, allWorktrees bool
	preview                  []protocol.JSONObject
}
