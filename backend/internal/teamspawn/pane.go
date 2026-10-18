package teamspawn

import (
	"bytes"
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// PaneRuntime retains Source detection's launch TMUX value and the original
// process PATH. Mailbox body metadata never selects a process or a socket.
type PaneRuntime struct {
	cwd               string
	pid               int
	environment       map[string]string
	insideTmux        bool
	mu                sync.Mutex
	firstExternal     bool
	leaderWindow      string
	itermSessions     []string
	backend           string
	inProcessFallback bool
	mode              string
	needsSetup        bool
}

func NewPaneRuntime(cwd string, pid int, environment map[string]string) (*PaneRuntime, error) {
	if !filepath.IsAbs(cwd) || pid < 1 {
		return nil, fmt.Errorf("pane runtime requires original CWD and process PID")
	}
	env := map[string]string{}
	for key, value := range environment {
		env[key] = value
	}
	return &PaneRuntime{cwd: cwd, pid: pid, environment: env, insideTmux: env["TMUX"] != ""}, nil
}

func (p *PaneRuntime) KillPane(backend, pane string) bool {
	command, args := "", []string{}
	switch backend {
	case "tmux":
		command = "tmux"
		if !p.insideTmux {
			args = append(args, "-L", fmt.Sprintf("claude-swarm-%d", p.pid))
		}
		args = append(args, "kill-pane", "-t", pane)
	case "iterm2":
		command = "it2"
		args = []string{"session", "close", "-f", "-s", pane}
	default:
		return false
	}
	stdout, stderr, code := p.run(command, args)
	_ = stdout
	_ = stderr
	if code == 0 && backend == "iterm2" {
		p.mu.Lock()
		for i, id := range p.itermSessions {
			if id == pane {
				p.itermSessions = append(p.itermSessions[:i], p.itermSessions[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
	}
	return code == 0
}

func (p *PaneRuntime) run(command string, args []string) (string, string, int) {
	return p.runAt(p.cwd, command, args)
}

func (p *PaneRuntime) runAt(cwd, command string, args []string) (string, string, int) {
	executable := ""
	if filepath.IsAbs(command) {
		executable = command
	}
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		extensions = []string{".exe", ".com", ".bat", ".cmd"}
	}
	for _, directory := range filepath.SplitList(p.environment["PATH"]) {
		if executable != "" {
			break
		}
		if directory == "" {
			directory = p.cwd
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(p.cwd, directory)
		}
		for _, extension := range extensions {
			candidate := filepath.Join(directory, command+extension)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				executable = candidate
				break
			}
		}
	}
	if executable == "" {
		return "", "", 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	process := settings.NativeProcessCommand(ctx, executable, args, p.environment)
	if process == nil {
		return "", "", 1
	}
	process.Dir = cwd
	keys := []string{}
	for key := range p.environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		process.Env = append(process.Env, key+"="+p.environment[key])
	}
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	code := 0
	if err != nil {
		code = 1
		if process.ProcessState != nil {
			code = process.ProcessState.ExitCode()
		}
	}
	return strings.TrimSpace(stdout.String()), stderr.String(), code
}
