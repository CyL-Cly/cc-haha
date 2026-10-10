package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

func nonemptyStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func sameResolvedPath(base, left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return resolveFrom(base, left) == resolveFrom(base, right)
}

var remoteRepoPatterns = []*regexp.Regexp{regexp.MustCompile(`/([^/]+?)(?:\.git)?$`), regexp.MustCompile(`:([^/]+/[^/]+?)(?:\.git)?$`)}

func remoteRepoName(remote *string) string {
	if remote == nil || *remote == "" {
		return ""
	}
	for _, pattern := range remoteRepoPatterns {
		if match := pattern.FindStringSubmatch(*remote); match != nil {
			return match[1]
		}
	}
	return ""
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func repoDirectoryName(value string) string {
	name := filepath.Base(value)
	if name != string(filepath.Separator) && name != "." {
		return name
	}
	parts := strings.FieldsFunc(value, func(character rune) bool { return character == '/' || character == '\\' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (server *Server) sessionGitInfoHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	workDir := info.WorkDir
	if workDir == "" {
		return notFound("Session not found: " + info.ID)
	}
	server.RegisterFilesystemAccessRoot(workDir)
	var metadata, repository, linked map[string]json.RawMessage
	_ = json.Unmarshal(info.Metadata, &metadata)
	_ = json.Unmarshal(metadata["repository"], &repository)
	_ = json.Unmarshal(metadata["worktreeSession"], &linked)
	branch := nonemptyStringPointer(firstString(stringField(repository, "branch"), stringField(linked, "originalBranch")))
	planned := firstString(stringField(linked, "worktreePath"), stringField(repository, "worktreePath"))
	active := stringField(linked, "worktreePath")
	if active == "" && sameResolvedPath(server.options.WorkDir, workDir, planned) {
		active = workDir
	}
	var worktree map[string]any
	if string(repository["worktree"]) == "true" || linked != nil {
		worktree = map[string]any{"enabled": true, "path": nonemptyStringPointer(active), "plannedPath": nonemptyStringPointer(planned), "sourceWorkDir": nonemptyStringPointer(firstString(stringField(linked, "originalCwd"), stringField(repository, "requestedWorkDir"), stringField(repository, "repoRoot"))), "slug": nonemptyStringPointer(firstString(stringField(linked, "worktreeName"), stringField(repository, "worktreeSlug"))), "branch": nonemptyStringPointer(firstString(stringField(linked, "worktreeBranch"), stringField(repository, "worktreeBranch")))}
	}
	name := repoDirectoryName(workDir)
	changed := 0
	if server.findCachedGitRoot(workDir) != "" {
		gitBranch := server.gitInfoCommand(request.Context(), workDir, "rev-parse", "--abbrev-ref", "HEAD")
		if branch == nil {
			if worktree != nil && (sameResolvedPath(server.options.WorkDir, workDir, active) || sameResolvedPath(server.options.WorkDir, workDir, planned)) {
				branch = nonemptyStringPointer(firstString(stringField(linked, "worktreeBranch"), stringField(repository, "worktreeBranch")))
			}
			if branch == nil {
				branch = gitBranch
			}
		}
		remote := server.gitInfoCommand(request.Context(), workDir, "remote", "get-url", "origin")
		if remoteName := remoteRepoName(remote); remoteName != "" {
			name = remoteName
		}
		status := server.gitInfoCommand(request.Context(), workDir, "-c", "core.fsmonitor=false", "status", "--porcelain")
		if status != nil {
			for _, line := range strings.Split(*status, "\n") {
				if line != "" {
					changed++
				}
			}
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"branch": branch, "repoName": name, "workDir": workDir, "changedFiles": changed, "worktree": worktree})
	return nil
}
