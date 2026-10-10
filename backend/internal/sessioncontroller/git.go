package sessioncontroller

import (
	"io/fs"
	"path/filepath"
)

// NoRepositoryBranch implements the actual findGitRoot/computeBranch no-repo
// branch without spawning Git. Active repositories require the unported Git
// filesystem service and are refused rather than rendered as HEAD.
func NoRepositoryBranch(cwd string, stat func(string) (fs.FileInfo, error)) (string, error) {
	if stat == nil {
		return "", capability("missing Git filesystem observation")
	}
	directory, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		info, failure := stat(filepath.Join(directory, ".git"))
		if failure == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return "", capability("active Git repository/worktree")
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return "HEAD", nil
}
