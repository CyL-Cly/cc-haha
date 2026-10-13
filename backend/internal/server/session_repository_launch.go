package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type sessionRepositoryOptions struct {
	Branch   string
	Worktree bool
}
type sessionRepository struct {
	RequestedWorkDir string `json:"requestedWorkDir"`
	RepoRoot         string `json:"repoRoot"`
	Branch           string `json:"branch"`
	Worktree         bool   `json:"worktree"`
	BaseRef          string `json:"baseRef"`
	WorktreePath     string `json:"worktreePath,omitempty"`
	WorktreeBranch   string `json:"worktreeBranch,omitempty"`
	WorktreeSlug     string `json:"worktreeSlug,omitempty"`
}
type RepositorySessionLaunch struct {
	WorkDir               string
	Repository            json.RawMessage
	WorktreeName, BaseRef string
}

func repositoryRequestError(code, message string) error {
	return &apiError{http.StatusBadRequest, code, message}
}
func (server *Server) resolveRepositoryDirectory(value string) (string, error) {
	resolved := server.filesystemPath(value)
	real, e := filepath.EvalSymlinks(resolved)
	if e != nil {
		return "", repositoryRequestError("WORKDIR_MISSING", "Working directory does not exist: "+resolved)
	}
	stat, e := os.Stat(real)
	if e != nil {
		return "", e
	}
	if !stat.IsDir() {
		return "", repositoryRequestError("WORKDIR_NOT_DIRECTORY", "Working directory is not a directory: "+real)
	}
	return normalizeDriveRoot(real), nil
}

func usableRepositoryContext(ctx repositoryContext) error {
	if ctx.State == "ok" && ctx.RepoRoot != nil {
		return nil
	}
	switch ctx.State {
	case "not_git_repo":
		return repositoryRequestError("REPOSITORY_NOT_GIT", "Selected directory is not a Git repository")
	case "missing_workdir":
		message := ctx.Error
		if message == "" {
			message = "Working directory does not exist"
		}
		return repositoryRequestError("WORKDIR_MISSING", message)
	default:
		message := ctx.Error
		if message == "" {
			message = "Failed to inspect Git repository"
		}
		return repositoryRequestError("REPOSITORY_CONTEXT_ERROR", message)
	}
}
func resolveRepositoryBranch(ctx repositoryContext, requested string) *repositoryBranch {
	if ctx.State != "ok" {
		return nil
	}
	if requested == "" {
		names := []*string{ctx.CurrentBranch, ctx.DefaultBranch}
		if len(ctx.Branches) > 0 {
			names = append(names, &ctx.Branches[0].Name)
		}
		for _, name := range names {
			if name == nil || *name == "" {
				continue
			}
			for _, branch := range ctx.Branches {
				if branch.Name == *name {
					requested = *name
					break
				}
			}
			if requested != "" {
				break
			}
		}
	}
	for _, branch := range ctx.Branches {
		if branch.Name == requested {
			copy := branch
			return &copy
		}
	}
	return nil
}
func repositoryBaseRef(branch repositoryBranch) string {
	if branch.Local || branch.RemoteRef == "" {
		return branch.Name
	}
	return branch.RemoteRef
}

var repositorySlugUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func repositoryWorktreeSlug(branch, sid string) (string, error) {
	safe := strings.Trim(repositorySlugUnsafe.ReplaceAllString(branch, "-"), "-")
	if len(safe) > 38 {
		safe = safe[:38]
	}
	if safe == "" {
		safe = "branch"
	}
	if len(sid) > 8 {
		sid = sid[:8]
	}
	slug := "desktop-" + safe + "-" + sid
	return slug, worktree.ValidateSlug(slug)
}
func (server *Server) resolveSessionRepository(ctx context.Context, value string, options *sessionRepositoryOptions, sid string) (RepositorySessionLaunch, error) {
	directory, e := server.resolveRepositoryDirectory(value)
	if e != nil {
		return RepositorySessionLaunch{}, e
	}
	launch := RepositorySessionLaunch{WorkDir: directory}
	if options == nil || options.Branch == "" && !options.Worktree {
		return launch, nil
	}
	repository := server.inspectRepository(ctx, directory)
	if e = usableRepositoryContext(repository); e != nil {
		return launch, e
	}
	branch := resolveRepositoryBranch(repository, options.Branch)
	if branch == nil {
		name := options.Branch
		if name == "" {
			name = "default branch"
		}
		return launch, repositoryRequestError("REPOSITORY_BRANCH_NOT_FOUND", "Branch not found: "+name)
	}
	plan := sessionRepository{RequestedWorkDir: repository.WorkDir, RepoRoot: *repository.RepoRoot, Branch: branch.Name, Worktree: options.Worktree, BaseRef: repositoryBaseRef(*branch)}
	if options.Worktree {
		slug, e := repositoryWorktreeSlug(branch.Name, sid)
		if e != nil {
			return launch, e
		}
		plan.WorktreeSlug, plan.WorktreePath, plan.WorktreeBranch = slug, worktree.PathFor(plan.RepoRoot, slug), worktree.BranchName(slug)
	}
	launch.WorkDir = repository.WorkDir
	launch.Repository, e = json.Marshal(plan)
	return launch, e
}
func (server *Server) switchRepositoryCheckout(ctx context.Context, directory string, plan sessionRepository) (RepositorySessionLaunch, error) {
	repository := server.inspectRepository(ctx, directory)
	if e := usableRepositoryContext(repository); e != nil {
		return RepositorySessionLaunch{}, e
	}
	branch := resolveRepositoryBranch(repository, plan.Branch)
	if branch == nil {
		return RepositorySessionLaunch{}, repositoryRequestError("REPOSITORY_BRANCH_NOT_FOUND", "Branch not found: "+plan.Branch)
	}
	if repository.CurrentBranch == nil || branch.Name != *repository.CurrentBranch {
		if branch.CheckedOut {
			return RepositorySessionLaunch{}, repositoryRequestError("REPOSITORY_BRANCH_CHECKED_OUT", fmt.Sprintf("Branch \"%s\" is already checked out in another worktree.", branch.Name))
		}
		args := []string{"switch", branch.Name}
		if !branch.Local {
			args = []string{"switch", "--track", "-c", branch.Name, repositoryBaseRef(*branch)}
		}
		result := server.repositoryGit(ctx, repository.WorkDir, args...)
		if result.code != 0 {
			reason := trimJS(result.stderr)
			if reason == "" {
				reason = trimJS(result.stdout)
			}
			if reason == "" {
				reason = "git switch failed"
			}
			return RepositorySessionLaunch{}, repositoryRequestError("REPOSITORY_SWITCH_FAILED", "Failed to switch branch: "+reason)
		}
	}
	updated := sessionRepository{RequestedWorkDir: repository.WorkDir, RepoRoot: *repository.RepoRoot, Branch: branch.Name, BaseRef: repositoryBaseRef(*branch)}
	data, e := json.Marshal(updated)
	return RepositorySessionLaunch{WorkDir: repository.WorkDir, Repository: data}, e
}
func (server *Server) createRepositoryBranchHTTP(writer http.ResponseWriter, request *http.Request) error {
	if request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	body, e := jsonObject(request)
	if e != nil {
		return e
	}
	directory := stringField(body, "workDir")
	if directory == "" {
		return badRequest("workDir is required")
	}
	var name string
	if raw, ok := body["name"]; !ok || json.Unmarshal(raw, &name) != nil || string(raw) == "null" {
		return badRequest("name must be a string")
	}
	from := ""
	if raw, ok := body["from"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &from) != nil {
			return badRequest("from must be a string")
		}
	}
	result, e := server.createRepositoryBranch(request.Context(), directory, name, from)
	if e != nil {
		return e
	}
	writeJSON(writer, http.StatusCreated, result)
	return nil
}
func (server *Server) createRepositoryBranch(ctx context.Context, directory, name, from string) (any, error) {
	directory, e := server.resolveRepositoryDirectory(directory)
	if e != nil {
		return nil, e
	}
	repository := server.inspectRepository(ctx, directory)
	if e = usableRepositoryContext(repository); e != nil {
		return nil, e
	}
	name = trimJS(name)
	if name == "" {
		return nil, repositoryRequestError("REPOSITORY_BRANCH_NAME_INVALID", "Branch name is required")
	}
	if strings.HasPrefix(name, "-") {
		return nil, repositoryRequestError("REPOSITORY_BRANCH_NAME_INVALID", fmt.Sprintf("Invalid branch name: \"%s\" must not start with \"-\"", name))
	}
	length := len(utf16.Encode([]rune(name)))
	if length > 200 {
		return nil, repositoryRequestError("REPOSITORY_BRANCH_NAME_INVALID", fmt.Sprintf("Invalid branch name: must be 200 characters or fewer (got %d)", length))
	}
	if strings.HasPrefix(name, "worktree-desktop-") {
		return nil, repositoryRequestError("REPOSITORY_BRANCH_NAME_INVALID", fmt.Sprintf("Invalid branch name: \"%s\" uses a prefix reserved for isolated worktrees", name))
	}
	checked := server.repositoryGit(ctx, directory, "check-ref-format", "refs/heads/"+name)
	if checked.code != 0 {
		return nil, repositoryRequestError("REPOSITORY_BRANCH_NAME_INVALID", fmt.Sprintf("Invalid branch name: \"%s\" is not a valid Git branch name", name))
	}
	if repository.HeadCommit == nil {
		return nil, repositoryRequestError("REPOSITORY_NO_COMMITS", "This repository has no commits yet, so there is nothing to branch from.")
	}
	for _, branch := range repository.Branches {
		if branch.Name == name {
			return nil, repositoryRequestError("REPOSITORY_BRANCH_EXISTS", "Branch already exists: "+name)
		}
	}
	baseRef := "HEAD"
	if from = trimJS(from); from != "" {
		branch := resolveRepositoryBranch(repository, from)
		if branch == nil {
			return nil, repositoryRequestError("REPOSITORY_BRANCH_NOT_FOUND", "Branch not found: "+from)
		}
		baseRef = repositoryBaseRef(*branch)
	}
	result := server.repositoryGit(ctx, directory, "branch", "--", name, baseRef)
	if result.code != 0 {
		reason := trimJS(result.stderr)
		code := "REPOSITORY_BRANCH_CREATE_FAILED"
		if strings.Contains(strings.ToLower(reason), "already exists") {
			code = "REPOSITORY_BRANCH_EXISTS"
		}
		if reason == "" {
			reason = trimJS(result.stdout)
		}
		if reason == "" {
			reason = "git branch failed"
		}
		return nil, repositoryRequestError(code, "Failed to create branch: "+reason)
	}
	return map[string]any{"branch": name, "baseRef": baseRef, "context": server.inspectRepository(ctx, directory)}, nil
}

