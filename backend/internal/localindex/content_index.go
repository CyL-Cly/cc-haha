package localindex

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type ContentSource struct {
	Path                string  `json:"path"`
	ProjectPath         string  `json:"projectPath"`
	OwnerSessionID      string  `json:"ownerSessionId"`
	OwnerTranscriptPath string  `json:"ownerTranscriptPath"`
	ModifiedAtMs        float64 `json:"modifiedAtMs"`
	SizeBytes           int64   `json:"sizeBytes"`
	MtimeMs             float64 `json:"mtimeMs"`
	FileIdentity        *string `json:"fileIdentity"`
	Fingerprint         string  `json:"fingerprint"`
	IndexedBytes        int64   `json:"indexedBytes"`
	IndexedLines        int64   `json:"indexedLines"`
	ParserVersion       int64   `json:"parserVersion"`
	State               string  `json:"state"`
	LastErrorCode       *string `json:"lastErrorCode"`
	UpdatedAtMs         int64   `json:"updatedAtMs"`
}
type ContentDocument struct {
	JSONLLine      int64   `json:"jsonlLine"`
	ByteStart      int64   `json:"byteStart"`
	ByteLength     int64   `json:"byteLength"`
	SegmentIndex   int64   `json:"segmentIndex"`
	Role           string  `json:"role"`
	MessageID      *string `json:"messageId"`
	Timestamp      *string `json:"timestamp"`
	Body           string  `json:"body"`
	NormalizedBody string  `json:"normalizedBody"`
}
type ContentReadiness struct {
	Scope         string  `json:"scope"`
	State         string  `json:"state"`
	Generation    int64   `json:"generation"`
	Discovered    int64   `json:"discovered"`
	Indexed       int64   `json:"indexed"`
	Degraded      int64   `json:"degraded"`
	LastErrorCode *string `json:"lastErrorCode"`
	UpdatedAtMs   int64   `json:"updatedAtMs"`
}
type ContentReadinessWrite struct {
	State         string  `json:"state"`
	Generation    *int64  `json:"generation,omitempty"`
	Discovered    int64   `json:"discovered"`
	Indexed       int64   `json:"indexed"`
	Degraded      int64   `json:"degraded"`
	LastErrorCode *string `json:"lastErrorCode"`
	UpdatedAtMs   *int64  `json:"updatedAtMs,omitempty"`
}
type ContentQueryOptions struct {
	Project           *string  `json:"project,omitempty"`
	ModifiedAfterMs   *float64 `json:"modifiedAfterMs,omitempty"`
	ModifiedBeforeMs  *float64 `json:"modifiedBeforeMs,omitempty"`
	Limit             *float64 `json:"limit,omitempty"`
	MatchesPerSession *float64 `json:"matchesPerSession,omitempty"`
	CaseSensitive     bool     `json:"caseSensitive,omitempty"`
}
type ContentMatch struct {
	SourcePath          string  `json:"sourcePath"`
	OwnerSessionID      string  `json:"ownerSessionId"`
	OwnerTranscriptPath string  `json:"ownerTranscriptPath"`
	ProjectPath         string  `json:"projectPath"`
	ModifiedAtMs        float64 `json:"modifiedAtMs"`
	ByteStart           int64   `json:"byteStart"`
	ByteLength          int64   `json:"byteLength"`
	SourceSizeBytes     int64   `json:"sourceSizeBytes"`
	SourceMtimeMs       float64 `json:"sourceMtimeMs"`
	SourceFileIdentity  *string `json:"sourceFileIdentity"`
	SourceFingerprint   string  `json:"sourceFingerprint"`
	SourceIndexedBytes  int64   `json:"sourceIndexedBytes"`
	SourceParserVersion int64   `json:"sourceParserVersion"`
	LineNumber          int64   `json:"lineNumber"`
	SegmentIndex        int64   `json:"segmentIndex"`
	Role                string  `json:"role"`
	MessageID           *string `json:"messageId"`
	Timestamp           *string `json:"timestamp"`
	Body                string  `json:"body"`
}
type ContentSessionMatches struct {
	OwnerSessionID      string         `json:"ownerSessionId"`
	OwnerTranscriptPath string         `json:"ownerTranscriptPath"`
	ProjectPath         string         `json:"projectPath"`
	ModifiedAtMs        float64        `json:"modifiedAtMs"`
	MatchCount          int64          `json:"matchCount"`
	Matches             []ContentMatch `json:"matches"`
}
type ContentQueryResult struct {
	Sessions  []ContentSessionMatches `json:"sessions"`
	Truncated bool                    `json:"truncated"`
}
type ContentSuggestion struct {
	OwnerSessionID      string  `json:"ownerSessionId"`
	OwnerTranscriptPath string  `json:"ownerTranscriptPath"`
	ProjectPath         string  `json:"projectPath"`
	ModifiedAtMs        float64 `json:"modifiedAtMs"`
}
type ContentSuggestions struct {
	Sessions  []ContentSuggestion `json:"sessions"`
	Truncated bool                `json:"truncated"`
}
type ContentIndex struct {
	db    *Database
	scope string
	now   func() time.Time
}

