package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func (s *Server) suggestionMetadata(ctx context.Context, ids []string) []protocol.JSONObject {
	if s.options.LocalIndex == nil || s.usableSessionIndex() != "on" {
		return nil
	}
	rows, e := s.options.LocalIndex.SuggestionMetadata(ctx, ids[:min(100, len(ids))])
	if e != nil || s.usableSessionIndex() != "on" {
		return nil
	}
	return rows
}
func (s *Server) collaborationTitles(ctx context.Context, ids []string) (protocol.JSONObject, error) {
	titles := protocol.NewJSONObject()
	for _, row := range s.suggestionMetadata(ctx, ids) {
		id, title := row.String("id"), trimJS(row.String("title"))
		if title != "" && (titles.Get(id) == nil || titles.String(id) == "Untitled Session") {
			titles.Set(id, quotedCollaboration(title))
		}
	}
	return titles, nil
}

func (s *Server) searchCollaborationMetadata(ctx context.Context, query string, limit, offset, deadline float64) (protocol.JSONObject, error) {
	if s.options.LocalIndex != nil && s.usableSessionIndex() == "on" {
		epoch := s.options.LocalIndex.Epoch()
		page, e := s.options.LocalIndex.SearchMetadata(ctx, query, &limit, &offset)
		if e == nil && page != nil && epoch == s.options.LocalIndex.Epoch() && s.usableSessionIndex() == "on" {
			return collaborationObject(page), nil
		}
	}
	rows := []protocol.JSONObject{}
	truncated := false
	root := filepath.Join(s.options.ConfigDir, "projects")
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, failure error) error {
		if errors.Is(failure, os.ErrNotExist) && path == root {
			return nil
		}
		if failure != nil {
			return failure
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if float64(time.Now().UnixMilli()) > deadline {
			truncated = true
			return filepath.SkipAll
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
		projection, e := s.projection.Get(path, filepath.Base(filepath.Dir(path)))
		if e != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return nil
		}
		var summary protocol.JSONObject
		_ = summary.UnmarshalJSON(projection.Summary)
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quotedCollaboration(strings.TrimSuffix(entry.Name(), ".jsonl"))})
		for _, key := range []string{"title", "workDir", "modifiedAt"} {
			row.Set(key, summary.Get(key))
		}
		row.Set("projectPath", quotedCollaboration(filepath.Base(filepath.Dir(path))))
		rows = append(rows, row)
		return nil
	})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	lower := cases.Lower(language.Und)
	needle := lower.String(trimJS(query))
	rank := func(row protocol.JSONObject) int {
		if needle == "" {
			return 0
		}
		names := []string{lower.String(row.String("title")), lower.String(row.String("id"))}
		for _, name := range names {
			if name == needle {
				return 3
			}
		}
		for _, name := range names {
			if strings.HasPrefix(name, needle) {
				return 2
			}
		}
		for _, name := range names {
			if strings.Contains(name, needle) {
				return 1
			}
		}
		return 0
	}
	matches := []protocol.JSONObject{}
	for _, row := range rows {
		for _, key := range []string{"title", "id", "workDir", "projectPath"} {
			if strings.Contains(lower.String(row.String(key)), needle) {
				matches = append(matches, row)
				break
			}
		}
	}
	compare := collate.New(language.English)
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if rank(a) != rank(b) {
			return rank(a) > rank(b)
		}
		if a.String("modifiedAt") != b.String("modifiedAt") {
			return a.String("modifiedAt") > b.String("modifiedAt")
		}
		if c := compare.CompareString(a.String("id"), b.String("id")); c != 0 {
			return c < 0
		}
		return compare.CompareString(a.String("projectPath"), b.String("projectPath")) < 0
	})
	start := int(min(float64(len(matches)), offset))
	end := int(min(float64(len(matches)), float64(start)+limit))
	out := collaborationObject(map[string]any{"sessions": matches[start:end], "total": len(matches)})
	if truncated {
		out.Set("truncated", json.RawMessage("true"))
	}
	return out, nil
}

func (s *Server) collaborationList(ctx context.Context, options protocol.JSONObject) (protocol.JSONObject, error) {
	query := trimJS(options.String("query"))
	var limit, offset float64
	_ = json.Unmarshal(options.Get("limit"), &limit)
	_ = json.Unmarshal(options.Get("offset"), &offset)
	deadline := float64(time.Now().UnixMilli() + 300)
	metadata, e := s.searchCollaborationMetadata(ctx, query, limit, offset, deadline)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if ctx.Err() != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	if query == "" {
		return metadata, nil
	}
	var sessions []protocol.JSONObject
	_ = json.Unmarshal(metadata.Get("sessions"), &sessions)
	if string(metadata.Get("truncated")) == "true" || float64(len(sessions)) == limit || float64(time.Now().UnixMilli()) > deadline {
		metadata.Set("truncated", json.RawMessage("true"))
		metadata.Set("totalIsLowerBound", json.RawMessage("true"))
		return metadata, nil
	}
	if s.options.Search == nil {
		return protocol.JSONObject{}, unsupported("actual collaboration full-text suggestions")
	}
	hundred := float64(100)
	content, e := s.options.Search.Suggest(ctx, query, &hundred, &deadline)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	ids := []string{}
	for _, item := range content.Sessions {
		ids = append(ids, item.SessionID)
	}
	details := map[string]protocol.JSONObject{}
	for _, row := range s.suggestionMetadata(ctx, ids) {
		details[row.String("id")] = row
	}
	lower := cases.Lower(language.Und)
	needle := lower.String(query)
	seen := map[string]bool{}
	bodyOnly := []protocol.JSONObject{}
	for _, item := range content.Sessions {
		if seen[item.SessionID] {
			continue
		}
		seen[item.SessionID] = true
		detail, exists := details[item.SessionID]
		matched := false
		if exists {
			for _, key := range []string{"id", "title", "workDir", "projectPath"} {
				if strings.Contains(lower.String(detail.String(key)), needle) {
					matched = true
					break
				}
			}
		}
		if matched {
			continue
		}
		if !exists {
			detail = collaborationObject(map[string]any{"id": item.SessionID, "title": item.SessionID, "projectPath": item.ProjectPath, "modifiedAt": item.ModifiedAt})
		}
		bodyOnly = append(bodyOnly, detail)
	}
	var total float64
	_ = json.Unmarshal(metadata.Get("total"), &total)
	start := int(min(float64(len(bodyOnly)), max(0, offset-total)))
	end := int(min(float64(len(bodyOnly)), float64(start)+limit-float64(len(sessions))))
	sessions = append(sessions, bodyOnly[start:end]...)
	return collaborationObject(map[string]any{"sessions": sessions, "total": total + float64(len(bodyOnly)), "truncated": content.Truncated, "totalIsLowerBound": content.Truncated || content.IndexUnavailable, "indexUnavailable": content.IndexUnavailable}), nil
}