func (server *Server) sameRepositoryPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return resolveFrom(server.options.WorkDir, left) == resolveFrom(server.options.WorkDir, right)
}
func (server *Server) prepareRepositorySessionLaunch(ctx context.Context, info sessionInfo, cwd string, resume bool) (RepositorySessionLaunch, error) {
	metadata := protocol.NewJSONObject()
	if e := metadata.UnmarshalJSON(info.Metadata); e != nil {
		return RepositorySessionLaunch{}, e
	}
	var plan sessionRepository
	if e := json.Unmarshal(metadata.Get("repository"), &plan); e != nil {
		return RepositorySessionLaunch{}, e
	}
	var worktreeSession struct {
		Path string `json:"worktreePath"`
	}
	_ = json.Unmarshal(metadata.Get("worktreeSession"), &worktreeSession)
	materialized := server.sameRepositoryPath(cwd, plan.WorktreePath) || server.sameRepositoryPath(cwd, worktreeSession.Path) || server.sameRepositoryPath(plan.WorktreePath, worktreeSession.Path)
	result := RepositorySessionLaunch{WorkDir: cwd, Repository: metadata.Get("repository")}
	// Source replaces an empty placeholder before checkout preparation, even
	// when the subsequent git switch fails.
	if !resume {
		if e := server.resetRepositoryPlaceholder(ctx, info); e != nil {
			return result, e
		}
	}
	if !resume && plan.Worktree && !materialized {
		result.WorkDir = plan.RequestedWorkDir
		if result.WorkDir == "" {
			result.WorkDir = plan.RepoRoot
		}
		if result.WorkDir == "" {
			result.WorkDir = cwd
		}
		result.WorktreeName = plan.WorktreeSlug
		if result.WorktreeName == "" {
			result.WorktreeName = plan.WorktreeBranch
		}
		if result.WorktreeName == "" {
			result.WorktreeName = plan.Branch
		}
		result.BaseRef = plan.BaseRef
	} else if !resume && !materialized {
		var e error
		result, e = server.switchRepositoryCheckout(ctx, cwd, plan)
		if e != nil {
			return result, e
		}
	} else if plan.Worktree {
		plan.Worktree = false
		result.Repository, _ = json.Marshal(plan)
	}
	return result, nil
}
func (server *Server) resetRepositoryPlaceholder(ctx context.Context, info sessionInfo) error {
	persist, e := server.shouldPersist(ctx)
	if e != nil || !persist || info.memory {
		return e
	}
	data, e := os.ReadFile(info.Path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	messageID, e := uuid()
	if e != nil {
		return e
	}
	snapshotID, e := uuid()
	if e != nil {
		return e
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	initial := map[string]any{"type": "file-history-snapshot", "messageId": messageID, "snapshot": map[string]any{"messageId": snapshotID, "trackedFileBackups": map[string]any{}, "timestamp": now}, "isSnapshotUpdate": false}
	first, _ := json.Marshal(initial)
	lines := [][]byte{first}
	var lastMetadata, lastTitle []byte
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var record protocol.JSONObject
		if record.UnmarshalJSON(line) != nil {
			continue
		}
		switch record.String("type") {
		case "session-meta":
			lastMetadata = append([]byte{}, line...)
		case "custom-title":
			lastTitle = append([]byte{}, line...)
		}
	}
	if lastMetadata != nil {
		lines = append(lines, lastMetadata)
	}
	if lastTitle != nil {
		lines = append(lines, lastTitle)
	}
	current, e := server.shouldPersist(ctx)
	if e != nil || !current {
		return e
	}
	if e = os.WriteFile(info.Path, append(bytes.Join(lines, []byte{'\n'}), '\n'), 0600); e == nil {
		server.noteSessionMutation(info.Path)
	}
	return e
}
func (server *Server) finalizeRepositorySessionLaunch(ctx context.Context, info sessionInfo, cwd string, repository json.RawMessage) error {
	metadata := protocol.NewJSONObject()
	if e := metadata.UnmarshalJSON(info.Metadata); e != nil {
		return e
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "workDir", Value: json.RawMessage(strconvQuote(cwd))})
	if repository != nil {
		value.Set("repository", repository)
	}
	for _, name := range []string{"permissionMode", "runtimeProviderId", "runtimeModelId", "effortLevel", "openAIFastMode"} {
		if v := metadata.Get(name); v != nil {
			value.Set(name, v)
		}
	}
	data, _ := value.MarshalJSON()
	if e := server.updateLaunchMetadata(ctx, info.ID, data); e != nil {
		return e
	}
	persist, e := server.shouldPersist(ctx)
	if e != nil || !persist || info.memory {
		return e
	}
	target := filepath.Join(server.options.ConfigDir, "projects", projectpath.Sanitize(cwd), info.ID+".jsonl")
	if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
		return e
	}
	value.Set("type", json.RawMessage(strconvQuote("session-meta")))
	value.Set("isMeta", json.RawMessage("true"))
	value.Set("timestamp", json.RawMessage(strconvQuote(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))))
	encoded, _ := value.MarshalJSON()
	file, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	_, e = file.Write(append(encoded, '\n'))
	// Different path spellings can name the same live transcript on macOS or
	// Windows; hard links have the same identity on other platforms too.
	sameTranscript := target == info.Path || sameTranscriptFile(target, info.Path)
	var title []byte
	if !sameTranscript {
		if old, e := os.ReadFile(info.Path); e == nil {
			for _, line := range bytes.Split(old, []byte{'\n'}) {
				var entry protocol.JSONObject
				if entry.UnmarshalJSON(line) == nil && entry.String("type") == "custom-title" {
					title = append([]byte{}, line...)
				}
			}
		}
	}
	if e == nil && title != nil {
		_, e = file.Write(append(title, '\n'))
	}
	closed := file.Close()
	if e == nil {
		e = closed
	}
	if e != nil {
		return e
	}
	server.noteSessionMutation(target)
	// A repository launch can relocate the transcript. Only a metadata-only
	// placeholder may be removed; existing conversation files remain protected.
	if !sameTranscript {
		previous, e := server.scanSession(ctx, info.Path)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if previous.MessageCount == 0 {
			if e = os.Remove(info.Path); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
			server.noteSessionMutation(info.Path)
		}
	}
	return nil
}

func sameTranscriptFile(left, right string) bool {
	a, err := os.Stat(left)
	if err != nil {
		return false
	}
	b, err := os.Stat(right)
	return err == nil && os.SameFile(a, b)
}
