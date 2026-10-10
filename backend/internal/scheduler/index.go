package scheduler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	_ "modernc.org/sqlite"
)

const indexSchema = `CREATE TABLE scheduled_run_source (
 singleton INTEGER PRIMARY KEY CHECK (singleton = 1), source_path TEXT NOT NULL,
 source_size INTEGER NOT NULL, source_mtime_ms REAL NOT NULL, source_fingerprint TEXT NOT NULL,
 revision INTEGER NOT NULL, state TEXT NOT NULL CHECK (state IN ('ready','degraded')),
 last_error_code TEXT, updated_at_ms INTEGER NOT NULL);
 CREATE TABLE scheduled_runs (run_id TEXT PRIMARY KEY, task_id TEXT NOT NULL,
 started_at TEXT NOT NULL, started_at_ms REAL NOT NULL, completed_at TEXT, completed_at_ms REAL,
 status TEXT NOT NULL, has_output INTEGER NOT NULL CHECK (has_output IN (0,1)),
 has_error INTEGER NOT NULL CHECK (has_error IN (0,1)), exit_code REAL, duration_ms REAL,
 session_id TEXT, source_ordinal INTEGER NOT NULL, revision INTEGER NOT NULL);
 CREATE INDEX scheduled_runs_order_idx ON scheduled_runs(started_at_ms DESC,source_ordinal ASC,run_id ASC);
 CREATE INDEX scheduled_runs_task_order_idx ON scheduled_runs(task_id,started_at_ms DESC,source_ordinal ASC,run_id ASC);
 CREATE INDEX scheduled_runs_terminal_completion_idx ON scheduled_runs(status,completed_at_ms,started_at_ms DESC,source_ordinal ASC,run_id ASC);`

type indexStatus struct {
	SourcePath    string
	SourceSize    int64
	SourceMtimeMs float64
	Fingerprint   string
	Revision      int64
	State         string
	LastError     *string
}
type runIndex struct {
	db  *sql.DB
	now func() int64
}

