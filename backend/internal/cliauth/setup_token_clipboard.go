package cliauth

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SetupTokenClipboard owns the same native/tmux clipboard operations as
// setClipboard. Local native copy starts before waiting for tmux. Every child
// is canceled and joined at terminal shutdown; none outlives its command owner.
type SetupTokenClipboard struct {
	ctx           context.Context
	cancel        context.CancelFunc
	environment   map[string]string
	platform, cwd string
	mu            sync.Mutex
	closed        bool
	workers       sync.WaitGroup
	linuxCopy     string
	// Run is the OS process primitive. Nil uses the actual private/frozen env.
	Run func(context.Context, string, []string, string) int
}

func NewSetupTokenClipboard(ctx context.Context, platform, cwd string, env map[string]string) *SetupTokenClipboard {
	ctx, cancel := context.WithCancel(ctx)
	copy := map[string]string{}
	for k, v := range env {
		copy[k] = v
	}
	return &SetupTokenClipboard{ctx: ctx, cancel: cancel, platform: platform, cwd: cwd, environment: copy}
}
func (c *SetupTokenClipboard) run(ctx context.Context, name string, args []string, text string) int {
	return c.runAdmitted(ctx, name, args, text, nil)
}
func (c *SetupTokenClipboard) runAdmitted(ctx context.Context, name string, args []string, text string, admitted chan struct{}) int {
	if admitted != nil {
		defer func() {
			if admitted != nil {
				close(admitted)
			}
		}()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if c.Run != nil {
		return c.Run(ctx, name, args, text)
	}
	path := ""
	for _, dir := range filepath.SplitList(c.environment["PATH"]) {
		names := []string{name}
		if c.platform == "windows" && filepath.Ext(name) == "" {
			extensions := c.environment["PATHEXT"]
			if extensions == "" {
				extensions = ".COM;.EXE;.BAT;.CMD"
			}
			for _, ext := range strings.Split(extensions, ";") {
				names = append(names, name+strings.ToLower(ext))
			}
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if info, e := os.Stat(candidate); e == nil && !info.IsDir() && (c.platform == "windows" || info.Mode()&0111 != 0) {
				path = candidate
				break
			}
		}
		if path != "" {
			break
		}
	}
	if path == "" {
		return -1
	}
	command := exec.CommandContext(ctx, path, args...)
	setupClipboardProcess(command)
	command.Dir = c.cwd
	for k, v := range c.environment {
		command.Env = append(command.Env, k+"="+v)
	}
	command.Stdin = strings.NewReader(text)
	command.WaitDelay = 2 * time.Second
	if command.Start() != nil {
		return 1
	}
	if admitted != nil {
		close(admitted)
		admitted = nil
	}
	if command.Wait() != nil {
		return 1
	}
	return 0
}
func (c *SetupTokenClipboard) native(text string, admitted chan struct{}) {
	run := func(name string, args []string) int {
		channel := admitted
		admitted = nil
		return c.runAdmitted(c.ctx, name, args, text, channel)
	}
	defer func() {
		if admitted != nil {
			close(admitted)
		}
	}()

	switch c.platform {
	case "darwin":
		run("pbcopy", nil)
	case "windows":
		run("clip", nil)
	case "linux":
		c.mu.Lock()
		cached := c.linuxCopy
		c.mu.Unlock()
		if cached == "none" {
			return
		}
		tools := []string{cached}
		if cached == "" {
			tools = []string{"wl-copy", "xclip", "xsel"}
		}
		for _, name := range tools {
			var args []string
			if name == "xclip" {
				args = []string{"-selection", "clipboard"}
			}
			if name == "xsel" {
				args = []string{"--clipboard", "--input"}
			}
			if run(name, args) == 0 {
				c.mu.Lock()
				c.linuxCopy = name
				c.mu.Unlock()
				return
			}
		}
		c.mu.Lock()
		c.linuxCopy = "none"
		c.mu.Unlock()
	}
}
func (c *SetupTokenClipboard) Copy(ctx context.Context, text string) string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ""
	}
	c.workers.Add(1)
	native := c.environment["SSH_CONNECTION"] == ""
	if native {
		c.workers.Add(1)
	}
	c.mu.Unlock()
	defer c.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	if c.ctx.Err() != nil {
		cancel()
	}
	if native {
		admitted := make(chan struct{})
		go func() { defer c.workers.Done(); c.native(text, admitted) }()
		<-admitted
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	if c.environment["TMUX"] != "" {
		args := []string{"load-buffer", "-w", "-"}
		if c.environment["LC_TERMINAL"] == "iTerm2" {
			args = []string{"load-buffer", "-"}
		}
		if c.run(ctx, "tmux", args, text) == 0 {
			return "\x1bPtmux;\x1b\x1b]52;c;" + b64 + "\a\x1b\\"
		}
	}
	terminator := "\a"
	if setupTokenKitty(c.environment) {
		terminator = "\x1b\\"
	}
	return "\x1b]52;c;" + b64 + terminator
}
func setupTokenKitty(env map[string]string) bool {
	// Preserve detectTerminal's earlier competing identifiers, then TERM,
	// TERM_PROGRAM, multiplexers and the later Kitty fallback.
	if env["CURSOR_TRACE_ID"] != "" || strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "cursor") || strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "windsurf") || strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "antigravity") || env["VisualStudioVersion"] != "" || env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm" {
		return false
	}
	bundle := strings.ToLower(env["__CFBundleIdentifier"])
	for _, v := range []string{"vscodium", "windsurf", "com.google.android.studio", "pycharm", "intellij", "webstorm", "phpstorm", "rubymine"} {
		if strings.Contains(bundle, v) {
			return false
		}
	}
	if env["TERM"] == "xterm-ghostty" {
		return false
	}
	if strings.Contains(env["TERM"], "kitty") {
		return true
	}
	if env["TERM_PROGRAM"] != "" {
		return env["TERM_PROGRAM"] == "kitty"
	}
	if env["TMUX"] != "" || env["STY"] != "" || env["KONSOLE_VERSION"] != "" || env["GNOME_TERMINAL_SERVICE"] != "" || env["XTERM_VERSION"] != "" || env["VTE_VERSION"] != "" || env["TERMINATOR_UUID"] != "" {
		return false
	}
	return env["KITTY_WINDOW_ID"] != ""
}
func (c *SetupTokenClipboard) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}
