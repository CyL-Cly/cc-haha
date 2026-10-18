package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) populateTerminalExportBusinessNotices(global *protocol.JSONObject, value *tui.ConversationExportPresentation) error {
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	if settings != nil {
		var announcements []string
		if json.Unmarshal(settings.Get("companyAnnouncements"), &announcements) == nil && len(announcements) > 0 {
			index := 0
			var startups float64
			if global != nil {
				_ = json.Unmarshal(global.Get("numStartups"), &startups)
			}
			if startups != 1 {
				index = int(nativeMathRandom() * float64(len(announcements)))
			}
			value.Announcement = announcements[index]
		}
	}
	value.DebugMode, value.DebugDestination = m.errorLogSink.DebugPresentation()
	env := m.nativeOpenAIEnvironment()
	value.TmuxSession, value.TmuxPrefix = env["CLAUDE_CODE_TMUX_SESSION"], env["CLAUDE_CODE_TMUX_PREFIX"]
	value.TmuxPrefixConflicts = env["CLAUDE_CODE_TMUX_PREFIX_CONFLICTS"] != ""
	return nil
}