func openRunIndex(scope string, now func() int64) (*runIndex, error) {
	path := filepath.Join(scope, "cc-haha/db/scheduled-runs-v1.sqlite")
	if e := localindex.PrepareManagedDatabasePath(path, scope, "scheduled-runs-v1.sqlite"); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	success := false
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()
	var version int
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return nil, e
	}
	if version > 2 {
		return nil, &Error{500, "SCHEDULED_RUN_INDEX_SCHEMA_UNSUPPORTED", "Unsupported scheduled-run index database schema"}
	}
	for _, pragma := range []string{"PRAGMA busy_timeout=100", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON", "PRAGMA wal_autocheckpoint=1000"} {
		if _, e = db.Exec(pragma); e != nil {
			return nil, e
		}
	}
	if version < 2 {
		if version != 0 {
			if _, e = db.Exec("PRAGMA secure_delete=ON"); e != nil {
				return nil, e
			}
		}
		tx, e := db.Begin()
		if e != nil {
			return nil, e
		}
		if version != 0 {
			_, e = tx.Exec("DROP TABLE scheduled_runs;DROP TABLE scheduled_run_source")
		}
		if e == nil {
			_, e = tx.Exec(indexSchema)
		}
		if e == nil {
			_, e = tx.Exec("PRAGMA user_version=2")
		}
		if e != nil {
			_ = tx.Rollback()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		if version != 0 {
			for _, statement := range []string{"PRAGMA wal_checkpoint(TRUNCATE)", "VACUUM", "PRAGMA wal_checkpoint(TRUNCATE)"} {
				if _, e = db.Exec(statement); e != nil {
					return nil, e
				}
			}
		}
	}
	success = true
	return &runIndex{db, now}, nil
}
func (i *runIndex) status(ctx context.Context) (indexStatus, error) {
	var s indexStatus
	e := i.db.QueryRowContext(ctx, `SELECT source_path,source_size,source_mtime_ms,source_fingerprint,revision,state,last_error_code FROM scheduled_run_source WHERE singleton=1`).Scan(&s.SourcePath, &s.SourceSize, &s.SourceMtimeMs, &s.Fingerprint, &s.Revision, &s.State, &s.LastError)
	if errors.Is(e, sql.ErrNoRows) {
		s.State = "degraded"
		reason := "SCHEDULED_RUN_INDEX_NOT_BUILT"
		s.LastError = &reason
		e = nil
	}
	return s, e
}
func nullableString(row protocol.JSONObject, key string) any {
	v := row.Get(key)
	if v == nil || string(v) == "null" {
		return nil
	}
	var out string
	if json.Unmarshal(v, &out) != nil {
		return nil
	}
	return out
}
func nullableNumber(row protocol.JSONObject, key string) any {
	v := row.Get(key)
	if v == nil || string(v) == "null" {
		return nil
	}
	var out float64
	if json.Unmarshal(v, &out) != nil {
		return nil
	}
	return out
}
func stringPresent(row protocol.JSONObject, key string) int {
	v := row.Get(key)
	if len(v) > 0 && v[0] == '"' {
		return 1
	}
	return 0
}
func (i *runIndex) replace(ctx context.Context, runs []protocol.JSONObject, path string, f localindex.Fingerprint, serialized string, failAfter int) (err error) {
	status, e := i.status(ctx)
	if e != nil {
		return e
	}
	revision := status.Revision + 1
	tx, e := i.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, "DELETE FROM scheduled_runs"); err != nil {
		return err
	}
	statement, e := tx.PrepareContext(ctx, `INSERT INTO scheduled_runs(run_id,task_id,started_at,started_at_ms,completed_at,completed_at_ms,status,has_output,has_error,exit_code,duration_ms,session_id,source_ordinal,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if e != nil {
		return e
	}
	defer statement.Close()
	for ordinal, row := range runs {
		started, valid, e := dateMs(row.Get("startedAt"))
		if e != nil {
			return e
		}
		if !valid {
			started = 0
		}
		completedRaw := row.Get("completedAt")
		if completedRaw == nil || string(completedRaw) == "null" {
			completedRaw = row.Get("startedAt")
		}
		completed, valid, e := dateMs(completedRaw)
		if e != nil {
			return e
		}
		var completedMs any
		if valid {
			completedMs = completed
		}
		if _, err = statement.ExecContext(ctx, row.String("id"), row.String("taskId"), row.String("startedAt"), started, nullableString(row, "completedAt"), completedMs, row.String("status"), stringPresent(row, "output"), stringPresent(row, "error"), nullableNumber(row, "exitCode"), nullableNumber(row, "durationMs"), nullableString(row, "sessionId"), ordinal, revision); err != nil {
			return err
		}
		if failAfter == ordinal+1 {
			return errors.New("injected scheduled-run projection failure")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_run_source(singleton,source_path,source_size,source_mtime_ms,source_fingerprint,revision,state,last_error_code,updated_at_ms) VALUES(1,?,?,?,?,?,'ready',NULL,?) ON CONFLICT(singleton) DO UPDATE SET source_path=excluded.source_path,source_size=excluded.source_size,source_mtime_ms=excluded.source_mtime_ms,source_fingerprint=excluded.source_fingerprint,revision=excluded.revision,state='ready',last_error_code=NULL,updated_at_ms=excluded.updated_at_ms`, path, f.Size, f.MtimeMs, serialized, revision, i.now())
	if err != nil {
		return err
	}
	err = tx.Commit()
	return err
}
func (i *runIndex) degraded(ctx context.Context, reason string) {
	_, _ = i.db.ExecContext(ctx, "UPDATE scheduled_run_source SET state='degraded',last_error_code=?,updated_at_ms=? WHERE singleton=1", reason, i.now())
}
func (i *runIndex) page(ctx context.Context, o PageOptions) (Page, error) {
	status, e := i.status(ctx)
	if e != nil {
		return Page{}, e
	}
	revision := "sqlite:" + stableToken(status.Fingerprint)
	expected := scopedRevision(revision, o)
	cursor, reset := decodeCursor(o.Cursor, expected)
	where := []string{}
	args := []any{}
	if o.TaskID != "" {
		where = append(where, "task_id=?")
		args = append(args, o.TaskID)
	}
	if o.NonterminalOnly {
		where = append(where, "status NOT IN ('completed','failed','timeout')")
	}
	if o.CompletedAfterMs != nil {
		where = append(where, "status IN ('completed','failed','timeout') AND completed_at_ms>=?")
		args = append(args, *o.CompletedAfterMs)
	}
	if cursor != nil {
		where = append(where, "(started_at_ms < ? OR (started_at_ms = ? AND source_ordinal > ?) OR (started_at_ms = ? AND source_ordinal = ? AND run_id > ?))")
		args = append(args, cursor.time, cursor.time, cursor.ordinal, cursor.time, cursor.ordinal, cursor.id)
	}
	limit := pageLimit(o)
	sqlLimit := int64(math.MaxInt64)
	if !math.IsInf(limit, 1) && limit < float64(math.MaxInt64) {
		if math.IsNaN(limit) {
			return Page{}, errors.New("datatype mismatch")
		}
		sqlLimit = int64(limit) + 1
	}
	args = append(args, sqlLimit)
	filter := ""
	if len(where) > 0 {
		filter = " WHERE " + strings.Join(where, " AND ")
	}
	rows, e := i.db.QueryContext(ctx, `SELECT run_id,task_id,started_at,completed_at,status,has_output,has_error,exit_code,duration_ms,session_id,source_ordinal,started_at_ms FROM scheduled_runs`+filter+` ORDER BY started_at_ms DESC,source_ordinal ASC,run_id ASC LIMIT ?`, args...)
	if e != nil {
		return Page{}, e
	}
	defer rows.Close()
	items := []pageItem{}
	for rows.Next() {
		var id, task, started, status string
		var completed, session sql.NullString
		var hasOutput, hasError, ordinal int
		var exit, duration sql.NullFloat64
		var startedMs float64
		if e = rows.Scan(&id, &task, &started, &completed, &status, &hasOutput, &hasError, &exit, &duration, &session, &ordinal, &startedMs); e != nil {
			return Page{}, e
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoted(id)}, protocol.JSONField{Name: "taskId", Value: quoted(task)}, protocol.JSONField{Name: "startedAt", Value: quoted(started)})
		if completed.Valid {
			row.Set("completedAt", quoted(completed.String))
		}
		row.Set("status", quoted(status))
		row.Set("hasOutput", raw(hasOutput == 1))
		row.Set("hasError", raw(hasError == 1))
		if exit.Valid {
			row.Set("exitCode", raw(exit.Float64))
		}
		if duration.Valid {
			row.Set("durationMs", raw(duration.Float64))
		}
		if session.Valid {
			row.Set("sessionId", quoted(session.String))
		}
		items = append(items, pageItem{row, position{startedMs, ordinal, id}})
	}
	if e = rows.Err(); e != nil {
		return Page{}, e
	}
	count := len(items)
	if float64(count) > limit {
		count = int(limit)
	}
	out := Page{Runs: []protocol.JSONObject{}, Revision: &status.Revision, RevisionToken: revision, Reset: reset}
	for _, item := range items[:count] {
		out.Runs = append(out.Runs, item.row)
	}
	if float64(len(items)) > limit && count > 0 {
		out.NextCursor = encodeCursor(items[count-1].position, expected)
	}
	return out, nil
}

