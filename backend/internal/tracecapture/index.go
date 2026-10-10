package tracecapture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SourceInput struct {
	SessionID             string  `json:"sessionId"`
	FilePath              string  `json:"filePath"`
	Size                  int64   `json:"size"`
	MtimeMs               float64 `json:"mtimeMs"`
	IndexedBytes          int64   `json:"indexedBytes"`
	FileIdentity          *string `json:"fileIdentity"`
	Fingerprint           *string `json:"fingerprint"`
	PendingTailBytes      *int64  `json:"pendingTailBytes,omitempty"`
	NextOrdinal           *int64  `json:"nextOrdinal,omitempty"`
	OversizedRecords      int64   `json:"oversizedRecords"`
	ScanTruncated         bool    `json:"scanTruncated"`
	WindowStartByte       int64   `json:"windowStartByte"`
	OversizedContinuation bool    `json:"oversizedContinuation,omitempty"`
}
type SourceRecord struct {
	SourceInput
	Revision          int64   `json:"revision"`
	LastResetRevision int64   `json:"lastResetRevision"`
	ResetToken        string  `json:"resetToken"`
	State             string  `json:"state"`
	LastErrorCode     *string `json:"lastErrorCode"`
}
type CallLocator struct {
	ID             string   `json:"id"`
	Ordinal        int64    `json:"ordinal"`
	FirstOrdinal   *int64   `json:"firstOrdinal,omitempty"`
	ByteStart      int64    `json:"byteStart"`
	ByteLength     int64    `json:"byteLength"`
	StartedAt      string   `json:"startedAt"`
	CompletedAt    *string  `json:"completedAt"`
	Status         string   `json:"status"`
	Source         string   `json:"source"`
	Model          *string  `json:"model"`
	DurationMs     *float64 `json:"durationMs"`
	Failed         bool     `json:"failed"`
	InputTokens    float64  `json:"inputTokens"`
	OutputTokens   float64  `json:"outputTokens"`
	RequestBytes   *float64 `json:"requestBytes"`
	ResponseBytes  *float64 `json:"responseBytes"`
	ResponseStatus *float64 `json:"responseStatus"`
	Revision       *int64   `json:"revision,omitempty"`
}
type EventLocator struct {
	ID         string  `json:"id"`
	Ordinal    int64   `json:"ordinal"`
	ByteStart  int64   `json:"byteStart"`
	ByteLength int64   `json:"byteLength"`
	Timestamp  string  `json:"timestamp"`
	Phase      string  `json:"phase"`
	Severity   string  `json:"severity"`
	CallID     *string `json:"callId"`
	Source     *string `json:"source"`
	Model      *string `json:"model"`
	Title      *string `json:"title"`
	Message    *string `json:"message"`
	Revision   *int64  `json:"revision,omitempty"`
}
type Summary struct {
	APICalls          int64        `json:"apiCalls"`
	FailedCalls       int64        `json:"failedCalls"`
	TotalDurationMs   float64      `json:"totalDurationMs"`
	TotalInputTokens  float64      `json:"totalInputTokens"`
	TotalOutputTokens float64      `json:"totalOutputTokens"`
	Models            []ModelCount `json:"models"`
	UpdatedAt         *string      `json:"updatedAt"`
}
type ModelCount struct {
	Model string `json:"model"`
	Calls int64  `json:"calls"`
}
type IndexedOverview struct {
	SourceRecord
	Summary Summary `json:"summary"`
}
type Projection struct {
	IndexedOverview
	Calls  []CallLocator  `json:"calls"`
	Events []EventLocator `json:"events"`
}
type ProjectionInput struct {
	Source SourceInput    `json:"source"`
	Calls  []CallLocator  `json:"calls"`
	Events []EventLocator `json:"events"`
}
type Index struct {
	DB   *localindex.Database
	Now  func() time.Time
	UUID func() string
}

