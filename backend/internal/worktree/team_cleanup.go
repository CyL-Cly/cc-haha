package worktree

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
)

var teamGitFile = regexp.MustCompile(`^gitdir:\s*(.+)$`)

// DestroyTeamWorktree follows the older Team cleanup path, which tolerates a
// missing worktree and falls back after git failure. Ordinary ExitWorktree and
// Agent cleanup retain their stricter original validation and result paths.
func (s *Service) DestroyTeamWorktree(ctx context.Context, path string, removeDirectory func(string) error) error {
	if removeDirectory == nil {
		return &CapabilityError{Boundary: "actual Team directory removal primitive"}
	}
	content, err := s.options.Files.ReadFile(filepath.Join(path, ".git"))
	if err == nil {
		match := teamGitFile.FindStringSubmatch(trim(string(content)))
		if len(match) == 2 {
			root := filepath.Join(match[1], "..", "..", "..")
			result, err := s.options.Run(ctx, ProcessRequest{Command: s.options.GitExecutable, Arguments: []string{"worktree", "remove", "--force", path}, CWD: root, Environment: s.options.Environment})
			if err == nil && (result.Code == 0 || strings.Contains(result.Stderr, "not a working tree")) {
				return nil
			}
		}
	}
	if err = removeDirectory(path); err != nil && s.options.Log != nil {
		s.options.Log("debug", "[TeammateTool] Failed to remove worktree "+path+": "+err.Error())
	}
	return nil
}
