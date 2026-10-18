package tui

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// RenderConversationText uses the current terminal transcript renderer with
// the same original history consumer as a resumed REPL. Export never replays a
// turn or reconstructs a parallel conversation from provider request bodies.
func RenderConversationText(records []protocol.JSONObject, presentation ...ConversationExportPresentation) string {
	state := State{Width: 80, Transcript: true}
	if len(presentation) > 0 && presentation[0].Columns > 0 {
		state.Width = presentation[0].Columns
	}
	state.SetInitialHistory(records, true)
	lines := state.transcriptLines()
	for index, line := range lines {
		if strings.HasPrefix(line, "You: ") {
			lines[index] = "❯ " + strings.TrimPrefix(line, "You: ")
		}
		if strings.HasPrefix(line, "Claude: ") {
			lines[index] = "⏺ " + strings.TrimPrefix(line, "Claude: ")
		}
	}
	body := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if len(presentation) > 0 {
		return RenderConversationExportHeader(presentation[0]) + "\n\n" + body
	}
	return body
}
