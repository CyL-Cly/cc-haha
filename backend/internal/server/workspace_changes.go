package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type workspaceChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	diff      string
}
type workspaceGitEntry struct {
	workspaceChange
	repoPath, repoOldPath string
}
type workspaceRepository struct {
	root, state, failure string
	branch               *string
}

func orderedHistoryKeys(raw json.RawMessage) []string {
	var object protocol.JSONObject
	if err := object.UnmarshalJSON(raw); err != nil || !object.IsObject() {
		return nil
	}
	keys := []string{}
	for _, property := range object.OwnProperties() {
		var name string
		if json.Unmarshal(property.NameJSON, &name) == nil {
			keys = append(keys, name)
		}
	}
	return keys
}
func workspaceToolPath(raw json.RawMessage, root string) string {
	var path string
	if json.Unmarshal(raw, &path) != nil || trimJS(path) == "" {
		return ""
	}
	absolute := normalizeDriveRoot(path)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, absolute)
	}
	absolute = filepath.Clean(absolute)
	if !sameOrInsideFilesystemPath(absolute, root) {
		return ""
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return ""
	}
	return normalizeWorkspacePath(relative)
}
func workspaceNullish(raw ...json.RawMessage) json.RawMessage {
	for _, value := range raw {
		if len(value) > 0 && string(bytes.TrimSpace(value)) != "null" {
			return value
		}
	}
	return nil
}
func workspaceLineCount(value string) int {
	if value == "" {
		return 0
	}
	count := strings.Count(value, "\n") + 1
	if strings.HasSuffix(value, "\n") {
		count--
	}
	return count
}
func workspaceSyntheticDiff(oldPath, newPath, before, after string) string {
	lines := func(value string) []string {
		if value == "" {
			return nil
		}
		result := strings.Split(value, "\n")
		if result[len(result)-1] == "" {
			result = result[:len(result)-1]
		}
		return result
	}
	oldLines, newLines := lines(before), lines(after)
	oldLabel, newLabel := oldPath, newPath
	if oldPath != "/dev/null" {
		oldLabel = "a/" + oldPath
	}
	if newPath != "/dev/null" {
		newLabel = "b/" + newPath
	}
	result := []string{"diff --session " + oldLabel + " " + newLabel, "--- " + oldLabel, "+++ " + newLabel, fmt.Sprintf("@@ -1,%d +1,%d @@", len(oldLines), len(newLines))}
	for _, line := range oldLines {
		result = append(result, "-"+line)
	}
	for _, line := range newLines {
		result = append(result, "+"+line)
	}
	return strings.Join(result, "\n")
}

var workspacePatchFile = regexp.MustCompile(`^\*\*\* (?:Add|Update|Delete) File: (.+)$`)

func workspaceChangesFromTool(name string, input historyObject, root string) []workspaceChange {
	path := workspaceToolPath(workspaceNullish(input["file_path"], input["path"]), root)
	edit := func(values historyObject) workspaceChange {
		before, after := stringField(values, "old_string"), stringField(values, "new_string")
		status := "added"
		if before != "" {
			status = "modified"
		}
		return workspaceChange{Path: path, Status: status, Additions: workspaceLineCount(after), Deletions: workspaceLineCount(before), diff: workspaceSyntheticDiff(path, path, before, after)}
	}
	switch strings.ToLower(name) {
	case "write":
		if path != "" {
			content := stringField(input, "content")
			return []workspaceChange{{Path: path, Status: "added", Additions: workspaceLineCount(content), diff: workspaceSyntheticDiff("/dev/null", path, "", content)}}
		}
	case "edit":
		if path != "" {
			return []workspaceChange{edit(input)}
		}
	case "multiedit":
		if path != "" {
			result := []workspaceChange{}
			for _, values := range contentBlocks(input["edits"]) {
				result = append(result, edit(values))
			}
			return result
		}
	case "notebookedit":
		path = workspaceToolPath(workspaceNullish(input["notebook_path"], input["file_path"], input["path"]), root)
		if path != "" {
			before, after := stringField(input, "old_source"), stringField(input, "new_source")
			status := "added"
			if before != "" {
				status = "modified"
			}
			return []workspaceChange{{Path: path, Status: status, Additions: workspaceLineCount(after), Deletions: workspaceLineCount(before), diff: workspaceSyntheticDiff(path, path, before, after)}}
		}
	case "apply_patch":
		result := []workspaceChange{}
		for _, line := range strings.Split(stringField(input, "patch"), "\n") {
			match := workspacePatchFile.FindStringSubmatch(line)
			if len(match) != 2 {
				continue
			}
			path = workspaceToolPath(jsonString(match[1]), root)
			if path == "" {
				continue
			}
			status := "modified"
			if strings.Contains(line, "Add File") {
				status = "added"
			} else if strings.Contains(line, "Delete File") {
				status = "deleted"
			}
			result = append(result, workspaceChange{Path: path, Status: status})
		}
		return result
	}
	return nil
}

