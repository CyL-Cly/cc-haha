package teamspawn

import (
	"fmt"
	"strings"
	"time"
)

// SelectBackend follows the original launch snapshot: noninteractive always
// stays in-process; auto falls back only when detection itself fails.
func (p *PaneRuntime) SelectBackend(mode string, noninteractive, preferTmux bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == "" {
		if mode == "" {
			mode = "auto"
		}
		p.mode = mode
	}
	if noninteractive || p.mode == "in-process" {
		return "in-process", nil
	}
	if p.backend != "" {
		return p.backend, nil
	}
	inITerm := p.environment["TERM_PROGRAM"] == "iTerm.app" || p.environment["ITERM_SESSION_ID"] != ""
	if p.mode == "auto" && !p.insideTmux && !inITerm {
		return "in-process", nil
	}
	if p.insideTmux {
		p.backend = "tmux"
		return p.backend, nil
	}
	if inITerm && !preferTmux {
		_, _, code := p.run("it2", []string{"session", "list"})
		if code == 0 {
			p.backend = "iterm2"
			return p.backend, nil
		}
	}
	_, _, code := p.run("tmux", []string{"-V"})
	if code == 0 {
		p.backend = "tmux"
		p.needsSetup = inITerm && !preferTmux
		return p.backend, nil
	}
	if p.mode == "auto" {
		p.backend = "in-process"
		return p.backend, nil
	}
	if inITerm {
		return "", fmt.Errorf("iTerm2 detected but it2 CLI not installed. Install it2 with: pip install it2")
	}
	return "", fmt.Errorf("To use agent swarms, install tmux and start a tmux session with: tmux new-session -s claude")
}
func (p *PaneRuntime) tmux(args ...string) (string, string, int) {
	if !p.insideTmux {
		args = append([]string{"-L", fmt.Sprintf("claude-swarm-%d", p.pid)}, args...)
	}
	return p.run("tmux", args)
}
func paneLines(v string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(v), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
func tmuxColor(color string) string {
	switch color {
	case "purple":
		return "magenta"
	case "orange":
		return "colour208"
	case "pink":
		return "colour205"
	}
	return color
}
func (p *PaneRuntime) decoratePane(pane, name, color string) {
	c := tmuxColor(color)
	p.tmux("select-pane", "-t", pane, "-P", "bg=default,fg="+c)
	p.tmux("set-option", "-p", "-t", pane, "pane-border-style", "fg="+c)
	p.tmux("set-option", "-p", "-t", pane, "pane-active-border-style", "fg="+c)
	p.tmux("select-pane", "-t", pane, "-T", name)
	p.tmux("set-option", "-p", "-t", pane, "pane-border-format", "#[fg="+c+",bold] #{pane_title} #[default]")
}

// CreatePane serializes physical splits, as the Source module's promise lock
// does. All pane IDs come from the chosen backend's actual subprocess output.
func (p *PaneRuntime) CreatePane(backend, name, color string) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if backend == "iterm2" {
		return p.createITermPane()
	}
	if backend != "tmux" {
		return "", false, fmt.Errorf("unknown pane backend %s", backend)
	}
	var window, firstPane string
	if p.insideTmux {
		firstPane = p.environment["TMUX_PANE"]
		if firstPane == "" {
			out, _, code := p.run("tmux", []string{"display-message", "-p", "#{pane_id}"})
			if code == 0 {
				firstPane = out
			}
		}
		if p.leaderWindow == "" {
			args := []string{"display-message"}
			if p.environment["TMUX_PANE"] != "" {
				args = append(args, "-t", p.environment["TMUX_PANE"])
			}
			args = append(args, "-p", "#{session_name}:#{window_index}")
			out, _, code := p.run("tmux", args)
			if code == 0 {
				p.leaderWindow = out
			}
		}
		window = p.leaderWindow
		if firstPane == "" || window == "" {
			return "", false, fmt.Errorf("Could not determine current tmux pane/window")
		}
	} else {
		window = "claude-swarm:swarm-view"
		_, _, code := p.tmux("has-session", "-t", "claude-swarm")
		if code != 0 {
			out, stderr, code := p.tmux("new-session", "-d", "-s", "claude-swarm", "-n", "swarm-view", "-P", "-F", "#{pane_id}")
			if code != 0 {
				if stderr == "" {
					stderr = "Unknown error"
				}
				return "", false, fmt.Errorf("Failed to create swarm session: %s", stderr)
			}
			firstPane = out
		} else {
			out, _, _ := p.tmux("list-windows", "-t", "claude-swarm", "-F", "#{window_name}")
			has := false
			for _, v := range paneLines(out) {
				has = has || v == "swarm-view"
			}
			if has {
				out, _, _ := p.tmux("list-panes", "-t", window, "-F", "#{pane_id}")
				if rows := paneLines(out); len(rows) > 0 {
					firstPane = rows[0]
				}
			} else {
				out, stderr, code := p.tmux("new-window", "-t", "claude-swarm", "-n", "swarm-view", "-P", "-F", "#{pane_id}")
				if code != 0 {
					return "", false, fmt.Errorf("Failed to create swarm-view window: %s", stderr)
				}
				firstPane = out
			}
		}
	}
	out, _, code := p.tmux("list-panes", "-t", window, "-F", "#{pane_id}")
	if code != 0 {
		if p.insideTmux {
			return "", false, fmt.Errorf("Could not determine pane count for current window")
		}
		return "", false, fmt.Errorf("Could not determine pane count for swarm window")
	}
	count := len(paneLines(out))
	first := count == 1 && (p.insideTmux || !p.firstExternal)
	pane := firstPane
	if !p.insideTmux && first {
		p.firstExternal = true
		p.tmux("set-option", "-w", "-t", window, "pane-border-status", "top")
	} else {
		args := []string{"split-window", "-t", firstPane, "-h", "-l", "70%", "-P", "-F", "#{pane_id}"}
		if !first {
			out, _, _ := p.tmux("list-panes", "-t", window, "-F", "#{pane_id}")
			rows := paneLines(out)
			if p.insideTmux && len(rows) > 0 {
				rows = rows[1:]
			}
			n := len(rows)
			target := ""
			if n > 0 {
				target = rows[(n-1)/2]
			}
			direction := "-h"
			if n%2 == 1 {
				direction = "-v"
			}
			args = []string{"split-window", "-t", target, direction, "-P", "-F", "#{pane_id}"}
		}
		out, stderr, code := p.tmux(args...)
		if code != 0 {
			return "", false, fmt.Errorf("Failed to create teammate pane: %s", stderr)
		}
		pane = out
	}
	p.decoratePane(pane, name, color)
	out, _, _ = p.tmux("list-panes", "-t", window, "-F", "#{pane_id}")
	rows := paneLines(out)
	if p.insideTmux && len(rows) > 2 {
		p.tmux("select-layout", "-t", window, "main-vertical")
		p.tmux("resize-pane", "-t", rows[0], "-x", "30%")
	} else if !p.insideTmux && len(rows) > 1 {
		p.tmux("select-layout", "-t", window, "tiled")
	}
	time.Sleep(200 * time.Millisecond)
	if first && p.insideTmux {
		p.tmux("set-option", "-w", "-t", window, "pane-border-status", "top")
	}
	return pane, first, nil
}
func (p *PaneRuntime) createITermPane() (string, bool, error) {
	for {
		first := len(p.itermSessions) == 0
		args := []string{"session", "split"}
		target := ""
		if first {
			args = append(args, "-v")
			if _, id, ok := strings.Cut(p.environment["ITERM_SESSION_ID"], ":"); ok && id != "" {
				args = append(args, "-s", id)
			}
		} else {
			target = p.itermSessions[len(p.itermSessions)-1]
			args = append(args, "-s", target)
		}
		out, stderr, code := p.run("it2", args)
		if code != 0 {
			if target != "" {
				out, _, listCode := p.run("it2", []string{"session", "list"})
				if listCode == 0 && !strings.Contains(out, target) {
					p.itermSessions = p.itermSessions[:len(p.itermSessions)-1]
					continue
				}
			}
			return "", false, fmt.Errorf("Failed to create iTerm2 split pane: %s", stderr)
		}
		_, pane, ok := strings.Cut(out, "Created new pane:")
		if !ok || strings.TrimSpace(pane) == "" {
			return "", false, fmt.Errorf("Failed to parse session ID from split output: %s", out)
		}
		pane = strings.TrimSpace(strings.Split(pane, "\n")[0])
		p.itermSessions = append(p.itermSessions, pane)
		return pane, first, nil
	}
}
func (p *PaneRuntime) SendCommand(backend, pane, command string) error {
	var stderr string
	var code int
	if backend == "tmux" {
		_, stderr, code = p.tmux("send-keys", "-t", pane, command, "Enter")
	} else {
		_, stderr, code = p.run("it2", []string{"session", "run", "-s", pane, command})
	}
	if code != 0 {
		return fmt.Errorf("Failed to send command to pane %s: %s", pane, stderr)
	}
	return nil
}
func (p *PaneRuntime) InsideTmux() bool { return p.insideTmux }