func NewIndex(db *localindex.Database) *Index { return &Index{DB: db, Now: time.Now, UUID: uuid} }
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func ptr[T any](v T) *T { return &v }
func val[T any](v *T, f T) T {
	if v == nil {
		return f
	}
	return *v
}
func sourceRow(r protocol.JSONObject) SourceRecord {
	return SourceRecord{SourceInput: SourceInput{SessionID: text(r.Get("session_id")), FilePath: text(r.Get("file_path")), Size: int64(number(r.Get("size_bytes"))), MtimeMs: number(r.Get("mtime_ms")), IndexedBytes: int64(number(r.Get("indexed_bytes"))), FileIdentity: nullableString(r.Get("file_identity")), Fingerprint: nullableString(r.Get("fingerprint")), PendingTailBytes: ptr(int64(number(r.Get("pending_tail_bytes")))), NextOrdinal: ptr(int64(number(r.Get("next_ordinal")))), OversizedRecords: int64(number(r.Get("oversized_records"))), ScanTruncated: number(r.Get("scan_truncated")) == 1, WindowStartByte: int64(number(r.Get("window_start_byte")))}, Revision: int64(number(r.Get("revision"))), LastResetRevision: int64(number(r.Get("last_reset_revision"))), ResetToken: text(r.Get("reset_token")), State: text(r.Get("state")), LastErrorCode: nullableString(r.Get("last_error_code"))}
}
func callRow(r protocol.JSONObject) CallLocator {
	return CallLocator{ID: text(r.Get("call_id")), Ordinal: int64(number(r.Get("ordinal"))), ByteStart: int64(number(r.Get("byte_start"))), ByteLength: int64(number(r.Get("byte_length"))), Revision: ptr(int64(number(r.Get("revision")))), StartedAt: text(r.Get("started_at")), CompletedAt: nullableString(r.Get("completed_at")), Status: text(r.Get("status")), Source: text(r.Get("source")), Model: nullableString(r.Get("model")), DurationMs: nullableNumber(r.Get("duration_ms")), Failed: number(r.Get("failed")) == 1, InputTokens: number(r.Get("input_tokens")), OutputTokens: number(r.Get("output_tokens")), RequestBytes: nullableNumber(r.Get("request_bytes")), ResponseBytes: nullableNumber(r.Get("response_bytes")), ResponseStatus: nullableNumber(r.Get("response_status"))}
}
func eventRow(r protocol.JSONObject) EventLocator {
	return EventLocator{ID: text(r.Get("event_id")), Ordinal: int64(number(r.Get("ordinal"))), ByteStart: int64(number(r.Get("byte_start"))), ByteLength: int64(number(r.Get("byte_length"))), Revision: ptr(int64(number(r.Get("revision")))), Timestamp: text(r.Get("timestamp")), Phase: text(r.Get("phase")), Severity: text(r.Get("severity")), CallID: nullableString(r.Get("call_id")), Source: nullableString(r.Get("source")), Model: nullableString(r.Get("model")), Title: nullableString(r.Get("title")), Message: nullableString(r.Get("message"))}
}
func readSource(op *localindex.Operation, id string) (*SourceRecord, error) {
	r, e := op.Get("SELECT "+SOURCE_COLUMNS+" FROM trace_sources AS source JOIN trace_sessions AS session ON session.session_id=source.session_id WHERE source.session_id=?", id)
	if e != nil || r == nil {
		return nil, e
	}
	v := sourceRow(*r)
	return &v, nil
}
func overview(op *localindex.Operation, id string) (*IndexedOverview, error) {
	r, e := op.Get("SELECT "+SUMMARY_COLUMNS+" FROM trace_sources AS source JOIN trace_sessions AS session ON session.session_id=source.session_id WHERE source.session_id=?", id)
	if e != nil || r == nil {
		return nil, e
	}
	models, e := op.All("SELECT model,call_count FROM trace_session_models WHERE session_id=? ORDER BY first_started_at,first_ordinal,model LIMIT 64", id)
	if e != nil {
		return nil, e
	}
	s := Summary{APICalls: int64(number(r.Get("api_calls"))), FailedCalls: int64(number(r.Get("failed_calls"))), TotalDurationMs: number(r.Get("total_duration_ms")), TotalInputTokens: number(r.Get("total_input_tokens")), TotalOutputTokens: number(r.Get("total_output_tokens")), UpdatedAt: nullableString(r.Get("summary_updated_at")), Models: []ModelCount{}}
	for _, m := range models {
		s.Models = append(s.Models, ModelCount{text(m.Get("model")), int64(number(m.Get("call_count")))})
	}
	return &IndexedOverview{sourceRow(*r), s}, nil
}
func (i *Index) GetSource(ctx context.Context, id string) (v *SourceRecord, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error { v, e = readSource(op, id); return e })
	return
}
func (i *Index) GetSummary(ctx context.Context, id string) (v *IndexedOverview, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error { v, e = overview(op, id); return e })
	return
}
func safe(n int64) bool { return n >= 0 && n <= 9007199254740991 }
func normalizeSource(s SourceInput, next int64) (SourceInput, error) {
	tail := val(s.PendingTailBytes, s.Size-s.IndexedBytes)
	if !safe(s.Size) || !safe(s.IndexedBytes) || s.IndexedBytes > s.Size || !safe(tail) || tail != s.Size-s.IndexedBytes || !safe(next) || math.IsNaN(s.MtimeMs) || math.IsInf(s.MtimeMs, 0) || !safe(s.OversizedRecords) || !safe(s.WindowStartByte) || s.WindowStartByte > s.IndexedBytes {
		return s, errors.New("Invalid trace source progress")
	}
	s.PendingTailBytes = &tail
	s.NextOrdinal = &next
	return s, nil
}
func validate(ordinal, start, length int64, s SourceInput) error {
	if !safe(ordinal) || ordinal >= *s.NextOrdinal || !safe(start) || !safe(length) || length < 1 || start > 9007199254740991-length || start+length > s.IndexedBytes {
		return errors.New("Invalid trace locator")
	}
	return nil
}
func (i *Index) write(ctx context.Context, in ProjectionInput, replace bool) (out *SourceRecord, e error) {
	e = i.DB.Transaction(ctx, func(op *localindex.Operation) error {
		old, e := readSource(op, in.Source.SessionID)
		if e != nil {
			return e
		}
		revision := int64(1)
		reset := revision
		token := ""
		next := int64(0)
		if old != nil {
			revision = old.Revision + 1
			reset = old.LastResetRevision
			token = old.ResetToken
			if !replace {
				next = val(old.NextOrdinal, 0)
			}
		}
		if replace || old == nil {
			reset = revision
			token = i.UUID()
		}
		for _, c := range in.Calls {
			next = max(next, c.Ordinal+1)
		}
		for _, v := range in.Events {
			next = max(next, v.Ordinal+1)
		}
		next = val(in.Source.NextOrdinal, next)
		s, e := normalizeSource(in.Source, next)
		if e != nil {
			return e
		}
		var code *string
		if s.OversizedContinuation {
			code = ptr("TRACE_OVERSIZED_RECORD_CONTINUATION")
		}
		if e = op.Exec(UPSERT_SOURCE_SQL, s.SessionID, s.FilePath, s.Size, s.MtimeMs, s.IndexedBytes, revision, reset, code, i.Now().UnixMilli(), s.FileIdentity, s.Fingerprint, s.PendingTailBytes, s.OversizedRecords, s.ScanTruncated, s.WindowStartByte); e != nil {
			return e
		}
		if e = op.Exec(UPSERT_SESSION_SQL, s.SessionID, revision, reset, token, next); e != nil {
			return e
		}
		if replace {
			if e = op.Exec("DELETE FROM trace_calls WHERE session_id=?", s.SessionID); e != nil {
				return e
			}
			if e = op.Exec("DELETE FROM trace_events WHERE session_id=?", s.SessionID); e != nil {
				return e
			}
		}
		for _, c := range in.Calls {
			if e = validate(c.Ordinal, c.ByteStart, c.ByteLength, s); e != nil {
				return e
			}
			first := val(c.FirstOrdinal, c.Ordinal)
			if !safe(first) || first > c.Ordinal {
				return errors.New("Invalid trace call insertion order")
			}
			if e = op.Exec(UPSERT_CALL_SQL, s.SessionID, c.ID, c.Ordinal, first, c.ByteStart, c.ByteLength, revision, c.StartedAt, c.CompletedAt, c.Status, c.Source, c.Model, c.DurationMs, c.Failed, c.InputTokens, c.OutputTokens, c.RequestBytes, c.ResponseBytes, c.ResponseStatus); e != nil {
				return e
			}
		}
		for _, v := range in.Events {
			if e = validate(v.Ordinal, v.ByteStart, v.ByteLength, s); e != nil {
				return e
			}
			if e = op.Exec(INSERT_EVENT_SQL, s.SessionID, v.Ordinal, v.ID, v.ByteStart, v.ByteLength, revision, v.Timestamp, v.Phase, v.Severity, v.CallID, v.Source, v.Model, v.Title, v.Message); e != nil {
				return e
			}
		}
		if replace || len(in.Calls) > 0 {
			if e = rebuildSummary(op, s.SessionID); e != nil {
				return e
			}
		}
		out, e = readSource(op, s.SessionID)
		return e
	})
	return
}
func rebuildSummary(op *localindex.Operation, id string) error {
	if e := op.Exec(`UPDATE trace_sessions SET (api_calls,failed_calls,total_duration_ms,total_input_tokens,total_output_tokens)=(SELECT COUNT(*),COALESCE(SUM(failed),0),COALESCE(SUM(duration_ms),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM trace_calls WHERE session_id=?),summary_updated_at=(SELECT COALESCE(completed_at,started_at) FROM trace_calls WHERE session_id=? ORDER BY started_at DESC,first_ordinal DESC LIMIT 1) WHERE session_id=?`, id, id, id); e != nil {
		return e
	}
	if e := op.Exec("DELETE FROM trace_session_models WHERE session_id=?", id); e != nil {
		return e
	}
	return op.Exec(`INSERT INTO trace_session_models(session_id,model,call_count,first_started_at,first_ordinal) SELECT ?,model,call_count,started_at,first_ordinal FROM (SELECT model,started_at,first_ordinal,COUNT(*) OVER(PARTITION BY model) AS call_count,ROW_NUMBER() OVER(PARTITION BY model ORDER BY started_at,first_ordinal) AS position FROM trace_calls WHERE session_id=? AND model IS NOT NULL AND model!='') WHERE position=1`, id, id)
}
func (i *Index) Replace(ctx context.Context, in ProjectionInput) (*SourceRecord, error) {
	return i.write(ctx, in, true)
}
func (i *Index) Append(ctx context.Context, in ProjectionInput) (*SourceRecord, error) {
	return i.write(ctx, in, false)
}
func locatorRows(op *localindex.Operation, id string, floor *int64, offset, limit *int64) (calls []CallLocator, events []EventLocator, e error) {
	calls = []CallLocator{}
	events = []EventLocator{}
	where := ""
	bind := []any{id}
	if floor != nil {
		where = " AND revision>?"
		bind = append(bind, *floor)
	}
	page := ""
	if offset != nil {
		page = " LIMIT ? OFFSET ?"
		bind = append(bind, *limit, *offset)
	}
	rows, e := op.All("SELECT * FROM trace_calls WHERE session_id=?"+where+" ORDER BY started_at,first_ordinal"+page, bind...)
	if e != nil {
		return nil, nil, e
	}
	for _, r := range rows {
		calls = append(calls, callRow(r))
	}
	rows, e = op.All("SELECT * FROM trace_events WHERE session_id=?"+where+" ORDER BY timestamp,ordinal"+page, bind...)
	for _, r := range rows {
		events = append(events, eventRow(r))
	}
	return
}
func (i *Index) GetSession(ctx context.Context, id string, offset, limit *int64) (v *Projection, totalEvents int64, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error {
		o, e := overview(op, id)
		if e != nil || o == nil {
			return e
		}
		calls, events, e := locatorRows(op, id, nil, offset, limit)
		if e != nil {
			return e
		}
		count, e := op.Get("SELECT COUNT(*) AS count FROM trace_events WHERE session_id=?", id)
		if e != nil {
			return e
		}
		totalEvents = int64(number(count.Get("count")))
		v = &Projection{*o, calls, events}
		return nil
	})
	return
}
func (i *Index) GetCall(ctx context.Context, id, callID string) (s *SourceRecord, c *CallLocator, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error {
		s, e = readSource(op, id)
		if e != nil || s == nil {
			return e
		}
		r, e := op.Get("SELECT * FROM trace_calls WHERE session_id=? AND call_id=?", id, callID)
		if e != nil || r == nil {
			return e
		}
		v := callRow(*r)
		c = &v
		return nil
	})
	return
}
func (i *Index) GetChanges(ctx context.Context, id string, since int64) (v protocol.JSONObject, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error {
		s, e := readSource(op, id)
		if e != nil || s == nil {
			return e
		}
		reset := since < s.LastResetRevision
		var floor *int64
		if !reset {
			floor = &since
		}
		calls, events, e := locatorRows(op, id, floor, nil, nil)
		v = obj("sessionId", id, "revision", s.Revision, "reset", reset, "calls", calls, "events", events)
		return e
	})
	return
}
func (i *Index) List(ctx context.Context, ids []string, offset, limit int64) (items []IndexedOverview, total int64, e error) {
	items = []IndexedOverview{}
	e = i.DB.Read(ctx, func(op *localindex.Operation) error {
		where := ""
		bind := []any{}
		if len(ids) > 0 {
			where = " WHERE source.session_id IN (" + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + ")"
			for _, id := range ids {
				bind = append(bind, id)
			}
		}
		r, e := op.Get("SELECT COUNT(*) AS total FROM trace_sources AS source JOIN trace_sessions AS session ON session.session_id=source.session_id"+where, bind...)
		if e != nil {
			return e
		}
		total = int64(number(r.Get("total")))
		rows, e := op.All("SELECT source.session_id FROM trace_sources AS source JOIN trace_sessions AS session ON session.session_id=source.session_id"+where+" ORDER BY source.mtime_ms DESC,source.session_id LIMIT ? OFFSET ?", append(bind, max(0, limit), max(0, offset))...)
		if e != nil {
			return e
		}
		for _, r := range rows {
			o, e := overview(op, text(r.Get("session_id")))
			if e != nil {
				return e
			}
			if o != nil {
				items = append(items, *o)
			}
		}
		return nil
	})
	return
}
func (i *Index) Degrade(ctx context.Context, id, code string) error {
	return i.DB.Write(ctx, func(op *localindex.Operation) error {
		return op.Exec("UPDATE trace_sources SET state='degraded',last_error_code=?,updated_at_ms=? WHERE session_id=?", code, i.Now().UnixMilli(), id)
	})
}
func (i *Index) Delete(ctx context.Context, id string) error {
	return i.DB.Write(ctx, func(op *localindex.Operation) error {
		return op.Exec("DELETE FROM trace_sources WHERE session_id=?", id)
	})
}
