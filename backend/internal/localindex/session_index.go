package localindex

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"math"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SessionSource struct {
	Path          string  `json:"path"`
	Size          int64   `json:"size"`
	MtimeMs       float64 `json:"mtimeMs"`
	FileIdentity  *string `json:"fileIdentity"`
	Fingerprint   string  `json:"fingerprint"`
	IndexedBytes  int64   `json:"indexedBytes"`
	ParserVersion int64   `json:"parserVersion"`
	State         string  `json:"state"`
	LastErrorCode *string `json:"lastErrorCode"`
	UpdatedAtMs   int64   `json:"updatedAtMs"`
}
type Backfill struct {
	Scope         string  `json:"scope"`
	State         string  `json:"state"`
	Watermark     *string `json:"watermark"`
	Discovered    int64   `json:"discovered"`
	Indexed       int64   `json:"indexed"`
	Degraded      int64   `json:"degraded"`
	LastErrorCode *string `json:"lastErrorCode"`
	UpdatedAtMs   int64   `json:"updatedAtMs"`
}
type SessionPage struct {
	Sessions []protocol.JSONObject `json:"sessions"`
	Total    int64                 `json:"total"`
}
type SessionListOptions struct {
	Project       *string
	Limit, Offset *float64
}
type SessionFileMatch struct {
	FilePath   string `json:"filePath"`
	ProjectDir string `json:"projectDir"`
}
type EntryLocator struct {
	Ordinal         int64   `json:"ordinal"`
	JSONLLine       int64   `json:"jsonlLine"`
	ByteStart       int64   `json:"byteStart"`
	ByteLength      int64   `json:"byteLength"`
	EntryType       string  `json:"entryType"`
	MessageID       *string `json:"messageId"`
	Role            *string `json:"role"`
	Timestamp       *string `json:"timestamp"`
	ParentToolUseID *string `json:"parentToolUseId"`
}
type LocatorPage struct {
	Source  SessionSource  `json:"source"`
	Entries []EntryLocator `json:"entries"`
}
type SessionIndex struct{ db *Database }

