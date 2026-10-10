package worktree

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const AgentUnavailableReason = "the workspace is not a git repository and no WorktreeCreate hook is configured"

type AgentWorktree struct {
	Path       string `json:"worktreePath"`
	Branch     string `json:"worktreeBranch,omitempty"`
	HeadCommit string `json:"headCommit,omitempty"`
	GitRoot    string `json:"gitRoot,omitempty"`
	HookBased  bool   `json:"hookBased,omitempty"`
}
type AgentAttempt struct {
	Worktree          *AgentWorktree `json:"worktree,omitempty"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

// CanonicalRoot validates the same common-dir/backlink pair as the original
// discovery helper, so a child never nests its worktree in a session worktree.
func (s *Service) CanonicalRoot(cwd string) string {
	root := s.GitRoot(cwd)
	if root == "" {
		return ""
	}
	pointer := trim(s.read(filepath.Join(root, ".git")))
	if !strings.HasPrefix(pointer, "gitdir:") {
		return root
	}
	dir := trim(strings.TrimPrefix(pointer, "gitdir:"))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	raw := trim(s.read(filepath.Join(dir, "commondir")))
	if raw == "" {
		return root
	}
	common := raw
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	common = filepath.Clean(common)
	if filepath.Dir(dir) != filepath.Join(common, "worktrees") {
		return root
	}
	backlink, e := s.options.Files.Realpath(trim(s.read(filepath.Join(dir, "gitdir"))))
	if e != nil {
		return root
	}
	physical, e := s.options.Files.Realpath(root)
	if e != nil || backlink != filepath.Join(physical, ".git") {
		return root
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

func (s *Service) CreateAgent(ctx context.Context, cwd, slug string, settings Settings) (AgentAttempt, error) {
	root := s.CanonicalRoot(cwd)
	hasHook, e := s.hasCreateHook(ctx)
	if e != nil {
		return AgentAttempt{}, e
	}
	if !hasHook && root == "" {
		return AgentAttempt{UnavailableReason: AgentUnavailableReason}, nil
	}
	if hasHook {
		root = cwd
	}
	session, e := s.create(ctx, Request{CWD: root, Slug: slug, Settings: settings}, true)
	if e != nil {
		return AgentAttempt{}, e
	}
	info := &AgentWorktree{Path: session.Path, HookBased: session.HookBased}
	if !session.HookBased {
		info.Branch, info.HeadCommit, info.GitRoot = session.Branch, session.OriginalHeadCommit, root
	}
	return AgentAttempt{Worktree: info}, nil
}

// HasAgentChanges is fail closed on git failures. The result controls cleanup;
// treating an unknown status as clean would destroy the child's output.
func (s *Service) HasAgentChanges(ctx context.Context, info AgentWorktree) (bool, error) {
	status, e := s.run(ctx, info.Path, []string{"status", "--porcelain"}, false)
	if e != nil {
		return true, e
	}
	if status.Code != 0 || trim(status.Stdout) != "" {
		return true, nil
	}
	commits, e := s.run(ctx, info.Path, []string{"rev-list", "--count", info.HeadCommit + "..HEAD"}, false)
	if e != nil {
		return true, e
	}
	if commits.Code != 0 {
		return true, nil
	}
	return decimalPrefixPositive(trim(commits.Stdout)), nil
}

func decimalPrefixPositive(value string) bool {
	end := 0
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return false
	}
	n, e := strconv.ParseFloat(value[:end], 64)
	return (e == nil || n > 0) && n > 0
}

func (s *Service) RemoveAgent(ctx context.Context, info AgentWorktree) (bool, error) {
	if info.HookBased {
		if s.options.RemoveHook == nil {
			s.options.Log("warn", "No WorktreeRemove hook configured, hook-based agent worktree left at: "+info.Path)
			return false, nil
		}
		return s.options.RemoveHook(ctx, info.Path)
	}
	if info.GitRoot == "" {
		s.options.Log("error", "Cannot remove agent worktree: no git root provided")
		return false, nil
	}
	if e := s.SafeLocation(info.GitRoot, info.Path); e != nil {
		s.options.Log("error", e.Error())
		return false, nil
	}
	removed, e := s.run(ctx, info.GitRoot, []string{"worktree", "remove", "--force", info.Path}, false)
	if e != nil {
		return false, e
	}
	if removed.Code != 0 {
		s.options.Log("error", "Failed to remove agent worktree: "+removed.Stderr)
		return false, nil
	}
	if info.Branch != "" {
		deleted, e := s.run(ctx, info.GitRoot, []string{"branch", "-D", info.Branch}, false)
		if e != nil {
			return false, e
		}
		if deleted.Code != 0 {
			s.options.Log("error", fmt.Sprintf("Could not delete agent worktree branch: %s", deleted.Stderr))
		}
	}
	return true, nil
}
