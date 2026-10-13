// Package localindex owns the disposable native SQLite projections. Canonical
// transcripts remain authoritative; these databases share the original schema.
package localindex

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	_ "modernc.org/sqlite"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

type DatabaseKind string

const (
	Primary DatabaseKind = "primary"
	Content DatabaseKind = "search"
	Trace   DatabaseKind = "trace"
)

type DatabaseOptions struct {
	Path, Scope string
	Kind        DatabaseKind
}
type Error struct {
	Code, Message string
	Cause         error
}

func (e *Error) Error() string         { return e.Message }
func (e *Error) Unwrap() error         { return e.Cause }
func coded(code, message string) error { return &Error{Code: code, Message: message} }

type Database struct {
	path           string
	kind           DatabaseKind
	sql            *sql.DB
	connection     *sql.Conn
	readConnection *sql.Conn
	mu             sync.RWMutex
	writeMu        cancellableMutex
	readMu         cancellableMutex
	closed         atomic.Bool
}
type Operation struct {
	database   *Database
	ctx        context.Context
	active     atomic.Bool
	write      bool
	connection *sql.Conn
}
type RunResult struct {
	Changes         int64 `json:"changes"`
	LastInsertRowid int64 `json:"lastInsertRowid"`
}
type StorageStats struct {
	DatabaseBytes int64 `json:"databaseBytes"`
	WALBytes      int64 `json:"walBytes"`
}
type Checkpoint struct {
	Busy               int64 `json:"busy"`
	LogFrames          int64 `json:"logFrames"`
	CheckpointedFrames int64 `json:"checkpointedFrames"`
}

func OpenDatabase(ctx context.Context, o DatabaseOptions) (*Database, error) {
	if o.Kind != Primary && o.Kind != Content && o.Kind != Trace {
		return nil, errors.New("Invalid native index database kind")
	}
	if o.Path != ":memory:" && !filepath.IsAbs(o.Path) {
		return nil, errors.New("Local index requires an explicit absolute database path")
	}
	if o.Path != ":memory:" {
		if err := PrepareManagedDatabasePath(o.Path, o.Scope, filename(o.Kind)); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", o.Path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	connection, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	d := &Database{path: o.Path, kind: o.Kind, sql: db, connection: connection}
	fail := func(err error) (*Database, error) { _ = connection.Close(); _ = db.Close(); return nil, err }
	version := 0
	if err = connection.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	maximum := 7
	code, message := "LOCAL_INDEX_SCHEMA_UNSUPPORTED", "Unsupported local index database schema"
	if o.Kind == Content {
		maximum = 1
		code = "SEARCH_CONTENT_SCHEMA_UNSUPPORTED"
		message = "Unsupported search content database schema"
	}
	if o.Kind == Trace {
		maximum = 6
		code = "TRACE_INDEX_SCHEMA_UNSUPPORTED"
		message = "Unsupported trace index database schema"
	}
	if version > maximum {
		return fail(coded(code, message))
	}
	journalSize := "16777216"
	if o.Kind == Trace {
		journalSize = "8388608"
	}
	for _, statement := range []string{"PRAGMA busy_timeout = 100", "PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL", "PRAGMA foreign_keys = ON", "PRAGMA wal_autocheckpoint = 1000", "PRAGMA journal_size_limit = " + journalSize} {
		if _, err = connection.ExecContext(ctx, statement); err != nil {
			return fail(err)
		}
	}
	if version < maximum {
		if _, err = connection.ExecContext(ctx, "BEGIN"); err != nil {
			return fail(err)
		}
		for next := version + 1; next <= maximum; next++ {
			script, e := schemaFiles.ReadFile(fmt.Sprintf("schema/%s-schema_v%d.sql", o.Kind, next))
			if e != nil {
				err = e
				break
			}
			if _, err = connection.ExecContext(ctx, string(script)); err != nil {
				break
			}
			if _, err = connection.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", next)); err != nil {
				break
			}
		}
		if err != nil {
			_, _ = connection.ExecContext(ctx, "ROLLBACK")
			return fail(err)
		}
		if _, err = connection.ExecContext(ctx, "COMMIT"); err != nil {
			_, _ = connection.ExecContext(ctx, "ROLLBACK")
			return fail(err)
		}
	}
	if o.Kind == Content {
		if err = d.checkContentSchema(ctx); err != nil {
			return fail(err)
		}
	}
	if o.Path != ":memory:" {
		d.readConnection, err = db.Conn(ctx)
		if err != nil {
			return fail(err)
		}
		readConfiguration := []string{"PRAGMA busy_timeout = 100", "PRAGMA synchronous = NORMAL", "PRAGMA foreign_keys = ON"}
		if o.Kind == Trace {
			readConfiguration = append(readConfiguration, "PRAGMA journal_size_limit = "+journalSize)
		}
		for _, statement := range readConfiguration {
			if _, err = d.readConnection.ExecContext(ctx, statement); err != nil {
				_ = d.readConnection.Close()
				return fail(err)
			}
		}
	}
	return d, nil
}
func filename(kind DatabaseKind) string {
	if kind == Trace {
		return "trace-index-v1.sqlite"
	}
	if kind == Content {
		return "search-index-v1.sqlite"
	}
	return "index-v1.sqlite"
}
func (d *Database) label() string {
	if d.kind == Trace {
		return "Trace index"
	}
	if d.kind == Content {
		return "Search content"
	}
	return "Local index"
}