func (server *Server) workspaceTranscriptChanges(ctx context.Context, info sessionInfo, root string) ([]workspaceChange, error) {
	if info.memory {
		return []workspaceChange{}, nil
	}
	data, err := server.recoverSessionHistory(ctx, info)
	if err != nil {
		return nil, err
	}
	var recovery struct {
		Status       string `json:"status"`
		Completeness struct {
			Workspace *bool `json:"workspace"`
		} `json:"completeness"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &recovery); err != nil {
		return nil, err
	}
	complete := recovery.Status == "ready"
	if recovery.Completeness.Workspace != nil {
		complete = *recovery.Completeness.Workspace
	}
	if !complete {
		return nil, &apiError{413, "HISTORY_WORKSPACE_LIMIT", "Workspace transcript exceeds the viewing budget"}
	}
	// Workspace uses the bounded root recovery projection and its workspace
	// completeness, independently of activity and full turn checkpoint budgets.
	projected := recovery.Messages
	messages := []historyObject{}
	errors := map[string]bool{}
	for _, raw := range projected {
		message := historyRecord(raw)
		messages = append(messages, message)
		if stringField(message, "type") == "tool_result" {
			for _, block := range contentBlocks(message["content"]) {
				if stringField(block, "type") == "tool_result" && string(bytes.TrimSpace(block["is_error"])) == "true" {
					var id string
					if json.Unmarshal(block["tool_use_id"], &id) == nil {
						errors[id] = true
					}
				}
			}
		}
	}
	result := []workspaceChange{}
	indexes := map[string]int{}
	for _, message := range messages {
		if stringField(message, "type") != "tool_use" {
			continue
		}
		for _, block := range contentBlocks(message["content"]) {
			if stringField(block, "type") != "tool_use" || errors[stringField(block, "id")] {
				continue
			}
			for _, change := range workspaceChangesFromTool(stringField(block, "name"), historyRecord(block["input"]), root) {
				if index, exists := indexes[change.Path]; exists {
					existing := result[index]
					if existing.Status == "added" {
						change.Status = existing.Status
					}
					change.Additions += existing.Additions
					change.Deletions += existing.Deletions
					pieces := []string{}
					for _, diff := range []string{existing.diff, change.diff} {
						if diff != "" {
							pieces = append(pieces, diff)
						}
					}
					change.diff = strings.Join(pieces, "\n")
					result[index] = change
				} else {
					indexes[change.Path] = len(result)
					result = append(result, change)
				}
			}
		}
	}
	return result, ctx.Err()
}

func workspaceTextFile(path string) *string {
	content, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(content, 0) >= 0 {
		return nil
	}
	text := string(protocol.DecodeJavaScriptUTF8(content))
	return &text
}
func (server *Server) workspaceSnapshotChanges(ctx context.Context, info sessionInfo, root string) ([]workspaceChange, error) {
	evidence, err := server.checkpointSnapshots(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	if !evidence.Complete {
		return nil, &apiError{413, "HISTORY_WORKSPACE_LIMIT", "Workspace file-history evidence is incomplete"}
	}
	order := []string{}
	snapshots := map[string]historyObject{}
	for _, raw := range evidence.Rows {
		row := historyRecord(raw)
		snapshot := historyRecord(row["snapshot"])
		id := stringField(snapshot, "messageId")
		if id == "" {
			id = stringField(row, "messageId")
		}
		if id == "" {
			continue
		}
		if _, exists := snapshots[id]; !exists {
			order = append(order, id)
		}
		snapshots[id] = snapshot
	}
	paths := []string{}
	backups := map[string]json.RawMessage{}
	for _, id := range order {
		snapshot := snapshots[id]
		tracked := historyRecord(snapshot["trackedFileBackups"])
		for _, path := range orderedHistoryKeys(snapshot["trackedFileBackups"]) {
			if _, exists := backups[path]; !exists {
				paths = append(paths, path)
				backups[path] = historyRecord(tracked[path])["backupFileName"]
			}
		}
	}
	result := []workspaceChange{}
	files := sessionrewind.NativeFiles{ConfigHome: server.options.ConfigDir}
	for _, tracked := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relative := workspaceToolPath(jsonString(tracked), root)
		if relative == "" || backups[tracked] == nil {
			continue
		}
		var before *string
		if string(bytes.TrimSpace(backups[tracked])) != "null" {
			var name string
			if json.Unmarshal(backups[tracked], &name) != nil {
				return nil, unsupported("workspace non-string file-history backup name")
			}
			data, err := files.ReadBackup(ctx, info.ID, name)
			if err == nil && bytes.IndexByte(data, 0) < 0 {
				text := string(protocol.DecodeJavaScriptUTF8(data))
				before = &text
			}
		}
		after := workspaceTextFile(filepath.Join(root, filepath.FromSlash(relative)))
		if before == nil && after == nil || before != nil && after != nil && *before == *after {
			continue
		}
		beforeText, afterText, oldPath, newPath, status := "", "", relative, relative, "modified"
		if before == nil {
			status, oldPath = "added", "/dev/null"
		} else {
			beforeText = *before
		}
		if after == nil {
			if before != nil {
				status = "deleted"
			}
			newPath = "/dev/null"
		} else {
			afterText = *after
		}
		added, deleted := sessionrewind.CountDiffLines(ctx, beforeText, afterText)
		result = append(result, workspaceChange{Path: relative, Status: status, Additions: added, Deletions: deleted, diff: workspaceSyntheticDiff(oldPath, newPath, beforeText, afterText)})
	}
	return result, ctx.Err()
}

func (server *Server) workspaceStoredChanges(ctx context.Context, info sessionInfo, root string) ([]workspaceChange, error) {
	transcript, err := server.workspaceTranscriptChanges(ctx, info, root)
	if err != nil {
		return nil, err
	}
	snapshots, err := server.workspaceSnapshotChanges(ctx, info, root)
	if err != nil {
		return nil, err
	}
	result, indexes := []workspaceChange{}, map[string]int{}
	for _, change := range append(transcript, snapshots...) {
		if index, exists := indexes[change.Path]; exists {
			if change.diff == "" {
				change.diff = result[index].diff
			}
			result[index] = change
		} else {
			indexes[change.Path] = len(result)
			result = append(result, change)
		}
	}
	return result, nil
}

func (server *Server) workspaceGit(ctx context.Context, root string, args ...string) repositoryGitResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := server.repositoryGit(ctx, root, args...)
	result.stdout = string(protocol.DecodeJavaScriptUTF8([]byte(result.stdout)))
	result.stderr = string(protocol.DecodeJavaScriptUTF8([]byte(result.stderr)))
	return result
}
func workspaceGitError(prefix, root string, result repositoryGitResult, args ...string) string {
	message := trimJS(result.stderr)
	if message == "" {
		message = trimJS(result.stdout)
	}
	if message == "" {
		message = "unknown git failure"
	}
	return fmt.Sprintf("%s (git %s in %s): %s", prefix, strings.Join(args, " "), root, message)
}
func (server *Server) workspaceRepository(ctx context.Context, directory string) workspaceRepository {
	args := []string{"rev-parse", "--show-toplevel"}
	result := server.workspaceGit(ctx, directory, args...)
	if result.code != 0 {
		if strings.Contains(trimJS(result.stderr), "not a git repository") {
			return workspaceRepository{state: "not_git_repo"}
		}
		return workspaceRepository{state: "error", failure: workspaceGitError("Failed to inspect git repository", directory, result, args...)}
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(trimJS(result.stdout)))
	if err != nil {
		return workspaceRepository{state: "error", failure: workspaceFSError("Failed to canonicalize git repository root", trimJS(result.stdout), err)}
	}
	result = server.workspaceGit(ctx, directory, "rev-parse", "--abbrev-ref", "HEAD")
	var branch *string
	if value := trimJS(result.stdout); result.code == 0 && value != "" {
		branch = &value
	}
	return workspaceRepository{state: "ok", root: root, branch: branch}
}
func workspaceStatusKind(code string) string {
	if code == "??" {
		return "untracked"
	}
	for index, character := range []string{"R", "C", "T", "D", "A", "M"} {
		if strings.Contains(code, character) {
			return []string{"renamed", "copied", "type_changed", "deleted", "added", "modified"}[index]
		}
	}
	return "unknown"
}
func (server *Server) workspaceStatusEntries(ctx context.Context, repo, root string) ([]workspaceGitEntry, string) {
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}
	result := server.workspaceGit(ctx, repo, args...)
	if result.code != 0 {
		return nil, workspaceGitError("Failed to read git status", repo, result, args...)
	}
	entries := []workspaceGitEntry{}
	parts := strings.Split(result.stdout, "\x00")
	rebase := func(path string) (string, bool) {
		absolute := filepath.Join(repo, filepath.FromSlash(path))
		if !sameOrInsideFilesystemPath(absolute, root) {
			return "", false
		}
		value, err := filepath.Rel(root, absolute)
		if err != nil || value == "." {
			return "", false
		}
		return normalizeWorkspacePath(value), true
	}
	for index := 0; index < len(parts); index++ {
		part := parts[index]
		if len(part) < 3 {
			continue
		}
		status, path := workspaceStatusKind(part[:2]), normalizeWorkspacePath(part[3:])
		old := ""
		if status == "renamed" || status == "copied" {
			index++
			if index < len(parts) {
				old = normalizeWorkspacePath(parts[index])
			}
		}
		relative, inside := rebase(path)
		if !inside {
			continue
		}
		oldRelative, _ := rebase(old)
		if old == "" {
			oldRelative = ""
		}
		entries = append(entries, workspaceGitEntry{workspaceChange: workspaceChange{Path: relative, OldPath: oldRelative, Status: status}, repoPath: path, repoOldPath: old})
	}
	return entries, ""
}

var workspaceNewline = regexp.MustCompile(`\r\n|\r|\n`)

func workspaceTextLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := workspaceNewline.Split(text, -1)
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
func workspaceNumstat(raw string) int {
	prefix := parseIntPrefix.FindString(raw)
	value, _ := strconv.Atoi(prefix)
	return value
}
func (server *Server) workspaceGitChanges(ctx context.Context, info sessionInfo, repo workspaceRepository, canonicalRoot string) ([]workspaceChange, string) {
	entries, failure := server.workspaceStatusEntries(ctx, repo.root, canonicalRoot)
	if failure != "" {
		return nil, failure
	}
	stats := map[string][2]int{}
	tracked := false
	for _, entry := range entries {
		tracked = tracked || entry.Status != "untracked"
	}
	if tracked {
		args := []string{"diff", "--numstat", "--find-renames", "--find-copies", "HEAD", "--"}
		result := server.workspaceGit(ctx, repo.root, args...)
		if result.code != 0 {
			return nil, workspaceGitError("Failed to read git diff stats", repo.root, result, args...)
		}
		for _, line := range strings.Split(trimJS(result.stdout), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) >= 3 && fields[2] != "" {
				stats[normalizeWorkspacePath(fields[2])] = [2]int{workspaceNumstat(fields[0]), workspaceNumstat(fields[1])}
			}
		}
	}
	result := []workspaceChange{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err.Error()
		}
		change := entry.workspaceChange
		if entry.Status == "untracked" {
			resolved, err := server.resolveWorkspacePath(info, filepath.Join(repo.root, filepath.FromSlash(entry.repoPath)))
			if err != nil {
				return nil, err.Error()
			}
			stat, err := os.Stat(resolved.canonical)
			if err != nil {
				return nil, workspaceFSError("Failed to read untracked workspace file", resolved.canonical, err)
			}
			if stat.Mode().IsRegular() && stat.Size() <= 256*1024 {
				content, err := os.ReadFile(resolved.canonical)
				if err != nil {
					return nil, workspaceFSError("Failed to read untracked workspace file", resolved.canonical, err)
				}
				change.Additions = len(workspaceTextLines(string(protocol.DecodeJavaScriptUTF8(content))))
			}
		} else {
			value := stats[entry.repoPath]
			change.Additions, change.Deletions = value[0], value[1]
		}
		result = append(result, change)
	}
	return result, ""
}
func workspaceSortChanges(changes []workspaceChange) {
	comparator := collate.New(language.English)
	sort.SliceStable(changes, func(i, j int) bool { return comparator.CompareString(changes[i].Path, changes[j].Path) < 0 })
}

func (server *Server) workspaceStatus(ctx context.Context, info sessionInfo) (map[string]any, error) {
	root := server.filesystemPath(info.WorkDir)
	response := map[string]any{"state": "ok", "workDir": root, "repoName": nil, "branch": nil, "isGitRepo": false, "changedFiles": []workspaceChange{}}
	_, err := os.Stat(root)
	if workspaceMissing(err) {
		response["state"] = "missing_workdir"
		return response, nil
	}
	if err != nil {
		response["state"] = "error"
		response["error"] = workspaceFSError("Failed to stat workspace path", root, err)
		return response, nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		response["state"], response["error"] = "error", workspaceFSError("Failed to canonicalize workspace root", root, err)
		return response, nil
	}
	repo := server.workspaceRepository(ctx, root)
	stored, err := server.workspaceStoredChanges(ctx, info, root)
	if err != nil {
		return nil, err
	}
	if repo.state == "not_git_repo" {
		workspaceSortChanges(stored)
		response["repoName"], response["changedFiles"] = filepath.Base(root), stored
		return response, nil
	}
	if repo.state == "error" {
		response["state"], response["error"] = "error", repo.failure
		return response, nil
	}
	response["repoName"], response["branch"], response["isGitRepo"] = filepath.Base(repo.root), repo.branch, true
	changes, failure := server.workspaceGitChanges(ctx, info, repo, canonicalRoot)
	if failure != "" {
		response["state"], response["error"] = "error", failure
		return response, nil
	}
	seen := map[string]bool{}
	for _, change := range changes {
		seen[change.Path] = true
	}
	for _, change := range stored {
		if !seen[change.Path] {
			changes = append(changes, change)
			seen[change.Path] = true
		}
	}
	workspaceSortChanges(changes)
	response["changedFiles"] = changes
	return response, ctx.Err()
}

func (server *Server) workspaceStoredDiff(ctx context.Context, info sessionInfo, resolved workspacePath) (string, error) {
	changes, err := server.workspaceTranscriptChanges(ctx, info, resolved.root)
	if err != nil {
		return "", err
	}
	for _, change := range changes {
		if change.Path != resolved.relative {
			continue
		}
		if trimJS(change.diff) != "" {
			return change.diff, nil
		}
		file := workspaceReadFile(resolved)
		if file["state"] == "ok" && file["previewType"] == "text" {
			return workspaceSyntheticDiff("/dev/null", resolved.relative, "", file["content"].(string)), nil
		}
		break
	}
	changes, err = server.workspaceSnapshotChanges(ctx, info, resolved.root)
	if err != nil {
		return "", err
	}
	for _, change := range changes {
		if change.Path == resolved.relative {
			return change.diff, nil
		}
	}
	return "", nil
}
func workspaceUntrackedDiff(resolved workspacePath) (string, string, error) {
	stat, err := os.Stat(resolved.canonical)
	if workspaceMissing(err) || err == nil && !stat.Mode().IsRegular() {
		return "missing", "", nil
	}
	if err != nil {
		return "error", "", err
	}
	content, err := os.ReadFile(resolved.canonical)
	if err != nil {
		return "error", "", err
	}
	path := resolved.relative
	lines := []string{"diff --git a/" + path + " b/" + path, "new file mode 100644"}
	if bytes.IndexByte(content, 0) >= 0 {
		lines = append(lines, "Binary files /dev/null and b/"+path+" differ", "")
		return "ok", strings.Join(lines, "\n"), nil
	}
	text := workspaceTextLines(string(protocol.DecodeJavaScriptUTF8(content)))
	if len(text) == 0 {
		text = []string{""}
	}
	lines = append(lines, "--- /dev/null", "+++ b/"+path, fmt.Sprintf("@@ -0,0 +1,%d @@", len(text)))
	for _, value := range text {
		lines = append(lines, "+"+value)
	}
	lines = append(lines, "")
	return "ok", strings.Join(lines, "\n"), nil
}
func (server *Server) workspaceDiff(ctx context.Context, info sessionInfo, path string) (map[string]any, error) {
	response := map[string]any{"state": "error", "path": normalizeWorkspacePath(path)}
	resolved, err := server.resolveWorkspacePath(info, path)
	if err != nil {
		response["error"] = err.Error()
		return response, nil
	}
	response["path"] = resolved.relative
	repo := server.workspaceRepository(ctx, resolved.root)
	entries, failure := []workspaceGitEntry{}, repo.failure
	if repo.state == "ok" {
		entries, failure = server.workspaceStatusEntries(ctx, repo.root, func() string {
			canonical, _ := filepath.EvalSymlinks(resolved.root)
			return canonical
		}())
	}
	relative, _ := filepath.Rel(repo.root, resolved.canonical)
	repoPath := normalizeWorkspacePath(relative)
	var selected *workspaceGitEntry
	for index := range entries {
		entry := &entries[index]
		if entry.repoPath == repoPath || entry.repoOldPath == repoPath {
			selected = entry
			break
		}
	}
	if repo.state != "ok" || failure != "" || selected == nil {
		diff, err := server.workspaceStoredDiff(ctx, info, resolved)
		if err != nil {
			return nil, err
		}
		if diff != "" {
			response["state"], response["diff"] = "ok", diff
		} else if repo.state == "not_git_repo" {
			response["state"] = "not_git_repo"
		} else if failure != "" {
			response["error"] = failure
		} else {
			response["state"] = "missing"
		}
		return response, nil
	}
	if selected.Status == "untracked" {
		state, diff, err := workspaceUntrackedDiff(resolved)
		response["state"] = state
		if err != nil {
			response["error"] = workspaceFSError("Failed to read untracked workspace file", resolved.canonical, err)
		} else if state == "ok" {
			response["diff"] = diff
		}
		return response, nil
	}
	args := []string{"diff", "--no-ext-diff", "--binary", "--find-renames", "--find-copies", "HEAD", "--", selected.repoPath}
	result := server.workspaceGit(ctx, repo.root, args...)
	if result.code != 0 {
		response["error"] = workspaceGitError("Failed to read git diff", repo.root, result, args...)
	} else if trimJS(result.stdout) == "" {
		response["state"] = "missing"
	} else {
		response["state"], response["diff"] = "ok", result.stdout
	}
	return response, ctx.Err()
}
