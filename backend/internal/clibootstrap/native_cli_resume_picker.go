package clibootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"golang.org/x/text/unicode/norm"
)

// CLIResumePicker holds only immutable stat observations and read-only lite
// metadata. A selected fullPath is consumed by the original activation loader.
type CLIResumePicker struct {
	mu        sync.Mutex
	n         *NativeBoundary
	platform  string
	filter    protocolRaw
	worktrees []string
	stats     []resumestate.PickerStat
	cursor    int
	logs      []protocol.JSONObject
}
type protocolRaw = []byte

func NewCLIResumePicker(ctx context.Context, n *NativeBoundary, platform string, filter []byte) (*CLIResumePicker, error) {
	if n == nil {
		return nil, errors.New("CLI resume picker requires native filesystem")
	}
	env := map[string]string{}
	for k, v := range n.Environment {
		env[k] = v
	}
	env["GIT_TERMINAL_PROMPT"] = "0"
	env["GIT_ASKPASS"] = ""
	// Source uses its normal native Git resolver and porcelain worktree producer.
	result, e := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(env), Arguments: []string{"worktree", "list", "--porcelain"}, CWD: n.CWD, Environment: env, IgnoreStdin: true})
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	paths := []string{}
	if e == nil && result.Code == 0 {
		for _, line := range strings.Split(result.Stdout, "\n") {
			if strings.HasPrefix(line, "worktree ") {
				paths = append(paths, norm.NFC.String(strings.TrimPrefix(line, "worktree ")))
			}
		}
	}
	return &CLIResumePicker{n: n, platform: platform, filter: append([]byte(nil), filter...), worktrees: paths}, nil
}
func (p *CLIResumePicker) ReadResumePicker(ctx context.Context, reset *bool, count int) ([]protocol.JSONObject, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return nil, false, context.Cause(ctx)
	}
	files := mainResumeFiles{n: p.n}
	if reset != nil {
		config := p.n.ConfigHomeDirectory()
		if config == "" {
			config = filepath.Join(p.n.Home, ".claude")
		}
		p.stats = resumestate.ReadPickerStats(files, func(path string) (resumestate.Stat, error) {
			info, e := p.n.stat(path)
			if e != nil {
				return resumestate.Stat{}, e
			}
			return resumestate.Stat{Size: info.Size(), Modified: nativeResumeDateMillis(info.ModTime()), Birth: nativeResumeBirthMillis(path, info)}, nil
		}, filepath.Join(config, "projects"), p.n.CWD, p.worktrees, p.platform == "windows", *reset)
		p.cursor = 0
		p.logs = []protocol.JSONObject{}
	}
	page, e := resumestate.EnrichPickerPage(ctx, files, p.stats, p.cursor, count, p.n.Environment)
	if e != nil {
		return nil, false, e
	}
	offset := len(p.logs)
	for i, log := range page.Logs {
		log.Set("value", rawMain(offset+i))
		p.logs = append(p.logs, log)
	}
	p.cursor = page.NextIndex
	return resumestate.FilterPR(p.logs, p.filter), p.cursor < len(p.stats), nil
}
func (p *CLIResumePicker) CurrentDirectory() string { return p.n.CWD }

func (p *CLIResumePicker) IsCrossProject(log protocol.JSONObject, all bool) bool {
	project := log.String("projectPath")
	if !all || project == "" || project == p.n.CWD {
		return false
	}
	if p.n.Environment["USER_TYPE"] == "ant" {
		for _, root := range p.worktrees {
			if project == root || strings.HasPrefix(project, root+string(filepath.Separator)) {
				return false
			}
		}
	}
	return true
}
func (p *CLIResumePicker) CrossProjectCommand(log protocol.JSONObject) string {
	// The distribution's actual launcher remains Go; the Source distribution
	// spells its own executable claude in the otherwise unchanged cd recipe.
	return "cd " + teamspawn.ShellQuote(log.String("projectPath")) + " && claude-haha --resume " + log.String("sessionId")
}