type operationContextKey struct{}

// Context retains synchronous operation ownership for explicitly nested native
// calls. Independent goroutines retain their own context and serialize writers.
func (op *Operation) Context() context.Context { return op.ctx }
func (d *Database) with(ctx context.Context, write, transaction bool, fn func(*Operation) error) error {
	if owner, ok := ctx.Value(operationContextKey{}).(*Operation); ok && owner.database == d && owner.active.Load() {
		if transaction {
			return errors.New(d.label() + " transactions cannot be nested")
		}
		child := &Operation{database: d, ctx: ctx, write: write, connection: owner.connection}
		child.active.Store(true)
		defer child.active.Store(false)
		return fn(child)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed.Load() {
		return errors.New(d.label() + " database is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	connection := d.connection
	if !write && d.readConnection != nil {
		if e := d.readMu.lock(ctx); e != nil {
			return e
		}
		defer d.readMu.unlock()
		connection = d.readConnection
	} else {
		if e := d.writeMu.lock(ctx); e != nil {
			return e
		}
		defer d.writeMu.unlock()
	}
	op := &Operation{database: d, ctx: ctx, write: write, connection: connection}
	op.ctx = context.WithValue(ctx, operationContextKey{}, op)
	op.active.Store(true)
	defer op.active.Store(false)
	if !transaction {
		return fn(op)
	}
	if _, err := d.connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := fn(op); err != nil {
		_, _ = d.connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return err
	}
	if _, err := d.connection.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = d.connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return err
	}
	return nil
}
func (d *Database) Read(ctx context.Context, fn func(*Operation) error) error {
	return d.with(ctx, false, false, fn)
}
func (d *Database) Write(ctx context.Context, fn func(*Operation) error) error {
	return d.with(ctx, true, false, fn)
}
func (d *Database) Transaction(ctx context.Context, fn func(*Operation) error) error {
	return d.with(ctx, true, true, fn)
}
func (op *Operation) check() error {
	if op == nil || op.database == nil {
		return errors.New("Invalid native index operation")
	}
	if op.database.closed.Load() {
		return errors.New(op.database.label() + " database is closed")
	}
	if op.database.kind == Primary && !op.active.Load() {
		return errors.New("Local index database operation is no longer active")
	}
	return op.ctx.Err()
}
func (op *Operation) Exec(statement string, bindings ...any) error {
	if err := op.check(); err != nil {
		return err
	}
	if !op.write {
		return errors.New("Index read operation cannot write")
	}
	_, err := op.connection.ExecContext(op.ctx, statement, bindings...)
	return err
}
func (op *Operation) Run(statement string, bindings ...any) (RunResult, error) {
	if err := op.check(); err != nil {
		return RunResult{}, err
	}
	if !op.write {
		return RunResult{}, errors.New("Index read operation cannot write")
	}
	result, err := op.connection.ExecContext(op.ctx, statement, bindings...)
	if err != nil {
		return RunResult{}, err
	}
	changes, err := result.RowsAffected()
	if err != nil {
		return RunResult{}, err
	}
	row, err := result.LastInsertId()
	return RunResult{changes, row}, err
}
func (op *Operation) All(statement string, bindings ...any) ([]protocol.JSONObject, error) {
	if err := op.check(); err != nil {
		return nil, err
	}
	rows, err := op.connection.QueryContext(op.ctx, statement, bindings...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []protocol.JSONObject{}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		record := protocol.NewJSONObject()
		for i, key := range columns {
			value := values[i]
			if integer, ok := value.(int64); ok {
				value = float64(integer)
			}
			data, err := encodeValue(value)
			if err != nil {
				return nil, err
			}
			record.Set(key, data)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
func (op *Operation) Get(statement string, bindings ...any) (*protocol.JSONObject, error) {
	rows, err := op.All(statement, bindings...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}
func (d *Database) CheckpointPassive(ctx context.Context) (Checkpoint, error) {
	var result Checkpoint
	err := d.Read(ctx, func(op *Operation) error {
		row, err := op.Get("PRAGMA wal_checkpoint(PASSIVE)")
		if err != nil {
			return err
		}
		if row != nil {
			result = Checkpoint{intValue(row.Get("busy")), intValue(row.Get("log")), intValue(row.Get("checkpointed"))}
		}
		return nil
	})
	return result, err
}
func (d *Database) StorageStats() (StorageStats, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed.Load() {
		return StorageStats{}, errors.New(d.label() + " database is closed")
	}
	size := func(path string) int64 {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return info.Size()
		}
		return 0
	}
	return StorageStats{size(d.path), size(d.path + "-wal")}, nil
}
func (d *Database) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed.Swap(true) {
		return nil
	}
	err := d.connection.Close()
	if d.readConnection != nil {
		_ = d.readConnection.Close()
	}
	closeErr := d.sql.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (d *Database) checkContentSchema(ctx context.Context) error {
	required := map[string]bool{"table:search_sources": false, "table:search_documents": false, "table:search_documents_fts": false, "table:search_backfill_state": false, "trigger:search_documents_after_insert": false, "trigger:search_documents_after_delete": false, "trigger:search_documents_after_update": false}
	rows, err := d.connection.QueryContext(ctx, "SELECT type,name FROM sqlite_master WHERE name LIKE 'search_%'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := required[kind+":"+name]; ok {
			required[kind+":"+name] = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, found := range required {
		if !found {
			return coded("SQLITE_CORRUPT", "Search content database schema is incomplete or corrupt")
		}
	}
	for _, query := range []string{"SELECT path,project_path,owner_session_id,owner_transcript_path,modified_at_ms,size_bytes,mtime_ms,file_identity,fingerprint,indexed_bytes,indexed_lines,parser_version,state,last_error_code,updated_at_ms FROM search_sources LIMIT 1", "SELECT id,source_path,jsonl_line,byte_start,byte_length,segment_index,role,message_id,timestamp,body,normalized_body FROM search_documents LIMIT 1", "SELECT scope,state,generation,discovered,indexed,degraded,last_error_code,updated_at_ms FROM search_backfill_state LIMIT 1", `SELECT rowid FROM search_documents_fts WHERE search_documents_fts MATCH '"cc-haha-search-health-probe"' LIMIT 1`} {
		rows, err := d.connection.QueryContext(ctx, query)
		if err != nil {
			if strings.Contains(err.Error(), "no such ") || strings.Contains(err.Error(), "malformed") {
				return coded("SQLITE_CORRUPT", "Search content database schema is incomplete or corrupt")
			}
			return err
		}
		_ = rows.Close()
	}
	return nil
}
