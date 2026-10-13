package clibootstrap

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Explicit CLI prompt files are user-selected startup reads. They feed the
// existing custom/default prompt consumer and are not Agent Read tool calls.
func resolveNativeCLIStartupPromptFiles(o MainPrintOptions) (MainPrintOptions, error) {
	if o.DetachedAgent != nil || o.TeamWorker != nil {
		return o, nil
	}
	for _, request := range []struct {
		path, label, textFlag, fileFlag string
		append                          bool
	}{
		{o.Arguments.SystemPromptFile, "System prompt", "--system-prompt", "--system-prompt-file", false},
		{o.Arguments.AppendSystemPromptFile, "Append system prompt", "--append-system-prompt", "--append-system-prompt-file", true},
	} {
		if request.path == "" {
			continue
		}
		if o.Native == nil {
			return o, mainCap("explicit CLI prompt file requires native filesystem")
		}
		text := o.Arguments.SystemPrompt
		if request.append {
			text = o.Arguments.AppendSystemPrompt
		}
		if text != "" {
			return o, fmt.Errorf("Error: Cannot use both %s and %s. Please use only one.", request.textFlag, request.fileFlag)
		}
		path := request.path
		if o.Platform == "windows" {
			path = nativepaths.ResolveWindows(path, o.Native.CWD)
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(o.Native.CWD, path)
		} else {
			path = filepath.Clean(path)
		}
		data, err := o.Native.readFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return o, fmt.Errorf("Error: %s file not found: %s", request.label, path)
			}
			message := strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: ")
			return o, fmt.Errorf("Error reading %s file: %s", strings.ToLower(request.label), message)
		}
		value := string(protocol.DecodeJavaScriptUTF8(data))
		if request.append {
			o.Arguments.AppendSystemPrompt = value
		} else {
			o.Arguments.SystemPrompt, o.Arguments.HasCustomSystemPrompt = value, true
		}
	}
	return o, nil
}