func NewContentIndex(db *Database, scope string, now func() time.Time) (*ContentIndex, error) {
	if db == nil || db.kind != Content || scope == "" {
		return nil, errors.New("Content index requires its search database and explicit scope")
	}
	if now == nil {
		now = time.Now
	}
	return &ContentIndex{db, scope, now}, nil
}
func NormalizeContent(s string) string { return cases.Lower(language.Und).String(s) }
func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func clamp(v *float64, fallback, maximum int) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return fallback
	}
	return max(1, min(maximum, int(math.Trunc(*v))))
}
func phrase(s string) string                { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func stringValue(v json.RawMessage) string  { var s string; _ = json.Unmarshal(v, &s); return s }
func numberValue(v json.RawMessage) float64 { var n float64; _ = json.Unmarshal(v, &n); return n }
func nullableValue(v json.RawMessage) *string {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	s := stringValue(v)
	return &s
}
func sourceRow(r protocol.JSONObject) ContentSource {
	return ContentSource{stringValue(r.Get("path")), stringValue(r.Get("project_path")), stringValue(r.Get("owner_session_id")), stringValue(r.Get("owner_transcript_path")), numberValue(r.Get("modified_at_ms")), intValue(r.Get("size_bytes")), numberValue(r.Get("mtime_ms")), nullableValue(r.Get("file_identity")), stringValue(r.Get("fingerprint")), intValue(r.Get("indexed_bytes")), intValue(r.Get("indexed_lines")), intValue(r.Get("parser_version")), stringValue(r.Get("state")), nullableValue(r.Get("last_error_code")), intValue(r.Get("updated_at_ms"))}
}
func readinessRow(r protocol.JSONObject) ContentReadiness {
	return ContentReadiness{stringValue(r.Get("scope")), stringValue(r.Get("state")), intValue(r.Get("generation")), intValue(r.Get("discovered")), intValue(r.Get("indexed")), intValue(r.Get("degraded")), nullableValue(r.Get("last_error_code")), intValue(r.Get("updated_at_ms"))}
}
func (i *ContentIndex) GetSource(ctx context.Context, path string) (*ContentSource, error) {
	var result *ContentSource
	err := i.db.Read(ctx, func(op *Operation) error {
		row, e := op.Get("SELECT * FROM search_sources WHERE path = ?", path)
		if row != nil {
			value := sourceRow(*row)
			result = &value
		}
		return e
	})
	return result, err
}
func (i *ContentIndex) ListSources(ctx context.Context) ([]ContentSource, error) {
	out := []ContentSource{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT * FROM search_sources ORDER BY path")
		for _, r := range rows {
			out = append(out, sourceRow(r))
		}
		return e
	})
	return out, err
}
func (i *ContentIndex) CountSources(ctx context.Context) (int64, error) {
	var count int64
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT COUNT(*) AS total FROM search_sources")
		if r != nil {
			count = intValue(r.Get("total"))
		}
		return e
	})
	return count, err
}
func upsertContentSource(op *Operation, s ContentSource) error {
	return op.Exec(`INSERT INTO search_sources(path,project_path,owner_session_id,owner_transcript_path,modified_at_ms,size_bytes,mtime_ms,file_identity,fingerprint,indexed_bytes,indexed_lines,parser_version,state,last_error_code,updated_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET project_path=excluded.project_path,owner_session_id=excluded.owner_session_id,owner_transcript_path=excluded.owner_transcript_path,modified_at_ms=excluded.modified_at_ms,size_bytes=excluded.size_bytes,mtime_ms=excluded.mtime_ms,file_identity=excluded.file_identity,fingerprint=excluded.fingerprint,indexed_bytes=excluded.indexed_bytes,indexed_lines=excluded.indexed_lines,parser_version=excluded.parser_version,state=excluded.state,last_error_code=excluded.last_error_code,updated_at_ms=excluded.updated_at_ms`, s.Path, s.ProjectPath, s.OwnerSessionID, s.OwnerTranscriptPath, s.ModifiedAtMs, s.SizeBytes, s.MtimeMs, s.FileIdentity, s.Fingerprint, s.IndexedBytes, s.IndexedLines, s.ParserVersion, s.State, s.LastErrorCode, s.UpdatedAtMs)
}
func insertContentDocument(op *Operation, path string, d ContentDocument) error {
	return op.Exec(`INSERT INTO search_documents(source_path,jsonl_line,byte_start,byte_length,segment_index,role,message_id,timestamp,body,normalized_body) VALUES(?,?,?,?,?,?,?,?,?,?)`, path, d.JSONLLine, d.ByteStart, d.ByteLength, d.SegmentIndex, d.Role, d.MessageID, d.Timestamp, d.Body, d.NormalizedBody)
}

