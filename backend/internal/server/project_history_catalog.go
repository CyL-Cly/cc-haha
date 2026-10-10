package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) projectHistoryMutation() string {
	s.mu.Lock()
	epoch := s.sessionMutation
	s.mu.Unlock()
	return s.options.ConfigDir + ":" + strconv.FormatUint(epoch, 10)
}
func (s *Server) projectHistoryRevision() string {
	key := s.projectHistoryMutation()
	if i := s.options.LocalIndex; i != nil {
		status := i.Status()
		key += ":" + strconv.FormatUint(i.Epoch(), 10) + ":" + status.Mode + ":" + status.State
	}
	return key
}
func (s *Server) projectHistoryManager() *projectSessionHistory {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projectHistory == nil {
		s.projectHistory = newProjectSessionHistory(projectHistorySource{now: func() int64 { return time.Now().UnixMilli() }, scope: func() string { return s.options.ConfigDir }, revision: s.projectHistoryRevision, mutation: s.projectHistoryMutation, load: s.loadProjectHistoryCatalog, hydrate: s.hydrateProjectHistoryRow})
	}
	return s.projectHistory
}

// Decode only metadata while building the catalog. Disk validation and workspace
// availability are deliberately deferred to the bounded page hydration below.
func projectHistoryMetadata(raw json.RawMessage, path, project, id string) (sessionInfo, error) {
	var info sessionInfo
	if e := json.Unmarshal(raw, &info); e != nil {
		return info, e
	}
	info.ID = id
	info.ProjectPath = project
	info.Path = path
	info.Metadata = append(json.RawMessage(nil), raw...)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	info.workDirNull = string(fields["workDir"]) == "null"
	info.teamWorker = string(fields["isTeamWorker"]) == "true"
	return info, nil
}
func (s *Server) historyCatalogFromFiles(ctx context.Context, only map[string]bool) ([]sessionInfo, error) {
	rows := []sessionInfo{}
	root := filepath.Join(s.options.ConfigDir, "projects")
	visit := func(path string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		project := filepath.Base(filepath.Dir(path))
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		projection, e := s.projection.Get(path, project)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		info, e := projectHistoryMetadata(projection.Summary, path, project, id)
		if e == nil && !info.teamWorker {
			rows = append(rows, info)
		}
		return nil
	}
	if only != nil {
		for path := range only {
			if filepath.Dir(filepath.Dir(path)) == root {
				if e := visit(path); e != nil {
					return nil, e
				}
			}
		}
		return rows, nil
	}
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) && path == root {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && filepath.Dir(path) != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Dir(filepath.Dir(path)) != root || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		return visit(path)
	})
	return rows, e
}
func (s *Server) historyCatalogFromIndex(ctx context.Context) ([]sessionInfo, bool, error) {
	i := s.options.LocalIndex
	if i == nil || i.Mode() != "on" || !i.ConsumerReadAllowed() {
		return nil, false, nil
	}
	state := i.Status().State
	var excluded map[string]bool
	if state == "degraded" {
		paths := i.SourceScopedFailurePaths()
		if paths == nil {
			return nil, false, nil
		}
		excluded = map[string]bool{}
		for _, path := range paths {
			excluded[filepath.Clean(path)] = true
		}
	} else if s.usableSessionIndex() != "on" {
		return nil, false, nil
	}
	epoch := i.Epoch()
	rows := []sessionInfo{}
	size := float64(500)
	for offset := float64(0); ; offset += 500 {
		page, e := i.List(ctx, localindex.SessionListOptions{Limit: &size, Offset: &offset})
		if e != nil || page == nil {
			i.NoteConsumerFailure()
			return nil, false, nil
		}
		status := i.Status()
		if status.State == "off" || status.State == "degraded" && i.SourceScopedFailurePaths() == nil {
			return nil, false, nil
		}
		for _, row := range page.Sessions {
			path := indexedValue(row, "transcriptPath")
			if excluded[filepath.Clean(path)] {
				continue
			}
			raw, e := row.MarshalJSON()
			if e != nil {
				return nil, false, e
			}
			info, e := projectHistoryMetadata(raw, path, indexedValue(row, "projectPath"), indexedValue(row, "id"))
			if e != nil {
				return nil, false, e
			}
			rows = append(rows, info)
		}
		if int64(offset)+int64(len(page.Sessions)) >= page.Total || len(page.Sessions) == 0 {
			break
		}
	}
	if excluded != nil {
		disk, e := s.historyCatalogFromFiles(ctx, excluded)
		if e != nil {
			return nil, false, e
		}
		rows = append(rows, disk...)
	}
	if epoch != i.Epoch() || !i.ConsumerReadAllowed() {
		return nil, false, nil
	}
	return rows, true, nil
}
func (s *Server) loadProjectHistoryCatalog(ctx context.Context) ([]projectHistoryRow, error) {
	infos, ok, e := s.historyCatalogFromIndex(ctx)
	if e != nil {
		return nil, e
	}
	if !ok {
		// Source never scans every transcript to fill a still-usable on index.
		if s.usableSessionIndex() == "on" {
			return []projectHistoryRow{}, nil
		}
		infos, e = s.historyCatalogFromFiles(ctx, nil)
		if e != nil {
			return nil, e
		}
	}
	roots := map[string]*string{}
	rows := []projectHistoryRow{}
	for _, info := range infos {
		var metadata map[string]json.RawMessage
		_ = json.Unmarshal(info.Metadata, &metadata)
		var worktree, repository map[string]json.RawMessage
		_ = json.Unmarshal(metadata["worktreeSession"], &worktree)
		_ = json.Unmarshal(metadata["repository"], &repository)
		candidate := stringField(worktree, "originalCwd")
		if candidate == "" {
			candidate = stringField(repository, "repoRoot")
		}
		if candidate == "" {
			candidate = info.WorkDir
		}
		if candidate == "" {
			candidate = desanitizeSessionProject(info.ProjectPath)
		}
		key := candidate + "\x00" + strconv.FormatBool(info.WorkDir != "")
		root, found := roots[key]
		if !found {
			root = logicalProjectRootWith(info.WorkDir, metadata, s.findCachedCanonicalGitRoot)
			if root == nil {
				root = logicalProjectRootWith(desanitizeSessionProject(info.ProjectPath), metadata, s.findCachedCanonicalGitRoot)
			}
			roots[key] = root
		}
		value := info.WorkDir
		if root != nil {
			value = *root
		}
		if value == "" {
			value = info.ProjectPath
		}
		if value == "" {
			value = "unknown"
		}
		rows = append(rows, projectHistoryRow{info: info, root: value})
	}
	return rows, nil
}
func (s *Server) hydrateProjectHistoryRow(ctx context.Context, row projectHistoryRow) (*sessionInfo, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	root, e := filepath.EvalSymlinks(filepath.Join(s.options.ConfigDir, "projects"))
	if e == nil {
		_, e = s.validateIndexedSessionPath(row.info.Path, row.info.ProjectPath, row.info.ID, root)
	}
	if errors.Is(e, fs.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		if s.options.LocalIndex != nil {
			s.options.LocalIndex.NoteConsumerFailure()
		}
		return nil, projectHistoryChanged("Project history changed; retry from the first page")
	}
	var metadata protocol.JSONObject
	_ = json.Unmarshal(row.info.Metadata, &metadata)
	for key, value := range map[string]string{"id": row.info.ID, "projectPath": row.info.ProjectPath, "transcriptPath": row.info.Path} {
		raw, _ := json.Marshal(value)
		metadata.Set(key, raw)
	}
	returnInfo := s.hydrateIndexedSession(metadata)
	returnInfo.ID = row.info.ID
	returnInfo.ProjectPath = row.info.ProjectPath
	returnInfo.Path = row.info.Path
	return &returnInfo, nil
}
