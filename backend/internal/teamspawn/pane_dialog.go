package teamspawn

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

func (p *PaneRuntime) DialogSupportsHideShow() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backend == "tmux"
}

// DialogPane executes the real pane consumer with the mounted dialog's owned
// cancellation. Pane identity still comes from the actual team config file.
func (p *PaneRuntime) DialogPane(ctx context.Context, action, backend, pane string) error {
	command := "tmux"
	args := []string{}
	if backend == "iterm2" {
		command = "it2"
		switch action {
		case "view":
			args = []string{"session", "focus", "-s", pane}
		case "kill":
			args = []string{"session", "close", "-f", "-s", pane}
		default:
			return fmt.Errorf("unknown teammate pane action")
		}
	} else {
		if action == "kill" && backend != "tmux" {
			return nil
		}
		if !p.insideTmux {
			args = append(args, "-L", fmt.Sprintf("claude-swarm-%d", p.pid))
		}
		switch action {
		case "view":
			args = append(args, "select-pane", "-t", pane)
		case "kill":
			args = append(args, "kill-pane", "-t", pane)
		default:
			return fmt.Errorf("unknown teammate pane action")
		}
	}
	return p.runDialog(ctx, command, args)
}

func (p *PaneRuntime) runDialog(ctx context.Context, command string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		extensions = []string{".exe", ".com", ".bat", ".cmd"}
	}
	executable := ""
	for _, directory := range filepath.SplitList(p.environment["PATH"]) {
		if directory == "" {
			directory = p.cwd
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(p.cwd, directory)
		}
		for _, extension := range extensions {
			candidate := filepath.Join(directory, command+extension)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				executable = candidate
				break
			}
		}
		if executable != "" {
			break
		}
	}
	if executable == "" {
		return fmt.Errorf("teammate pane executable %s unavailable", command)
	}
	process := settings.NativeProcessCommand(ctx, executable, args, p.environment)
	if process == nil {
		return fmt.Errorf("teammate pane process unavailable")
	}
	process.Dir = p.cwd
	keys := make([]string, 0, len(p.environment))
	for key := range p.environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		process.Env = append(process.Env, key+"="+p.environment[key])
	}
	var stderr bytes.Buffer
	process.Stderr = &stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("teammate pane %s: %w: %s", command, err, stderr.String())
	}
	return nil
}
