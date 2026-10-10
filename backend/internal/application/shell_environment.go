package application

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

const shellEnvironmentMarker = "__CC_HAHA_TERMINAL_ENV_START__"

// terminalEnvironment owns one original login-shell capture promise. Canceling
// an API waiter never cancels this process memo or replaces it with ambient env.
type terminalEnvironment struct {
	base     map[string]string
	platform string
	tty      bool
	once     sync.Once
	done     chan struct{}
	captured map[string]string
}

func newTerminalEnvironment(base map[string]string) *terminalEnvironment {
	copy := make(map[string]string, len(base))
	for key, value := range base {
		copy[key] = value
	}
	return &terminalEnvironment{base: copy, platform: runtime.GOOS, tty: term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()), done: make(chan struct{})}
}
func (e *terminalEnvironment) Get(context.Context) (map[string]string, error) {
	e.once.Do(func() { go func() { e.captured = e.capture(); close(e.done) }() })
	<-e.done
	merged := make(map[string]string, len(e.base)+len(e.captured))
	for k, v := range e.captured {
		merged[k] = v
	}
	for k, v := range e.base {
		merged[k] = v
	}
	if e.captured != nil {
		delimiter := ":"
		if e.platform == "windows" {
			delimiter = ";"
		}
		if path := mergeShellPaths(delimiter, e.captured["PATH"], e.base["PATH"]); path != "" {
			merged["PATH"] = path
		}
	}
	return merged, nil
}
func (e *terminalEnvironment) capture() map[string]string {
	flag := strings.ToLower(strings.TrimSpace(e.base["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"]))
	if e.platform == "windows" || e.tty || flag == "1" || flag == "true" || flag == "yes" || flag == "on" {
		return nil
	}
	var shell string
	for _, path := range []string{e.base["SHELL"], "/bin/zsh", "/usr/bin/zsh", "/bin/bash", "/usr/bin/bash"} {
		name := filepath.Base(path)
		if !strings.Contains(name, "zsh") && !strings.Contains(name, "bash") {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			shell = path
			break
		}
	}
	if shell == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "printf '%s\\0' '__CC_HAHA_TERMINAL_ENV_START__'\nenv -0")
	for key, value := range e.base {
		if key != "BASH_ENV" && key != "ENV" && key != "SHELL" && key != "PS1" && key != "PROMPT" {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	command.Env = append(command.Env, "SHELL="+shell, "PS1=", "PROMPT=")
	stdout, stderr := &boundedShellOutput{cancel: cancel}, &boundedShellOutput{cancel: cancel}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return nil
	}
	return parseShellEnvironment(string(protocol.DecodeJavaScriptUTF8(stdout.Bytes())))
}

type boundedShellOutput struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (b *boundedShellOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		b.cancel()
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}
func parseShellEnvironment(output string) map[string]string {
	if at := strings.Index(output, shellEnvironmentMarker+"\x00"); at >= 0 {
		output = output[at+len(shellEnvironmentMarker)+1:]
	}
	values := map[string]string{}
	for _, entry := range strings.Split(output, "\x00") {
		key, value, found := strings.Cut(entry, "=")
		if found && key != "" {
			values[key] = value
		}
	}
	return values
}
func mergeShellPaths(delimiter string, values ...string) string {
	seen := map[string]bool{}
	paths := []string{}
	for _, list := range values {
		for _, path := range strings.Split(list, delimiter) {
			path = strings.TrimFunc(path, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return strings.Join(paths, delimiter)
}