func NewSessionIndex(db *Database) (*SessionIndex, error) {
	if db == nil || db.kind != Primary {
		return nil, errors.New("Session index requires its primary native database")
	}
	return &SessionIndex{db}, nil
}
func bounded(v *float64, fallback float64) float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return fallback
	}
	return max(0, math.Trunc(*v))
}
func jsTruthy(v json.RawMessage) bool {
	if len(v) == 0 || string(v) == "null" || string(v) == "false" {
		return false
	}
	if v[0] == '"' {
		return stringValue(v) != ""
	}
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return n != 0
	}
	return true
}
func parseStored(v json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(v, &s) != nil || !json.Valid([]byte(s)) {
		return nil
	}
	return protocol.StringifyValidatedJavaScriptJSON([]byte(s))
}
func indexedSession(r protocol.JSONObject) protocol.JSONObject {
	out := protocol.NewJSONObject()
	for _, f := range [][2]string{{"transcriptPath", "transcript_path"}, {"id", "session_id"}, {"title", "title"}, {"createdAt", "created_at"}, {"modifiedAt", "modified_at"}, {"messageCount", "message_count"}, {"projectPath", "project_path"}, {"workDir", "work_dir"}} {
		out.Set(f[0], r.Get(f[1]))
	}
	for _, f := range [][2]string{{"permissionMode", "permission_mode"}, {"runtimeModelId", "runtime_model_id"}, {"effortLevel", "effort_level"}} {
		if jsTruthy(r.Get(f[1])) {
			out.Set(f[0], r.Get(f[1]))
		}
	}
	if intValue(r.Get("runtime_provider_present")) == 1 {
		out.Set("runtimeProviderId", r.Get("runtime_provider_id"))
	}
	repository := parseStored(r.Get("repository_json"))
	if jsTruthy(repository) {
		out.Set("repository", repository)
	}
	if string(r.Get("worktree_session_json")) != "null" {
		if worktree := parseStored(r.Get("worktree_session_json")); worktree != nil {
			out.Set("worktreeSession", worktree)
		}
	}
	return out
}
func sessionSourceRow(r protocol.JSONObject) SessionSource {
	return SessionSource{stringValue(r.Get("path")), intValue(r.Get("size_bytes")), numberValue(r.Get("mtime_ms")), nullableValue(r.Get("file_identity")), stringValue(r.Get("prefix_hash")), intValue(r.Get("indexed_bytes")), intValue(r.Get("parser_version")), stringValue(r.Get("state")), nullableValue(r.Get("last_error_code")), intValue(r.Get("updated_at_ms"))}
}
func (i *SessionIndex) List(ctx context.Context, o SessionListOptions) (SessionPage, error) {
	out := SessionPage{Sessions: []protocol.JSONObject{}}
	limit, offset := bounded(o.Limit, 50), bounded(o.Offset, 0)
	where := "is_team_worker = 0"
	bindings := []any{}
	if o.Project != nil {
		where += " AND project_path = ?"
		bindings = append(bindings, *o.Project)
	}
	err := i.db.Read(ctx, func(op *Operation) error {
		total, e := op.Get("SELECT COUNT(*) AS total FROM sessions WHERE "+where, bindings...)
		if e != nil {
			return e
		}
		if total != nil {
			out.Total = intValue(total.Get("total"))
		}
		rows, e := op.All("SELECT * FROM sessions WHERE "+where+" ORDER BY modified_at_ms DESC,session_id ASC,transcript_path ASC LIMIT ? OFFSET ?", append(bindings, limit, offset)...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Sessions = append(out.Sessions, indexedSession(r))
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) Get(ctx context.Context, id string) (*protocol.JSONObject, error) {
	var result *protocol.JSONObject
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM sessions WHERE session_id = ? ORDER BY modified_at_ms DESC,transcript_path ASC LIMIT 1", id)
		if r != nil {
			out := indexedSession(*r)
			result = &out
		}
		return e
	})
	return result, err
}
func (i *SessionIndex) SuggestionMetadata(ctx context.Context, ids []string) ([]protocol.JSONObject, error) {
	unique := []any{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
		if len(unique) == 100 {
			break
		}
	}
	out := []protocol.JSONObject{}
	if len(unique) == 0 {
		return out, nil
	}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT * FROM sessions WHERE session_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")+") ORDER BY modified_at_ms DESC,session_id ASC,transcript_path ASC LIMIT 100", unique...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, indexedSession(r))
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) FindFiles(ctx context.Context, id string) ([]SessionFileMatch, error) {
	out := []SessionFileMatch{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT sessions.transcript_path,sessions.project_path FROM sessions JOIN source_files ON source_files.path=sessions.transcript_path WHERE sessions.session_id=? ORDER BY source_files.mtime_ms DESC,sessions.transcript_path ASC", id)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, SessionFileMatch{stringValue(r.Get("transcript_path")), stringValue(r.Get("project_path"))})
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) UpdateTitle(ctx context.Context, id, title string) (bool, error) {
	changed := false
	err := i.db.Write(ctx, func(op *Operation) error {
		r, e := op.Run("UPDATE sessions SET title = ? WHERE session_id = ?", title, id)
		changed = r.Changes > 0
		return e
	})
	return changed, err
}
func (i *SessionIndex) SearchMetadata(ctx context.Context, query string, limitOption, offsetOption *float64) (SessionPage, error) {
	needle := NormalizeContent(trimJS(query))
	limit := min(100.0, max(1.0, bounded(limitOption, 30)))
	offset := bounded(offsetOption, 0)
	out := SessionPage{Sessions: []protocol.JSONObject{}}
	nonASCII := false
	for _, r := range needle {
		if r > 127 && NormalizeContent(string(r)) != cases.Upper(language.Und).String(string(r)) {
			nonASCII = true
			break
		}
	}
	err := i.db.Read(ctx, func(op *Operation) error {
		if nonASCII {
			return searchUnicodeMetadata(op, needle, limit, offset, &out)
		}
		lower := func(column string) string { return "replace(replace(lower(" + column + "),'K','k'),'İ','i̇')" }
		title, id := lower("title"), lower("session_id")
		where := "instr(" + title + ",?)>0 OR instr(" + id + ",?)>0 OR instr(" + lower("coalesce(work_dir,'')") + ",?)>0 OR instr(" + lower("project_path") + ",?)>0"
		r, e := op.Get("SELECT COUNT(*) AS total FROM sessions WHERE "+where, needle, needle, needle, needle)
		if e != nil {
			return e
		}
		if r != nil {
			out.Total = intValue(r.Get("total"))
		}
		rows, e := op.All("SELECT * FROM sessions WHERE "+where+" ORDER BY CASE WHEN ?='' THEN 0 WHEN "+title+"=? OR "+id+"=? THEN 3 WHEN instr("+title+",?)=1 OR instr("+id+",?)=1 THEN 2 WHEN instr("+title+",?)>0 OR instr("+id+",?)>0 THEN 1 ELSE 0 END DESC,modified_at_ms DESC,session_id ASC,transcript_path ASC LIMIT ? OFFSET ?", needle, needle, needle, needle, needle, needle, needle, needle, needle, needle, needle, limit, offset)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Sessions = append(out.Sessions, indexedSession(r))
		}
		return nil
	})
	return out, err
}
func searchUnicodeMetadata(op *Operation, needle string, limit, offset float64, out *SessionPage) error {
	buckets := [4][]protocol.JSONObject{}
	var last *protocol.JSONObject
	scanned := 0
	for scanned < 4096 {
		where := ""
		bindings := []any{}
		if last != nil {
			where = "WHERE modified_at_ms < ? OR (modified_at_ms = ? AND session_id > ?) OR (modified_at_ms = ? AND session_id = ? AND transcript_path > ?)"
			ms := numberValue(last.Get("modified_at_ms"))
			id := stringValue(last.Get("session_id"))
			path := stringValue(last.Get("transcript_path"))
			bindings = []any{ms, ms, id, ms, id, path}
		}
		rows, e := op.All("SELECT * FROM sessions "+where+" ORDER BY modified_at_ms DESC,session_id ASC,transcript_path ASC LIMIT 256", bindings...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			names := []string{NormalizeContent(stringValue(r.Get("title"))), NormalizeContent(stringValue(r.Get("session_id")))}
			matched := false
			for _, s := range append(append([]string{}, names...), NormalizeContent(stringValue(r.Get("work_dir"))), NormalizeContent(stringValue(r.Get("project_path")))) {
				matched = matched || strings.Contains(s, needle)
			}
			if matched {
				rank := 0
				for _, s := range names {
					if s == needle {
						rank = max(rank, 3)
					} else if strings.HasPrefix(s, needle) {
						rank = max(rank, 2)
					} else if strings.Contains(s, needle) {
						rank = max(rank, 1)
					}
				}
				buckets[rank] = append(buckets[rank], r)
			}
			scanned++
			if scanned >= 4096 {
				break
			}
		}
		if len(rows) < 256 {
			break
		}
		last = &rows[len(rows)-1]
	}
	ordered := []protocol.JSONObject{}
	for _, rank := range []int{3, 2, 1, 0} {
		ordered = append(ordered, buckets[rank]...)
	}
	out.Total = int64(len(ordered))
	start := int(min(float64(len(ordered)), offset))
	end := int(min(float64(len(ordered)), offset+limit))
	for _, r := range ordered[start:end] {
		out.Sessions = append(out.Sessions, indexedSession(r))
	}
	return nil
}
func (i *SessionIndex) FindCandidates(ctx context.Context, o ContentQueryOptions) ([]protocol.JSONObject, error) {
	where := []string{}
	args := []any{}
	if o.Project != nil {
		where = append(where, "project_path = ?")
		args = append(args, *o.Project)
	}
	if o.ModifiedAfterMs != nil {
		where = append(where, "modified_at_ms >= ?")
		args = append(args, *o.ModifiedAfterMs)
	}
	if o.ModifiedBeforeMs != nil {
		where = append(where, "modified_at_ms <= ?")
		args = append(args, *o.ModifiedBeforeMs)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	out := []protocol.JSONObject{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT transcript_path,session_id,title,modified_at,project_path,work_dir FROM sessions "+clause+" ORDER BY modified_at_ms DESC,session_id ASC,transcript_path ASC", args...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			value := protocol.NewJSONObject()
			for _, f := range [][2]string{{"transcriptPath", "transcript_path"}, {"id", "session_id"}, {"title", "title"}, {"modifiedAt", "modified_at"}, {"projectPath", "project_path"}, {"workDir", "work_dir"}} {
				value.Set(f[0], r.Get(f[1]))
			}
			out = append(out, value)
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) GetSource(ctx context.Context, path string) (*SessionSource, error) {
	var result *SessionSource
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM source_files WHERE path = ?", path)
		if r != nil {
			value := sessionSourceRow(*r)
			result = &value
		}
		return e
	})
	return result, err
}
func (i *SessionIndex) ListSources(ctx context.Context) ([]SessionSource, error) {
	out := []SessionSource{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT * FROM source_files ORDER BY path ASC")
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, sessionSourceRow(r))
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) CountSources(ctx context.Context) (int64, error) {
	var count int64
	err := i.db.Read(ctx, func(op *Operation) error {
		row, e := op.Get("SELECT COUNT(*) AS count FROM source_files")
		if row != nil {
			count = intValue(row.Get("count"))
		}
		return e
	})
	return count, err
}
func (i *SessionIndex) GetBackfill(ctx context.Context, scope string) (*Backfill, error) {
	var out *Backfill
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM backfill_state WHERE scope=?", scope)
		if r != nil {
			value := Backfill{stringValue(r.Get("scope")), stringValue(r.Get("state")), nullableValue(r.Get("watermark")), intValue(r.Get("discovered")), intValue(r.Get("indexed")), intValue(r.Get("degraded")), nullableValue(r.Get("last_error_code")), intValue(r.Get("updated_at_ms"))}
			out = &value
		}
		return e
	})
	return out, err
}
func (i *SessionIndex) Locators(ctx context.Context, path string, types *[]string) (*LocatorPage, error) {
	var out *LocatorPage
	err := i.db.Read(ctx, func(op *Operation) error {
		source, e := op.Get("SELECT * FROM source_files WHERE path=?", path)
		if e != nil || source == nil {
			return e
		}
		out = &LocatorPage{Source: sessionSourceRow(*source), Entries: []EntryLocator{}}
		args := []any{path}
		clause := ""
		if types != nil {
			filtered := []string{}
			seen := map[string]bool{}
			for _, s := range *types {
				if s != "" && !seen[s] {
					filtered = append(filtered, s)
					seen[s] = true
				}
				if len(filtered) == 32 {
					break
				}
			}
			if len(filtered) == 0 {
				return nil
			}
			clause = " AND entry_type IN (" + strings.TrimSuffix(strings.Repeat("?,", len(filtered)), ",") + ")"
			for _, s := range filtered {
				args = append(args, s)
			}
		}
		rows, e := op.All("SELECT * FROM session_entries WHERE transcript_path=?"+clause+" ORDER BY ordinal ASC", args...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Entries = append(out.Entries, EntryLocator{intValue(r.Get("ordinal")), intValue(r.Get("jsonl_line")), intValue(r.Get("byte_start")), intValue(r.Get("byte_length")), stringValue(r.Get("entry_type")), nullableValue(r.Get("message_id")), nullableValue(r.Get("role")), nullableValue(r.Get("timestamp")), nullableValue(r.Get("parent_tool_use_id"))})
		}
		return nil
	})
	return out, err
}
func (i *SessionIndex) ProjectionSeed(ctx context.Context, path string) (*protocol.JSONObject, error) {
	var out *protocol.JSONObject
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT sessions.*,source_files.indexed_bytes,source_files.size_bytes FROM sessions JOIN source_files ON source_files.path=sessions.transcript_path WHERE sessions.transcript_path=?", path)
		if e != nil || r == nil {
			return e
		}
		summary := indexedSession(*r)
		for _, name := range []string{"transcriptPath", "id", "projectPath"} {
			summary.Delete(name)
		}
		value := protocol.NewJSONObject(protocol.JSONField{Name: "summary", Value: raw(summary)}, protocol.JSONField{Name: "indexedBytes", Value: r.Get("indexed_bytes")}, protocol.JSONField{Name: "pendingTailBytes", Value: raw(intValue(r.Get("size_bytes")) - intValue(r.Get("indexed_bytes")))}, protocol.JSONField{Name: "malformedLineCount", Value: raw(0)})
		out = &value
		return nil
	})
	return out, err
}
func sortedKeys[T any](values map[string]T) []string {
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
