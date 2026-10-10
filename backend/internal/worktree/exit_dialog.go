package worktree

import (
	"context"
	"fmt"
	"strings"
)

type ExitStatus struct {
	ChangedFiles []string
	Commits      int
}

// InspectExitDialog preserves the original dialog's stdout-only inspection.
// Its behavior differs from ExitWorktree.validateInput, which refuses unknown
// git state. The dialog always offers its default keep option when dirty.
func (s *Service) InspectExitDialog(ctx context.Context, cwd string, session Session) (ExitStatus, error) {
	status, e := s.run(ctx, cwd, []string{"status", "--porcelain"}, false)
	if e != nil {
		return ExitStatus{}, e
	}
	result := ExitStatus{ChangedFiles: []string{}}
	for _, line := range strings.Split(status.Stdout, "\n") {
		if trim(line) != "" {
			result.ChangedFiles = append(result.ChangedFiles, line)
		}
	}
	head := session.OriginalHeadCommit
	if head == "" {
		head = "undefined"
	}
	commits, e := s.run(ctx, cwd, []string{"rev-list", "--count", head + "..HEAD"}, false)
	if e != nil {
		return ExitStatus{}, e
	}
	_, _ = fmt.Sscan(trim(commits.Stdout), &result.Commits)
	return result, nil
}
