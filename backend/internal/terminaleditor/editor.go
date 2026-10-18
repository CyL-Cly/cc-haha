// Package terminaleditor retains the external editor selected by the terminal
// process. It owns no session state or permission policy.
package terminaleditor

import (
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func Select(environment map[string]string, platform string, stat func(string) (fs.FileInfo, error), cwd ...string) string {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := protocol.TrimJavaScriptString(environment[key]); value != "" {
			return value
		}
	}
	if platform == "win32" || platform == "windows" {
		return "start /wait notepad"
	}
	for _, command := range []string{"code", "vi", "nano"} {
		for _, directory := range filepath.SplitList(environment["PATH"]) {
			if directory == "" {
				continue
			}
			if !filepath.IsAbs(directory) && len(cwd) > 0 {
				directory = filepath.Join(cwd[0], directory)
			}
			info, err := stat(filepath.Join(directory, command))
			if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				return command
			}
		}
	}
	return ""
}

func DisplayName(editor string) string {
	names := map[string]string{"code": "VS Code", "cursor": "Cursor", "windsurf": "Windsurf", "antigravity": "Antigravity", "vi": "Vim", "vim": "Vim", "nano": "nano", "notepad": "Notepad", "start /wait notepad": "Notepad", "emacs": "Emacs", "subl": "Sublime Text", "atom": "Atom"}
	if editor == "" {
		return "IDE"
	}
	if name := ideintegration.IDEName(editor); name != "IDE" {
		return name
	}
	if name := names[strings.ToLower(protocol.TrimJavaScriptString(editor))]; name != "" {
		return name
	}
	command := strings.Split(editor, " ")[0]
	if command != "" {
		command = strings.ToLower(filepath.Base(command))
		if name := names[command]; name != "" {
			return name
		}
		return upperFirst(command)
	}
	return upperFirst(editor)
}

func upperFirst(value string) string {
	runes := []rune(strings.ToLower(value))
	if len(runes) != 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

func Command(editor, path string) string {
	switch editor {
	case "code":
		editor = "code -w"
	case "subl":
		editor = "subl --wait"
	}
	// The editor is the user's command, with its arguments. The plan path is
	// an argv value; do not interpret filename contents as shell expansions.
	return editor + " " + quotePath(path)
}
