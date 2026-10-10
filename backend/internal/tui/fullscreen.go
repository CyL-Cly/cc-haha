package tui

import (
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FullscreenEnabled is Source isFullscreenEnvEnabled's startup decision. The
// caller supplies its frozen process environment and the physical tmux probe;
// rendering never reads the agent process's ambient environment.
func FullscreenEnabled(environment map[string]string, probe func() (string, error)) bool {
	value := strings.ToLower(protocol.TrimJavaScriptString(environment["CLAUDE_CODE_NO_FLICKER"]))
	switch value {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	}
	if environment["TMUX"] != "" {
		term, program := environment["TERM"], environment["TERM_PROGRAM"]
		if program == "iTerm.app" && !strings.HasPrefix(term, "screen") && !strings.HasPrefix(term, "tmux") {
			return false
		}
		if program == "" && probe != nil {
			if output, err := probe(); err == nil && protocol.TrimJavaScriptString(output) == "1" {
				return false
			}
		}
	}
	return environment["USER_TYPE"] == "ant"
}
