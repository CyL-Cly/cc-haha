package worktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func New(o Options) (*Service, error) {
	if o.Files == nil || o.Run == nil || o.Now == nil || o.Log == nil || o.Environment == nil || o.GitExecutable == "" {
		return nil, errors.New("worktree requires actual files/process/environment/clock/log producers")
	}
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	o.Environment = env
	return &Service{o}, nil
}
func (s *Service) run(ctx context.Context, cwd string, args []string, noPrompt bool) (ProcessResult, error) {
	env := map[string]string{}
	for k, v := range s.options.Environment {
		env[k] = v
	}
	if noPrompt {
		env["GIT_TERMINAL_PROMPT"] = "0"
		env["GIT_ASKPASS"] = ""
	}
	return s.options.Run(ctx, ProcessRequest{Command: s.options.GitExecutable, Arguments: append([]string{}, args...), CWD: cwd, Environment: env, IgnoreStdin: noPrompt})
}
func (s *Service) EnsureExcluded(root string) error {
	dir := s.GitDir(root)
	if dir == "" {
		return nil
	}
	common := s.CommonDir(dir)
	path := filepath.Join(common, "info/exclude")
	existing := s.read(path)
	pattern := ".claude/worktrees/"
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		line = trim(line)
		if line == pattern || line == "/"+pattern {
			return nil
		}
	}
	if e := s.options.Files.MkdirAll(filepath.Dir(path), 0777); e != nil {
		return e
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return s.options.Files.WriteFile(path, []byte(existing+"# Claude worktree sessions\n"+pattern+"\n"), 0666)
}
func (s *Service) Create(ctx context.Context, r Request) (Session, error) {
	return s.create(ctx, r, false)
}
func (s *Service) create(ctx context.Context, r Request, agent bool) (Session, error) {
	if e := ValidateSlug(r.Slug); e != nil {
		return Session{}, e
	}
	if !filepath.IsAbs(r.CWD) || (!agent && r.SessionID == "") {
		return Session{}, &CapabilityError{"actual session ID and cwd"}
	}
	hasHook, e := s.hasCreateHook(ctx)
	if e != nil {
		return Session{}, e
	}
	if hasHook {
		path, e := s.options.CreateHook(ctx, r.Slug)
		if e != nil {
			return Session{}, e
		}
		return Session{OriginalCWD: r.CWD, Path: path, Name: r.Slug, SessionID: r.SessionID, TmuxSessionName: r.TmuxSessionName, HookBased: true}, nil
	}
	root := s.GitRoot(r.CWD)
	if root == "" {
		return Session{}, errors.New("Cannot create a worktree: not in a git repository and no WorktreeCreate hooks are configured. Configure WorktreeCreate/WorktreeRemove hooks in settings.json to use worktree isolation with other VCS systems.")
	}
	originalBranch := r.OriginalBranch
	if originalBranch == "" {
		dir := s.GitDir(root)
		head := trim(s.read(filepath.Join(dir, "HEAD")))
		if strings.HasPrefix(head, "ref: refs/heads/") {
			originalBranch = strings.TrimPrefix(head, "ref: refs/heads/")
		}
	}
	started := s.options.Now()
	created, e := s.getOrCreateAtRoot(ctx, root, r, agent)
	if e != nil {
		return Session{}, e
	}
	if !created.Existed {
		if e = s.PostSetup(ctx, root, created.Path, r.Settings); e != nil {
			return Session{}, e
		}
	}
	sparse := len(r.Settings.SparsePaths) > 0
	session := Session{OriginalCWD: r.CWD, Path: created.Path, Name: r.Slug, Branch: created.Branch, OriginalBranch: originalBranch, OriginalHeadCommit: created.HeadCommit, SessionID: r.SessionID, TmuxSessionName: r.TmuxSessionName, UsedSparsePaths: &sparse}
	if !created.Existed {
		duration := s.options.Now().UnixMilli() - started.UnixMilli()
		session.CreationDurationMs = &duration
	}
	return session, nil
}

// GitWorktree is the original getOrCreateWorktree result, before any session
// identity, CWD, metadata or post-creation consumer is applied.
type GitWorktree struct {
	Path, Branch, HeadCommit, BaseBranch string
	Existed                              bool
}

