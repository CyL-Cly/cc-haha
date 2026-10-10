package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return norm.NFC.String(path)
}
func resolveFrom(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

// This is Source's filesystem-only Git identity: linked worktrees may resolve
// to their main repository only when their commondir structure and back-link
// both match. No Git command, hooks, network or working-tree mutation is used.
func canonicalGitRoot(path string) string {
	root := filepath.Clean(path)
	for {
		stat, err := os.Stat(filepath.Join(root, ".git"))
		if err == nil && (stat.IsDir() || stat.Mode().IsRegular()) {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return ""
		}
		root = parent
	}
	return resolveCanonicalGitRoot(root)
}
func resolveCanonicalGitRoot(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(raw)), "gitdir:") {
		return norm.NFC.String(root)
	}
	directory := resolveFrom(root, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "gitdir:")))
	raw, err = os.ReadFile(filepath.Join(directory, "commondir"))
	if err != nil {
		return norm.NFC.String(root)
	}
	common := resolveFrom(directory, strings.TrimSpace(string(raw)))
	if filepath.Dir(directory) != filepath.Join(common, "worktrees") {
		return norm.NFC.String(root)
	}
	raw, err = os.ReadFile(filepath.Join(directory, "gitdir"))
	if err != nil {
		return norm.NFC.String(root)
	}
	backlink, err := filepath.EvalSymlinks(strings.TrimSpace(string(raw)))
	if err != nil {
		return norm.NFC.String(root)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || backlink != filepath.Join(realRoot, ".git") {
		return norm.NFC.String(root)
	}
	if filepath.Base(common) != ".git" {
		return norm.NFC.String(common)
	}
	return norm.NFC.String(filepath.Dir(common))
}
func logicalProjectRoot(workDir string, metadata map[string]json.RawMessage) *string {
	return logicalProjectRootWith(workDir, metadata, canonicalGitRoot)
}
func logicalProjectRootWith(workDir string, metadata map[string]json.RawMessage, gitRoot func(string) string) *string {
	var worktree, repository map[string]json.RawMessage
	_ = json.Unmarshal(metadata["worktreeSession"], &worktree)
	_ = json.Unmarshal(metadata["repository"], &repository)
	candidate := stringField(worktree, "originalCwd")
	if candidate == "" {
		candidate = stringField(repository, "repoRoot")
	}
	if candidate == "" {
		candidate = workDir
	}
	if candidate == "" {
		return nil
	}
	candidate = canonicalPath(candidate)
	if root := gitRoot(candidate); root != "" {
		return &root
	}
	marker := string(filepath.Separator) + ".claude" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
	if workDir != "" {
		if at := strings.Index(candidate, marker); at > 0 {
			candidate = candidate[:at]
		}
	}
	return &candidate
}
func (server *Server) sidebarHTTP(writer http.ResponseWriter, request *http.Request, all []sessionInfo) error {
	requested := "6"
	if value, ok := request.URL.Query()["perProjectLimit"]; ok && len(value) > 0 {
		requested = value[0]
	}
	if requested == "" {
		return badRequest("Invalid perProjectLimit parameter")
	}
	for _, char := range requested {
		if char < '0' || char > '9' {
			return badRequest("Invalid perProjectLimit parameter")
		}
	}
	limit, err := strconv.ParseUint(requested, 10, 64)
	if err != nil || limit == 0 || limit > 9007199254740991 {
		return badRequest("Invalid perProjectLimit parameter")
	}
	if limit > 20 {
		limit = 20
	}
	type project struct {
		Root     string
		Total    int
		seen     map[string]bool
		rendered int
	}
	groups := []*project{}
	byRoot := map[string]*project{}
	sessions := []sessionInfo{}
	total := 0
	for _, info := range all {
		root := info.WorkDir
		if info.ProjectRoot != nil {
			root = *info.ProjectRoot
		}
		if root == "" {
			root = info.ProjectPath
		}
		if root == "" {
			root = "unknown"
		}
		group := byRoot[root]
		if group == nil {
			group = &project{Root: root, seen: map[string]bool{}}
			byRoot[root] = group
			groups = append(groups, group)
		}
		if group.seen[info.ID] {
			continue
		}
		group.seen[info.ID] = true
		group.Total++
		total++
		if group.rendered < int(limit) {
			sessions = append(sessions, info)
			group.rendered++
		}
	}
	projects := []map[string]any{}
	for _, group := range groups {
		projects = append(projects, map[string]any{"projectRoot": group.Root, "total": group.Total})
	}
	writeJSON(writer, 200, map[string]any{"sessions": sessions, "projects": projects, "total": total, "index": server.localIndexStatus()})
	return nil
}
func fileIndexStatus() map[string]any {
	return map[string]any{"mode": "off", "state": "off", "discovered": 0, "indexed": 0, "degradedSources": 0, "databaseBytes": 0, "walBytes": 0, "lastUpdatedAt": nil, "lastErrorCode": nil}
}
