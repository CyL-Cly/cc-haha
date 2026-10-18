package errorlog

import (
	"path/filepath"
	"strings"
)

// DebugPresentation observes this actual sink's current debug decision and
// destination. It never creates a logger, writes a file or changes its policy.
func (s *NativeSink) DebugPresentation() (bool, string) {
	if s == nil {
		return false, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.debugMode {
		return false, ""
	}
	args := s.options.Arguments
	for _, arg := range args {
		if arg == "--debug-to-stderr" || arg == "-d2e" {
			return true, "stderr"
		}
	}
	for index, arg := range args {
		if strings.HasPrefix(arg, "--debug-file=") {
			return true, strings.TrimPrefix(arg, "--debug-file=")
		}
		if arg == "--debug-file" && index+1 < len(args) {
			return true, args[index+1]
		}
	}
	metadata := s.options.Metadata()
	if path, exists := metadata.Environment["CLAUDE_CODE_DEBUG_LOGS_DIR"]; exists {
		return true, path
	}
	return true, filepath.Join(metadata.ConfigDir, "debug", metadata.SessionID+".txt")
}