func (s *Service) GetOrCreateGitWorktree(ctx context.Context, root string, r Request) (GitWorktree, error) {
	if e := ValidateSlug(r.Slug); e != nil {
		return GitWorktree{}, e
	}
	if !filepath.IsAbs(root) {
		return GitWorktree{}, &CapabilityError{"actual canonical git root"}
	}
	return s.getOrCreateAtRoot(ctx, root, r, false)
}
func (s *Service) getOrCreateAtRoot(ctx context.Context, root string, r Request, agent bool) (GitWorktree, error) {
	path := PathFor(root, r.Slug)
	branch := BranchName(r.Slug)
	if e := s.SafeLocation(root, path); e != nil {
		return GitWorktree{}, e
	}
	head := s.WorktreeHead(path)
	existed := head != ""
	base := ""
	if !existed {
		if e := s.EnsureExcluded(root); e != nil {
			return GitWorktree{}, e
		}
		if e := s.options.Files.MkdirAll(filepath.Dir(path), 0777); e != nil {
			return GitWorktree{}, e
		}
		if e := s.SafeLocation(root, path); e != nil {
			return GitWorktree{}, e
		}
		switch {
		case r.PRNumber != nil && *r.PRNumber != 0:
			result, e := s.run(ctx, root, []string{"fetch", "origin", "pull/" + PRNumberString(*r.PRNumber) + "/head"}, true)
			if e != nil {
				return GitWorktree{}, e
			}
			if result.Code != 0 {
				reason := trim(result.Stderr)
				if reason == "" {
					reason = "PR may not exist or the repository may not have a remote named \"origin\""
				}
				return GitWorktree{}, fmt.Errorf("Failed to fetch PR #%s: %s", PRNumberString(*r.PRNumber), reason)
			}
			base = "FETCH_HEAD"
		case r.BaseRef != "":
			base = r.BaseRef
		default:
			def := r.DefaultBranch
			if def == "" {
				def = s.DefaultBranch(root)
			}
			base = "origin/" + def
			dir := s.GitDir(root)
			if dir != "" {
				head = s.ResolveRef(dir, "refs/remotes/origin/"+def)
			}
			if head == "" {
				result, e := s.run(ctx, root, []string{"fetch", "origin", def}, true)
				if e != nil {
					return GitWorktree{}, e
				}
				if result.Code != 0 {
					base = "HEAD"
				}
			}
		}
		if head == "" {
			result, e := s.run(ctx, root, []string{"rev-parse", base}, false)
			if e != nil {
				return GitWorktree{}, e
			}
			if result.Code != 0 {
				return GitWorktree{}, fmt.Errorf("Failed to resolve base branch %q: git rev-parse failed", base)
			}
			head = trim(result.Stdout)
		}
		args := []string{"worktree", "add"}
		if len(r.Settings.SparsePaths) > 0 {
			args = append(args, "--no-checkout")
		}
		args = append(args, "-B", branch, path, head)
		if e := s.SafeLocation(root, path); e != nil {
			return GitWorktree{}, e
		}
		result, e := s.run(ctx, root, args, false)
		if e != nil {
			return GitWorktree{}, e
		}
		if result.Code != 0 {
			return GitWorktree{}, errors.New("Failed to create worktree: " + result.Stderr)
		}
		if len(r.Settings.SparsePaths) > 0 {
			result, e = s.run(ctx, path, append([]string{"sparse-checkout", "set", "--cone", "--"}, r.Settings.SparsePaths...), false)
			if e != nil {
				return GitWorktree{}, e
			}
			if result.Code != 0 {
				_, _ = s.run(ctx, root, []string{"worktree", "remove", "--force", path}, false)
				return GitWorktree{}, errors.New("Failed to configure sparse-checkout: " + result.Stderr)
			}
			result, e = s.run(ctx, path, []string{"checkout", "HEAD"}, false)
			if e != nil {
				return GitWorktree{}, e
			}
			if result.Code != 0 {
				_, _ = s.run(ctx, root, []string{"worktree", "remove", "--force", path}, false)
				return GitWorktree{}, errors.New("Failed to checkout sparse worktree: " + result.Stderr)
			}
		}
	} else if agent {
		if s.options.TouchAgentWorktree == nil {
			return GitWorktree{}, &CapabilityError{"Agent resumed worktree timestamp producer"}
		}
		if e := s.options.TouchAgentWorktree(path, s.options.Now()); e != nil {
			return GitWorktree{}, e
		}
	}
	return GitWorktree{Path: path, Branch: branch, HeadCommit: head, BaseBranch: base, Existed: existed}, nil
}

func (s *Service) hasCreateHook(ctx context.Context) (bool, error) {
	if s.options.CreateHook == nil {
		return false, nil
	}
	if s.options.HasCreateHook != nil {
		return s.options.HasCreateHook(ctx)
	}
	return true, nil
}
