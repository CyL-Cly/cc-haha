package clibootstrap

import (
	_ "embed"
	"io"

	"github.com/charmbracelet/x/term"
	"github.com/dop251/goja"
)

// Only the installed Chalk generic supports-color primitive is embedded. It
// receives immutable environment/argv/TTY observations, no process or IO API.
//
//go:embed assets/tmux_supports_color.js
var tmuxSupportsColor string

func nativeTmuxColor(args []string, env map[string]string, platform string, tty bool) (bool, error) {
	vm := goja.New()
	if e := vm.Set("process", map[string]any{"env": env, "argv": append([]string{"go-cli"}, args...), "platform": platform}); e != nil {
		return false, e
	}
	if e := vm.Set("tty", map[string]any{"isatty": func(int) bool { return tty }}); e != nil {
		return false, e
	}
	// Windows is rejected by the fast path before using color. The release is
	// only consumed by the unreachable Windows-specific generic color branch.
	if e := vm.Set("os", map[string]any{"release": func() string { return "" }}); e != nil {
		return false, e
	}
	value, e := vm.RunString(tmuxSupportsColor)
	if e != nil {
		return false, e
	}
	stdout := value.ToObject(vm).Get("stdout")
	if stdout == nil || !stdout.ToBoolean() {
		return false, nil
	}
	return stdout.ToObject(vm).Get("hasBasic").ToBoolean(), nil
}
func nativeTmuxYellow(args []string, env map[string]string, platform string, output io.Writer) (func(string) string, error) {
	tty := false
	if file, ok := output.(term.File); ok {
		tty = term.IsTerminal(file.Fd())
	}
	enabled, e := nativeTmuxColor(args, env, platform, tty)
	if e != nil {
		return nil, e
	}
	return func(text string) string {
		if enabled {
			return "\x1b[33m" + text + "\x1b[39m"
		}
		return text
	}, nil
}