const runFingerprintPrefix = "cc-haha-scheduled-run-fingerprint:v1:"

func serializeRunFingerprint(f localindex.Fingerprint) string {
	return runFingerprintPrefix + base64.RawURLEncoding.EncodeToString(protocol.StringifyValidatedJavaScriptJSON(raw([]any{localindex.SerializeFingerprint(f), f.CtimeMs})))
}
func deserializeRunFingerprint(value string) *localindex.Fingerprint {
	if !strings.HasPrefix(value, runFingerprintPrefix) {
		return nil
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimPrefix(value, runFingerprintPrefix), "="))
	var fields []json.RawMessage
	if e != nil || json.Unmarshal(b, &fields) != nil || len(fields) != 2 {
		return nil
	}
	var s string
	var ctime float64
	if json.Unmarshal(fields[0], &s) != nil || json.Unmarshal(fields[1], &ctime) != nil || string(fields[1]) == "null" {
		return nil
	}
	f := localindex.DeserializeFingerprint(s)
	if f != nil && f.CtimeMs != ctime {
		return nil
	}
	return f
}
func busyError(e error) bool {
	return e != nil && (strings.Contains(e.Error(), "SQLITE_BUSY") || strings.Contains(e.Error(), "SQLITE_LOCKED"))
}
func indexError(e error) error { return fmt.Errorf("scheduled-run projection: %w", e) }
