package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// TerminalDiff captures this REPL's structured tool results on its Owner. The
// two independent Source useDiffData promises run native git workers; the
// caller's context cancels and joins them when the dialog is unmounted.
func (m *MainPrint) TerminalDiff(ctx context.Context) (terminaldiff.Dialog, error) {
	result := terminaldiff.Dialog{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.shuttingDown || m.Host == nil {
			return cooperative.ErrClosed
		}
		if !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild {
			return cooperative.ErrRestricted
		}
		messages, e := querypreflight.InspectionRecords(m.Host.History())
		if e != nil {
			return e
		}
		turns, e := terminaldiff.Turns(messages)
		if e != nil {
			return e
		}
		current := terminaldiff.Data{Files: []terminaldiff.File{}}
		root, e := m.Git.FindRoot(scope, m.workingDirectory())
		if e != nil {
			return e
		}
		if root != "" {
			dirFuture, e := m.Git.Resolve(scope, m.workingDirectory())
			if e != nil {
				return e
			}
			raw, e := dirFuture.Await(scope)
			if e != nil {
				return e
			}
			var gitDir string
			_ = json.Unmarshal(raw, &gitDir)
			cwd, env := m.workingDirectory(), m.nativeOpenAIEnvironment()
			run := func(args ...string) (string, bool) {
				active, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				v, e := worktree.NativeRun(active, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(env), Arguments: append([]string{"--no-optional-locks"}, args...), CWD: cwd, Environment: env, IgnoreStdin: true})
				return terminaldiff.CommandOutput(v.Stdout), e == nil && v.Code == 0
			}
			transient := func() bool {
				for _, name := range []string{"MERGE_HEAD", "REBASE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
					if _, e := os.Stat(filepath.Join(gitDir, name)); e == nil {
						return true
					}
				}
				return false
			}
			type statsOutput struct {
				short, num, untracked string
				valid                 bool
			}
			stats, e := nativeSkillWork(m, scope, func() (statsOutput, error) {
				if transient() {
					return statsOutput{}, nil
				}
				short, _ := run("diff", "HEAD", "--shortstat")
				if v := terminaldiff.Shortstat(short); v != nil && v.FilesCount > 500 {
					return statsOutput{short: short, valid: true}, nil
				}
				num, ok := run("diff", "HEAD", "--numstat")
				if !ok {
					return statsOutput{}, nil
				}
				untracked, _ := run("ls-files", "--others", "--exclude-standard")
				return statsOutput{short, num, untracked, true}, nil
			})
			if e != nil {
				return e
			}
			hunks, e := nativeSkillWork(m, scope, func() (string, error) {
				if transient() {
					return "", nil
				}
				v, _ := run("diff", "HEAD")
				return v, nil
			})
			if e != nil {
				return e
			}
			stat, e := stats.Await(scope)
			if e != nil {
				return e
			}
			diff, e := hunks.Await(scope)
			if e != nil {
				return e
			}
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if stat.valid {
				current, e = terminaldiff.Current(stat.short, stat.num, stat.untracked, diff)
				if e != nil {
					return e
				}
			}
		}
		result.Sources = append([]terminaldiff.Source{{Data: current}}, turns...)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