// ApplySource accepts a bounded iterator so projection never retains an entire transcript.
func (i *ContentIndex) ApplySource(ctx context.Context, s ContentSource, appendOnly bool, documents func(func(ContentDocument) error) error) error {
	return i.db.Transaction(ctx, func(op *Operation) error {
		if appendOnly {
			r, e := op.Get("SELECT path FROM search_sources WHERE path = ?", s.Path)
			if e != nil {
				return e
			}
			if r == nil {
				return errors.New("Cannot append an unindexed search source")
			}
		}
		if e := upsertContentSource(op, s); e != nil {
			return e
		}
		if !appendOnly {
			if e := op.Exec("DELETE FROM search_documents WHERE source_path = ?", s.Path); e != nil {
				return e
			}
		}
		if documents == nil {
			return nil
		}
		return documents(func(d ContentDocument) error { return insertContentDocument(op, s.Path, d) })
	})
}
func (i *ContentIndex) DeleteSource(ctx context.Context, path string) error {
	return i.db.Transaction(ctx, func(op *Operation) error { return op.Exec("DELETE FROM search_sources WHERE path = ?", path) })
}
func (i *ContentIndex) GetReadiness(ctx context.Context) (*ContentReadiness, error) {
	var result *ContentReadiness
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM search_backfill_state WHERE scope = ?", i.scope)
		if r != nil {
			value := readinessRow(*r)
			result = &value
		}
		return e
	})
	return result, err
}
func (i *ContentIndex) SetReadiness(ctx context.Context, r ContentReadinessWrite) error {
	return i.db.Transaction(ctx, func(op *Operation) error {
		generation := int64(0)
		if r.Generation != nil {
			generation = *r.Generation
		} else {
			current, e := op.Get("SELECT generation FROM search_backfill_state WHERE scope = ?", i.scope)
			if e != nil {
				return e
			}
			if current != nil {
				generation = intValue(current.Get("generation"))
			}
		}
		updated := i.now().UnixMilli()
		if r.UpdatedAtMs != nil {
			updated = *r.UpdatedAtMs
		}
		return op.Exec(`INSERT INTO search_backfill_state(scope,state,generation,discovered,indexed,degraded,last_error_code,updated_at_ms) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET state=excluded.state,generation=excluded.generation,discovered=excluded.discovered,indexed=excluded.indexed,degraded=excluded.degraded,last_error_code=excluded.last_error_code,updated_at_ms=excluded.updated_at_ms`, i.scope, r.State, generation, r.Discovered, r.Indexed, r.Degraded, r.LastErrorCode, updated)
	})
}
func (i *ContentIndex) Suggest(ctx context.Context, query string, requestedLimit *float64) (*ContentSuggestions, error) {
	normalized := NormalizeContent(trimJS(query))
	out := &ContentSuggestions{Sessions: []ContentSuggestion{}}
	if normalized == "" {
		return out, nil
	}
	ready, e := i.GetReadiness(ctx)
	if e != nil || ready == nil || ready.State != "ready" {
		return nil, e
	}
	limit := clamp(requestedLimit, 50, 100)
	useFTS := utf8.RuneCountInString(normalized) >= 3
	selection := `FROM search_sources AS source WHERE source.state='ready' AND EXISTS(SELECT 1 FROM search_documents AS document WHERE document.source_path=source.path AND instr(document.normalized_body,?)>0)`
	bindings := []any{normalized, limit + 1}
	if useFTS {
		selection = `FROM search_documents_fts JOIN search_documents AS document ON document.id=search_documents_fts.rowid JOIN search_sources AS source ON source.path=document.source_path WHERE search_documents_fts MATCH ? AND source.state='ready' AND instr(document.normalized_body,?)>0`
		bindings = []any{phrase(normalized), normalized, limit + 1}
	}
	e = i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All(`SELECT source.owner_session_id,source.owner_transcript_path,source.project_path,MAX(source.modified_at_ms) AS modified_at_ms `+selection+` GROUP BY source.owner_transcript_path ORDER BY modified_at_ms DESC,source.owner_transcript_path LIMIT ?`, bindings...)
		if e != nil {
			return e
		}
		out.Truncated = len(rows) > limit
		for _, r := range rows[:min(limit, len(rows))] {
			out.Sessions = append(out.Sessions, ContentSuggestion{stringValue(r.Get("owner_session_id")), stringValue(r.Get("owner_transcript_path")), stringValue(r.Get("project_path")), numberValue(r.Get("modified_at_ms"))})
		}
		return nil
	})
	return out, e
}
func (i *ContentIndex) Query(ctx context.Context, query string, o ContentQueryOptions) (*ContentQueryResult, error) {
	literal := trimJS(query)
	normalized := NormalizeContent(literal)
	out := &ContentQueryResult{Sessions: []ContentSessionMatches{}}
	if normalized == "" {
		return out, nil
	}
	ready, e := i.GetReadiness(ctx)
	if e != nil || ready == nil || ready.State != "ready" {
		return nil, e
	}
	limit := clamp(o.Limit, 50, 100)
	matches := clamp(o.MatchesPerSession, 5, 20)
	filters := []string{"source.state='ready'"}
	bindings := []any{}
	useFTS := utf8.RuneCountInString(normalized) >= 3
	from := `search_documents AS document JOIN search_sources AS source ON source.path=document.source_path`
	if useFTS {
		from = `search_documents_fts JOIN search_documents AS document ON document.id=search_documents_fts.rowid JOIN search_sources AS source ON source.path=document.source_path`
		filters = append(filters, "search_documents_fts MATCH ?")
		bindings = append(bindings, phrase(normalized))
	}
	if o.CaseSensitive {
		filters = append(filters, "instr(document.body,?)>0")
		bindings = append(bindings, literal)
	} else {
		filters = append(filters, "instr(document.normalized_body,?)>0")
		bindings = append(bindings, normalized)
	}
	if o.Project != nil {
		filters = append(filters, "source.project_path=?")
		bindings = append(bindings, *o.Project)
	}
	if o.ModifiedAfterMs != nil {
		filters = append(filters, "source.modified_at_ms>=?")
		bindings = append(bindings, *o.ModifiedAfterMs)
	}
	if o.ModifiedBeforeMs != nil {
		filters = append(filters, "source.modified_at_ms<=?")
		bindings = append(bindings, *o.ModifiedBeforeMs)
	}
	bindings = append(bindings, limit+1, matches)
	sql := `WITH matched AS MATERIALIZED(SELECT document.id,document.source_path,source.owner_session_id,source.owner_transcript_path,source.project_path,source.modified_at_ms,source.size_bytes AS source_size_bytes,source.mtime_ms AS source_mtime_ms,source.file_identity AS source_file_identity,source.fingerprint AS source_fingerprint,source.indexed_bytes AS source_indexed_bytes,source.parser_version AS source_parser_version,document.jsonl_line,document.byte_start,document.byte_length,document.segment_index,document.role,document.message_id,document.timestamp,document.body FROM ` + from + ` WHERE ` + strings.Join(filters, " AND ") + `),owner_counts AS(SELECT owner_transcript_path,MAX(modified_at_ms) AS owner_modified_at_ms,COUNT(*) AS owner_match_count FROM matched GROUP BY owner_transcript_path),limited_owners AS(SELECT owner_transcript_path,owner_match_count,ROW_NUMBER() OVER(ORDER BY owner_modified_at_ms DESC,owner_transcript_path) AS owner_rank FROM owner_counts ORDER BY owner_modified_at_ms DESC,owner_transcript_path LIMIT ?),ranked_matches AS(SELECT matched.*,ROW_NUMBER() OVER(PARTITION BY matched.owner_transcript_path ORDER BY matched.modified_at_ms DESC,matched.source_path,matched.jsonl_line,matched.segment_index) AS match_rank FROM matched JOIN limited_owners USING(owner_transcript_path)) SELECT limited_owners.owner_rank,limited_owners.owner_match_count,ranked_matches.* FROM ranked_matches JOIN limited_owners USING(owner_transcript_path) WHERE ranked_matches.match_rank<=? ORDER BY limited_owners.owner_rank,ranked_matches.match_rank`
	e = i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All(sql, bindings...)
		if e != nil {
			return e
		}
		var lastRank int64 = -1
		for _, r := range rows {
			rank := intValue(r.Get("owner_rank"))
			if rank != lastRank {
				out.Sessions = append(out.Sessions, ContentSessionMatches{stringValue(r.Get("owner_session_id")), stringValue(r.Get("owner_transcript_path")), stringValue(r.Get("project_path")), numberValue(r.Get("modified_at_ms")), intValue(r.Get("owner_match_count")), []ContentMatch{}})
				lastRank = rank
			}
			n := len(out.Sessions) - 1
			out.Sessions[n].Matches = append(out.Sessions[n].Matches, ContentMatch{stringValue(r.Get("source_path")), stringValue(r.Get("owner_session_id")), stringValue(r.Get("owner_transcript_path")), stringValue(r.Get("project_path")), numberValue(r.Get("modified_at_ms")), intValue(r.Get("byte_start")), intValue(r.Get("byte_length")), intValue(r.Get("source_size_bytes")), numberValue(r.Get("source_mtime_ms")), nullableValue(r.Get("source_file_identity")), stringValue(r.Get("source_fingerprint")), intValue(r.Get("source_indexed_bytes")), intValue(r.Get("source_parser_version")), intValue(r.Get("jsonl_line")), intValue(r.Get("segment_index")), stringValue(r.Get("role")), nullableValue(r.Get("message_id")), nullableValue(r.Get("timestamp")), stringValue(r.Get("body"))})
		}
		return nil
	})
	out.Truncated = len(out.Sessions) > limit
	out.Sessions = out.Sessions[:min(limit, len(out.Sessions))]
	return out, e
}
