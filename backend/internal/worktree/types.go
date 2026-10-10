// Package worktree supplies the original native git worktree operations. The
// caller owns active session identity, CWD, permissions and transcript changes.
package worktree

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "worktree capability is not migrated: " + e.Boundary }

type Session struct {
	OriginalCWD        string `json:"originalCwd"`
	Path               string `json:"worktreePath"`
	Name               string `json:"worktreeName"`
	Branch             string `json:"worktreeBranch,omitempty"`
	OriginalBranch     string `json:"originalBranch,omitempty"`
	OriginalHeadCommit string `json:"originalHeadCommit,omitempty"`
	SessionID          string `json:"sessionId"`
	TmuxSessionName    string `json:"tmuxSessionName,omitempty"`
	HookBased          bool   `json:"hookBased,omitempty"`
	CreationDurationMs *int64 `json:"creationDurationMs,omitempty"`
	UsedSparsePaths    *bool  `json:"usedSparsePaths,omitempty"`
}
type Settings struct {
	SparsePaths, SymlinkDirectories []string
	LocalSettingsPath               string
}
type Request struct {
	CWD, SessionID, Slug, TmuxSessionName, BaseRef string
	PRNumber                                       *float64
	Settings                                       Settings
	OriginalBranch, DefaultBranch                  string
}
type ProcessResult struct {
	Code           int
	Stdout, Stderr string
}
type ProcessRequest struct {
	Command     string
	Arguments   []string
	CWD         string
	Environment map[string]string
	IgnoreStdin bool
}
type Files interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	Realpath(string) (string, error)
	CopyFile(string, string) error
	Symlink(string, string) error
}
type OSFiles struct{}

func (OSFiles) ReadFile(p string) ([]byte, error)                 { return os.ReadFile(p) }
func (OSFiles) WriteFile(p string, b []byte, m fs.FileMode) error { return os.WriteFile(p, b, m) }
func (OSFiles) MkdirAll(p string, m fs.FileMode) error            { return os.MkdirAll(p, m) }
func (OSFiles) Stat(p string) (fs.FileInfo, error)                { return os.Stat(p) }
func (OSFiles) Lstat(p string) (fs.FileInfo, error)               { return os.Lstat(p) }
func (OSFiles) Realpath(p string) (string, error)                 { return filepath.EvalSymlinks(p) }
func (OSFiles) CopyFile(src, dst string) error {
	b, e := os.ReadFile(src)
	if e != nil {
		return e
	}
	st, e := os.Stat(src)
	if e != nil {
		return e
	}
	if e = os.WriteFile(dst, b, st.Mode().Perm()); e != nil {
		return e
	}
	return os.Chmod(dst, st.Mode().Perm())
}
func (OSFiles) Symlink(src, dst string) error { return os.Symlink(src, dst) }

type Options struct {
	Files         Files
	Environment   map[string]string
	GitExecutable string
	Run           func(context.Context, ProcessRequest) (ProcessResult, error)
	Now           func() time.Time
	Log           func(level, message string)
	// Hooks are actual user/plugin/SDK producers. Git must not silently replace
	// configured VCS hooks when their executor is unavailable.
	HasCreateHook         func(context.Context) (bool, error)
	CreateHook            func(context.Context, string) (string, error)
	RemoveHook            func(context.Context, string) (bool, error)
	IncludeMatcher        func(string, string) (bool, error)
	CommitAttributionHook func(context.Context, string, string) error
	// Agent fast-resume refreshes the directory timestamp without recreating it.
	TouchAgentWorktree func(string, time.Time) error
}
type Service struct{ options Options }
