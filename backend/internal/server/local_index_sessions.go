package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var indexedSessionID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Server) localIndexStatus() any {
	if s.options.LocalIndex == nil {
		return fileIndexStatus()
	}
	return s.options.LocalIndex.Status()
}
func (s *Server) noteSessionMutation(path string) {
	s.mu.Lock()
	s.sessionMutation++
	s.mu.Unlock()
	if s.options.LocalIndex != nil {
		s.options.LocalIndex.NoteMutation(path)
	}
}
func (s *Server) usableSessionIndex() string {
	i := s.options.LocalIndex
	if i == nil || !i.ConsumerReadAllowed() {
		return ""
	}
	status := i.Status()
	if status.Mode == "off" || status.State == "off" || status.State == "degraded" {
		return ""
	}
	if status.State != "building" && !i.SessionScopeReady(context.Background()) {
		return ""
	}
	return status.Mode
}
func (s *Server) validateIndexedSessionPath(path, project, id, projectsRoot string) (os.FileInfo, error) {
	invalid := errors.New("Indexed transcript path failed scope validation")
	if !indexedSessionID.MatchString(id) || project == "" || filepath.Base(project) != project || filepath.Base(path) != id+".jsonl" || filepath.Base(filepath.Dir(path)) != project {
		return nil, invalid
	}
	expected := filepath.Join(s.options.ConfigDir, "projects", project, id+".jsonl")
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return nil, e
	}
	expectedReal := real
	if filepath.Clean(expected) != filepath.Clean(path) {
		expectedReal, e = filepath.EvalSymlinks(expected)
		if e != nil {
			return nil, e
		}
	}
	relative, e := filepath.Rel(projectsRoot, real)
	if e != nil || expectedReal != real || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, invalid
	}
	info, e := os.Stat(real)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, invalid
	}
	return info, nil
}
func indexedValue(row protocol.JSONObject, key string) string {
	var value string
	_ = json.Unmarshal(row.Get(key), &value)
	return value
}
func indexSummaryMetadata(row protocol.JSONObject) map[string]json.RawMessage {
	data, _ := row.MarshalJSON()
	out := map[string]json.RawMessage{}
	_ = json.Unmarshal(data, &out)
	return out
}
func (s *Server) hydrateIndexedSession(row protocol.JSONObject) sessionInfo {
	var info sessionInfo
	data, _ := row.MarshalJSON()
	_ = json.Unmarshal(data, &info)
	info.workDirNull = string(row.Get("workDir")) == "null"
	info.Path = indexedValue(row, "transcriptPath")
	metadata := indexSummaryMetadata(row)
	info.Metadata, _ = json.Marshal(metadata)
	info.ProjectRoot = logicalProjectRootWith(info.WorkDir, metadata, s.findCachedCanonicalGitRoot)
	if info.ProjectRoot == nil && info.ProjectPath != "" {
		fallback := desanitizeSessionProject(info.ProjectPath)
		info.ProjectRoot = logicalProjectRootWith(fallback, metadata, s.findCachedCanonicalGitRoot)
	}
	info.WorkDirExists, info.WorkspaceState = sourceWorkspaceAvailability(info.WorkDir, info.ProjectRoot, metadata)
	return info
}
func desanitizeSessionProject(project string) string {
	if len(project) >= 3 && ((project[0] >= 'a' && project[0] <= 'z') || (project[0] >= 'A' && project[0] <= 'Z')) && project[1:3] == "--" {
		return project[:1] + ":\\" + strings.ReplaceAll(project[3:], "-", "\\")
	}
	return strings.ReplaceAll(project, "-", string(filepath.Separator))
}
func normalizedWorkspace(path string) string {
	path = strings.ReplaceAll(canonicalPath(path), "\\", "/")
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}
func sameWorkspace(a, b string) bool {
	return a != "" && b != "" && normalizedWorkspace(a) == normalizedWorkspace(b)
}
func directoryExists(path string) bool {
	if path == "" {
		return false
	}
	info, e := os.Stat(path)
	return e == nil && info.IsDir()
}
func sourceWorkspaceAvailability(workDir string, root *string, metadata map[string]json.RawMessage) (bool, string) {
	if directoryExists(workDir) {
		return true, "available"
	}
	if root == nil || sameWorkspace(workDir, *root) || !directoryExists(*root) {
		return false, "missing"
	}
	var worktree, repository map[string]json.RawMessage
	_ = json.Unmarshal(metadata["worktreeSession"], &worktree)
	_ = json.Unmarshal(metadata["repository"], &repository)
	removed := sameWorkspace(workDir, stringField(worktree, "worktreePath"))
	var enabled bool
	_ = json.Unmarshal(repository["worktree"], &enabled)
	removed = removed || enabled && sameWorkspace(workDir, stringField(repository, "worktreePath"))
	normalized := normalizedWorkspace(workDir)
	if at := strings.Index(normalized, "/.claude/worktrees/"); at > 0 {
		removed = removed || sameWorkspace(normalized[:at], *root)
	}
	if removed {
		return false, "worktree_removed"
	}
	return false, "missing"
}
func (s *Server) trySessionIndex(ctx context.Context, o localindex.SessionListOptions) ([]sessionInfo, int, bool) {
	index := s.options.LocalIndex
	if index == nil {
		return nil, 0, false
	}
	mode := s.usableSessionIndex()
	if mode != "on" {
		return nil, 0, false
	}
	epoch := index.Epoch()
	page, e := index.List(ctx, o)
	if e != nil || page == nil {
		index.NoteConsumerFailure()
		return nil, 0, false
	}
	status := index.Status()
	if status.State == "off" || status.State == "degraded" {
		index.NoteConsumerFailure()
		return nil, 0, false
	}
	if len(page.Sessions) == 0 {
		return []sessionInfo{}, int(page.Total), true
	}
	root, e := filepath.EvalSymlinks(filepath.Join(s.options.ConfigDir, "projects"))
	if e != nil {
		index.NoteConsumerFailure()
		return nil, 0, false
	}
	out := []sessionInfo{}
	for _, row := range page.Sessions {
		path, project, id := indexedValue(row, "transcriptPath"), indexedValue(row, "projectPath"), indexedValue(row, "id")
		if _, e := s.validateIndexedSessionPath(path, project, id, root); e == nil {
			out = append(out, s.hydrateIndexedSession(row))
		}
	}
	if epoch != index.Epoch() || !index.ConsumerReadAllowed() {
		return nil, 0, false
	}
	if len(out) == 0 && status.State != "building" {
		return nil, 0, false
	}
	return out, int(page.Total), true
}
func (s *Server) tryDegradedSessionIndex(ctx context.Context, o localindex.SessionListOptions) ([]sessionInfo, int, bool) {
	index := s.options.LocalIndex
	if index == nil || index.Mode() != "on" || !index.ConsumerReadAllowed() || index.Status().State != "degraded" {
		return nil, 0, false
	}
	paths := index.SourceScopedFailurePaths()
	if paths == nil {
		return nil, 0, false
	}
	epoch := index.Epoch()
	exclude := map[string]bool{}
	for _, path := range paths {
		exclude[filepath.Clean(path)] = true
	}
	rows := []protocol.JSONObject{}
	size := float64(500)
	for offset := float64(0); ; offset += 500 {
		page, e := index.List(ctx, localindex.SessionListOptions{Project: o.Project, Limit: &size, Offset: &offset})
		if e != nil || page == nil {
			index.NoteConsumerFailure()
			return nil, 0, false
		}
		if index.Status().State == "off" || index.Status().State == "degraded" && index.SourceScopedFailurePaths() == nil {
			return nil, 0, false
		}
		for _, row := range page.Sessions {
			if !exclude[filepath.Clean(indexedValue(row, "transcriptPath"))] {
				rows = append(rows, row)
			}
		}
		if int64(offset)+int64(len(page.Sessions)) >= page.Total || len(page.Sessions) == 0 {
			break
		}
	}
	projects := filepath.Join(s.options.ConfigDir, "projects")
	root, e := filepath.EvalSymlinks(projects)
	if e != nil {
		return nil, 0, false
	}
	for _, path := range paths {
		project := filepath.Base(filepath.Dir(path))
		if filepath.Dir(filepath.Dir(path)) != projects || o.Project != nil && *o.Project != project {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if _, e = s.validateIndexedSessionPath(path, project, id, root); e != nil {
			continue
		}
		info, e := s.scanSession(ctx, path)
		if e != nil || info.teamWorker {
			continue
		}
		data, _ := json.Marshal(info)
		var row protocol.JSONObject
		_ = json.Unmarshal(data, &row)
		row.Set("transcriptPath", json.RawMessage(strconvQuote(path)))
		var metadata protocol.JSONObject
		_ = json.Unmarshal(info.Metadata, &metadata)
		for _, field := range []string{"repository", "worktreeSession"} {
			if value := metadata.Get(field); len(value) > 0 {
				row.Set(field, value)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, 0, false
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if indexedValue(a, "modifiedAt") != indexedValue(b, "modifiedAt") {
			return indexedValue(a, "modifiedAt") > indexedValue(b, "modifiedAt")
		}
		if indexedValue(a, "id") != indexedValue(b, "id") {
			return indexedValue(a, "id") < indexedValue(b, "id")
		}
		return indexedValue(a, "transcriptPath") < indexedValue(b, "transcriptPath")
	})
	offset, limit := 0, 50
	if o.Offset != nil {
		offset = int(*o.Offset)
	}
	if o.Limit != nil {
		limit = int(*o.Limit)
	}
	offset = min(len(rows), max(0, offset))
	end := min(len(rows), offset+max(0, limit))
	out := []sessionInfo{}
	for _, row := range rows[offset:end] {
		if _, e = s.validateIndexedSessionPath(indexedValue(row, "transcriptPath"), indexedValue(row, "projectPath"), indexedValue(row, "id"), root); e == nil {
			out = append(out, s.hydrateIndexedSession(row))
		}
	}
	if epoch != index.Epoch() || !index.ConsumerReadAllowed() || len(out) == 0 {
		return nil, 0, false
	}
	return out, len(rows), true
}
func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }
func (s *Server) indexedSessionList(ctx context.Context, o localindex.SessionListOptions) ([]sessionInfo, int, bool) {
	if rows, total, ok := s.trySessionIndex(ctx, o); ok {
		return rows, total, true
	}
	return s.tryDegradedSessionIndex(ctx, o)
}
