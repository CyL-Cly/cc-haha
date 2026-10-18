package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type reviewPathResult struct {
	Path   string `json:"path"`
	OK     bool   `json:"ok"`
	Action string `json:"action,omitempty"`
	Error  string `json:"error,omitempty"`
}
type reviewWriteResult struct {
	State         string             `json:"state"`
	Snapshot      string             `json:"snapshot"`
	Results       []reviewPathResult `json:"results"`
	Status        *reviewStatus      `json:"status,omitempty"`
	BackupDir     string             `json:"backupDir,omitempty"`
	RevertedPaths []string           `json:"revertedPaths,omitempty"`
	DeletedPaths  []string           `json:"deletedPaths,omitempty"`
	Error         string             `json:"error,omitempty"`
}
type reviewWriteRequest struct {
	paths           []string
	patch, snapshot string
	source          *reviewSource
}

func reviewWriteFailure(state, message string) reviewWriteResult {
	return reviewWriteResult{State: state, Results: []reviewPathResult{}, Error: message}
}
func reviewWriteRefusal(resource string, source *reviewSource) string {
	if source == nil {
		return ""
	}
	switch source.Kind {
	case "turn":
		return reviewTurnRejection
	case "branch", "commit":
		return fmt.Sprintf("Review source %q is a read-only comparison; stage, unstage and revert only apply to the index and the working tree", source.Kind)
	}
	if resource == "stage" && source.Kind == "staged" {
		return "Staged comparison only supports unstaging"
	}
	if resource == "unstage" && source.Kind == "unstaged" {
		return "Unstaged comparison only supports staging or discarding"
	}
	if resource == "revert" && source.Kind == "staged" {
		return "Staged comparison does not display working-tree changes"
	}
	if strings.HasSuffix(resource, "-hunk") {
		expected := "unstaged"
		if resource == "unstage-hunk" {
			expected = "staged"
		}
		if source.Kind != expected {
			return "Hunk action does not match the displayed comparison"
		}
	}
	return ""
}
func (ctx *reviewContext) write(resource string, input reviewWriteRequest) (reviewWriteResult, error) {
	if refusal := reviewWriteRefusal(resource, input.source); refusal != "" {
		return reviewWriteFailure("error", refusal), nil
	}
	if strings.HasSuffix(resource, "-hunk") {
		return ctx.writeHunk(input, resource == "unstage-hunk")
	}
	// Validate every path before checking freshness or applying the first path.
	targets := make([]reviewTarget, 0, len(input.paths))
	for _, path := range input.paths {
		target, err := ctx.target(path, false)
		if err != nil {
			return reviewWriteResult{}, err
		}
		targets = append(targets, target)
	}
	head := ctx.head()
	entries, failure := ctx.entries()
	if failure != "" {
		return reviewWriteFailure("error", failure), nil
	}
	snapshot := ctx.snapshot(head, entries)
	if snapshot != input.snapshot {
		return reviewWriteResult{State: "stale", Snapshot: snapshot, Results: []reviewPathResult{}, Error: "Review snapshot changed since it was read; nothing was applied"}, nil
	}
	results := make([]reviewPathResult, 0, len(targets))
	backupDir := ""
	if resource == "revert" {
		id, err := uuid()
		if err != nil {
			return reviewWriteResult{}, err
		}
		session := reviewBackupSegment.ReplaceAllString(ctx.info.ID, "_")
		if len(session) > 120 {
			session = session[:120]
		}
		if session == "" {
			session = "unknown"
		}
		stamp := strings.NewReplacer(":", "-", ".", "-").Replace(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
		backupDir = filepath.Join(ctx.server.options.ConfigDir, "cc-haha", "review-backups", session, stamp+"-"+id[:6])
	}
	backupCreated := false
	for _, target := range targets {
		var entry *reviewEntry
		for i := range entries {
			if entries[i].path == target.relative {
				entry = &entries[i]
				break
			}
		}
		result := reviewPathResult{Path: target.relative}
		args := []string{}
		prefix := ""
		switch resource {
		case "stage":
			args = []string{"add", "--", reviewLiteral(target.repo)}
			prefix = "Failed to stage path"
			result.Action = "staged"
		case "unstage":
			paths := []string{reviewLiteral(target.repo)}
			if entry != nil && entry.repoOldPath != "" {
				old, inside := ctx.rebase(entry.repoOldPath)
				if !inside {
					result.Error = "Rename crosses workspace boundary"
					results = append(results, result)
					continue
				}
				oldTarget, err := ctx.target(old, false)
				if err != nil {
					return reviewWriteResult{}, err
				}
				paths = append(paths, reviewLiteral(oldTarget.repo))
			}
			if head != "" {
				args = append([]string{"restore", "--staged", "--"}, paths...)
			} else {
				args = append([]string{"reset", "-q", "--"}, paths...)
			}
			out := ctx.git(ctx.repo, args...)
			if out.code != 0 && head != "" {
				args = append([]string{"reset", "-q", "HEAD", "--"}, paths...)
				out = ctx.git(ctx.repo, args...)
			}
			result.OK, result.Action = out.code == 0, "unstaged"
			if !result.OK {
				result.Error = workspaceGitError("Failed to unstage path", ctx.repo, out, args...)
			}
			results = append(results, result)
			continue
		case "revert":
			stat, err := os.Stat(target.absolute)
			if err != nil && !workspaceMissing(err) {
				result.Error = workspaceFSError("Failed to stat review path", target.absolute, err)
				results = append(results, result)
				continue
			}
			if err == nil && stat.IsDir() {
				result.Error = "Revert requires a file path, not a directory: " + target.relative
				results = append(results, result)
				continue
			}
			if err == nil {
				if failure := reviewBackup(backupDir, target); failure != "" {
					result.Error = failure
					results = append(results, result)
					continue
				}
				backupCreated = true
			}
			if entry != nil && entry.untracked {
				if workspaceMissing(err) {
					result.OK, result.Action = true, "noop"
				} else {
					err = os.Remove(target.absolute)
					result.OK, result.Action = err == nil || os.IsNotExist(err), "deleted"
					if !result.OK {
						result.Error = workspaceFSError("Failed to remove untracked file", target.absolute, err)
					}
				}
				results = append(results, result)
				continue
			}
			args = []string{"restore", "--worktree", "--", reviewLiteral(target.repo)}
			out := ctx.git(ctx.repo, args...)
			if out.code != 0 {
				args = []string{"checkout", "--", reviewLiteral(target.repo)}
				out = ctx.git(ctx.repo, args...)
			}
			result.OK, result.Action = out.code == 0, "reverted"
			if !result.OK {
				result.Error = workspaceGitError("Failed to revert path", ctx.repo, out, args...)
			}
			results = append(results, result)
			continue
		}
		out := ctx.git(ctx.repo, args...)
		result.OK = out.code == 0
		if !result.OK {
			result.Error = workspaceGitError(prefix, ctx.repo, out, args...)
		}
		results = append(results, result)
	}
	if !backupCreated {
		backupDir = ""
	}
	return ctx.finishWrite(input.source, results, backupDir), nil
}
func reviewBackup(directory string, target reviewTarget) string {
	destination := filepath.Join(directory, filepath.FromSlash(target.relative))
	copy := func() error {
		// Node's copyFile rejects special files. Do not turn that refusal into
		// a blocking FIFO read while porting the copy through Go's io boundary.
		stat, err := os.Stat(target.absolute)
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() {
			return fmt.Errorf("review backup requires a regular file")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		_ = os.Chmod(directory, 0700)
		in, err := os.Open(target.absolute)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_ = os.Chmod(destination, 0600)
		return nil
	}
	if err := copy(); err != nil {
		return workspaceFSError("Failed to back up file before revert", target.absolute, err)
	}
	return ""
}

var reviewBackupSegment = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (ctx *reviewContext) writeHunk(input reviewWriteRequest, reverse bool) (reviewWriteResult, error) {
	paths := reviewPatchPaths(input.patch)
	if len(paths) != 1 {
		return reviewWriteFailure("error", fmt.Sprintf("Hunk operations accept a patch for exactly one file (found %d)", len(paths))), nil
	}
	workspace, inside := ctx.rebase(paths[0])
	if !inside {
		return reviewWriteResult{}, reviewForbidden("Path is outside workspace: " + paths[0])
	}
	target, err := ctx.target(workspace, false)
	if err != nil {
		return reviewWriteResult{}, err
	}
	head := ctx.head()
	entries, failure := ctx.entries()
	if failure != "" {
		return reviewWriteFailure("error", failure), nil
	}
	for _, entry := range entries {
		if entry.path == target.relative && entry.conflicted {
			return reviewWriteFailure("error", "Unmerged path only supports whole-file operations: "+target.relative), nil
		}
	}
	if regexp.MustCompile(`(?m)^GIT binary patch$`).MatchString(input.patch) {
		return reviewWriteFailure("error", "Binary file only supports whole-file operations: "+target.relative), nil
	}
	snapshot := ctx.snapshot(head, entries)
	if snapshot != input.snapshot {
		return reviewWriteResult{State: "stale", Snapshot: snapshot, Results: []reviewPathResult{}, Error: "Review snapshot changed since it was read; nothing was applied"}, nil
	}
	directory, err := os.MkdirTemp("", "cc-haha-review-patch-")
	if err != nil {
		return reviewWriteResult{}, err
	}
	defer os.RemoveAll(directory)
	patch := input.patch
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	file := filepath.Join(directory, "hunk.patch")
	if err := os.WriteFile(file, []byte(patch), 0600); err != nil {
		return reviewWriteResult{}, err
	}
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	prefix := "Failed to stage hunk"
	if reverse {
		args = append(args, "--reverse")
		prefix = "Failed to unstage hunk"
	}
	args = append(args, "--", file)
	out := ctx.git(ctx.repo, args...)
	result := reviewPathResult{Path: target.relative, OK: out.code == 0}
	if !result.OK {
		result.Error = workspaceGitError(prefix, ctx.repo, out, args...)
	}
	return ctx.finishWrite(input.source, []reviewPathResult{result}, ""), nil
}
func (ctx *reviewContext) finishWrite(source *reviewSource, results []reviewPathResult, backup string) reviewWriteResult {
	result := reviewWriteResult{State: "ok", Results: results, BackupDir: backup}
	success := 0
	for _, path := range results {
		if path.OK {
			success++
			if path.Action == "reverted" {
				result.RevertedPaths = append(result.RevertedPaths, path.Path)
			}
			if path.Action == "deleted" {
				result.DeletedPaths = append(result.DeletedPaths, path.Path)
			}
		} else if result.Error == "" {
			result.Error = path.Error
		}
	}
	if success == 0 && len(results) > 0 {
		result.State = "error"
	} else if success < len(results) {
		result.State = "partial"
	}
	if source != nil {
		status := ctx.status(*source, false)
		result.Status = &status
		result.Snapshot = status.Snapshot
	}
	if result.Snapshot == "" {
		head := ctx.head()
		entries, failure := ctx.entries()
		if failure == "" {
			result.Snapshot = ctx.snapshot(head, entries)
		}
	}
	return result
}
