package clibootstrap

import (
	"path/filepath"

	"golang.org/x/text/unicode/norm"
)

// Bound only by the actual native MainPrint task-store construction. The
// shared typed process environment is live; a warmed root or child retains
// the same Service instead of reconstructing its lifecycle or cache state.
func (m *MainPrint) nativeTaskConfigDirectory() string {
	n := m.options.Native
	directory, present := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
	if !present {
		directory = filepath.Join(n.Home, ".claude")
	}
	directory = norm.NFC.String(directory)
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(m.workingDirectory(), directory)
	}
	return directory
}
