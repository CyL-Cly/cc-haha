package clibootstrap

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/unicode/norm"
)

// TerminalKeybindingsPaths retains Source's displayed getKeybindingsPath value
// while resolving native filesystem work against this process's current CWD.
// Empty and absent CLAUDE_CONFIG_DIR have distinct Source meanings.
func (n *NativeBoundary) TerminalKeybindingsPaths(cwd string) (display, physical string) {
	config, exists := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
	if !exists {
		config = filepath.Join(n.Home, ".claude")
	}
	display = filepath.Join(norm.NFC.String(config), "keybindings.json")
	physical = display
	if !filepath.IsAbs(physical) {
		physical = filepath.Join(cwd, physical)
	}
	return display, physical
}

type nativeKeybindingsFileError struct {
	cause error
	text  string
}

func (e *nativeKeybindingsFileError) Error() string { return e.text }
func (e *nativeKeybindingsFileError) Unwrap() error { return e.cause }

func terminalKeybindingsFileError(err error, physical, display string) error {
	if err == nil {
		return nil
	}
	presentation := err
	requested := ""
	if path, ok := err.(*os.PathError); ok {
		copy := *path
		if path.Op == "open" || filepath.Clean(path.Path) == filepath.Clean(physical) {
			copy.Path = display
		}
		if path.Op == "mkdir" {
			requested = display
		}
		presentation = &copy
	}
	text := storage.JavaScriptErrorText(presentation, storage.JavaScriptErrorContext{RequestedPath: requested})
	return &nativeKeybindingsFileError{cause: err, text: strings.TrimPrefix(text, "Error: ")}
}
