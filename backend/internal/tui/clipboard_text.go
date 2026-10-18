package tui

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type textClipboardExecutor func(context.Context, []string, string, string, ...string) error
type textClipboardLinuxState struct {
	mu      sync.Mutex
	command string
}

var nativeTextClipboardLinux textClipboardLinuxState

// WriteClipboardText implements Source's native clipboard safety net and OSC
// 52 terminal producer. Every subprocess receives the explicit host environment
// and a context-bound stdin stream; exit joins it before reporting completion.
func WriteClipboardText(ctx context.Context, platform string, environment []string, text string, output io.Writer) error {
	if platform == "" {
		platform = runtime.GOOS
	}
	return writeClipboardText(ctx, platform, environment, text, output, executeTextClipboard, &nativeTextClipboardLinux)
}

func executeTextClipboard(ctx context.Context, environment []string, text, name string, args ...string) error {
	path := ""
	for _, item := range environment {
		if strings.HasPrefix(item, "PATH=") {
			path = strings.TrimPrefix(item, "PATH=")
		}
	}
	for _, directory := range filepath.SplitList(path) {
		candidate := filepath.Join(directory, name)
		if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
			candidate += ".exe"
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		command := exec.CommandContext(bounded, candidate, args...)
		command.Env = append([]string(nil), environment...)
		command.Stdin = strings.NewReader(text)
		return command.Run()
	}
	return exec.ErrNotFound
}

func writeClipboardText(ctx context.Context, platform string, environment []string, text string, output io.Writer, execute textClipboardExecutor, linux *textClipboardLinuxState) error {
	env := map[string]string{}
	for _, item := range environment {
		if key, value, ok := strings.Cut(item, "="); ok {
			env[key] = value
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if env["SSH_CONNECTION"] == "" {
		switch platform {
		case "darwin", "macos":
			_ = execute(ctx, environment, text, "pbcopy")
		case "windows", "win32":
			_ = execute(ctx, environment, text, "clip.exe")
		case "linux":
			linux.mu.Lock()
			commands := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
			for _, command := range commands {
				if linux.command != "" && linux.command != command[0] {
					continue
				}
				if err := execute(ctx, environment, text, command[0], command[1:]...); err == nil {
					linux.command = command[0]
					break
				}
				if linux.command != "" || ctx.Err() != nil {
					break
				}
				if command[0] == "xsel" {
					linux.command = "unavailable"
				}
			}
			linux.mu.Unlock()
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	sequence := "\x1b]52;c;" + encoded + "\a"
	if env["TERM"] == "xterm-kitty" || env["TERM_PROGRAM"] == "kitty" {
		sequence = "\x1b]52;c;" + encoded + "\x1b\\"
	}
	if env["TMUX"] != "" {
		args := []string{"load-buffer", "-w", "-"}
		if env["LC_TERMINAL"] == "iTerm2" {
			args = []string{"load-buffer", "-"}
		}
		if execute(ctx, environment, text, "tmux", args...) == nil {
			sequence = "\x1bPtmux;\x1b\x1b]52;c;" + encoded + "\a\x1b\\"
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	_, err := io.WriteString(output, sequence)
	return err
}
