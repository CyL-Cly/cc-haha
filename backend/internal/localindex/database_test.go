package localindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceIndex(t *testing.T, home string, input any) map[string]json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	allocation := filepath.Dir(home)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-local-index.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_LOCAL_INDEX_FIXTURE=1"}
	cmd.Env = append(cmd.Env, "TZ=UTC")
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Actual Source index: %v %s", err, stderr.String())
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Source JSON: %v %s", err, data)
	}
	return result
}
func TestDatabaseWALReaderDoesNotWaitForProjectionWriter(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(t.TempDir(), "index.sqlite"), Kind: Primary})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Write(ctx, func(op *Operation) error { return op.Exec("INSERT INTO schema_meta VALUES('committed','old')") }); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- db.Transaction(ctx, func(op *Operation) error {
			if err := op.Exec("UPDATE schema_meta SET value='new'"); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	readDone := make(chan error, 1)
	go func() {
		readDone <- db.Read(ctx, func(op *Operation) error {
			r, e := op.Get("SELECT value FROM schema_meta WHERE key='committed'")
			if e == nil && stringValue(r.Get("value")) != "old" {
				return errors.New("WAL reader exposed uncommitted projection")
			}
			return e
		})
	}()
	select {
	case e := <-readDone:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Foreground reader blocked behind projection writer")
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	err = db.Transaction(ctx, func(op *Operation) error {
		if e := db.Read(op.Context(), func(read *Operation) error {
			r, e := read.Get("SELECT value FROM schema_meta WHERE key='committed'")
			if e == nil && stringValue(r.Get("value")) != "new" {
				return errors.New("Nested operation did not reuse owned connection")
			}
			return e
		}); e != nil {
			return e
		}
		e := db.Transaction(op.Context(), func(*Operation) error { return nil })
		if e == nil || e.Error() != "Local index transactions cannot be nested" {
			return errors.New("Nested transaction guard failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func disposableHome(t *testing.T) string {
	t.Helper()
	h := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(h, 0700); err != nil {
		t.Fatal(err)
	}
	return h
}
func decoded(v any) any { var out any; _ = json.Unmarshal(raw(v), &out); return out }
func TestDatabaseSchemasAndOldFilesAgainstActualSource(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []DatabaseKind{Primary, Content} {
		maximum := 6
		if kind == Content {
			maximum = 1
		}
		for version := 0; version <= maximum; version++ {
			t.Run(string(kind)+string(rune('0'+version)), func(t *testing.T) {
				home := disposableHome(t)
				observed := sourceIndex(t, home, map[string]any{"action": "database", "kind": kind, "seedVersion": version})
				if observed["error"] != nil {
					t.Fatalf("Source open: %s", raw(observed))
				}
				db, err := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(home, "fixture.sqlite"), Kind: kind})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var schema any
				err = db.Read(ctx, func(op *Operation) error {
					rows, e := op.All("SELECT type,name,tbl_name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name")
					schema = rows
					return e
				})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(decoded(schema), decoded(json.RawMessage(observed["schema"]))) {
					t.Fatalf("Schema differs from actual Source: native=%s Source=%s", raw(schema), observed["schema"])
				}
				if err = db.Transaction(ctx, func(op *Operation) error {
					if kind == Primary {
						return op.Exec("INSERT INTO schema_meta(key,value) VALUES('native-proof','read-after-write')")
					}
					return op.Exec("INSERT INTO search_backfill_state(scope,state,updated_at_ms) VALUES('native-proof','ready',42)")
				}); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				query := "SELECT value FROM schema_meta WHERE key='native-proof'"
				if kind == Content {
					query = "SELECT state,updated_at_ms FROM search_backfill_state WHERE scope='native-proof'"
				}
				back := sourceIndex(t, home, map[string]any{"action": "database", "kind": kind, "operations": []any{map[string]any{"statements": []any{map[string]any{"sql": query}}}}})
				if !bytes.Contains(back["observations"], []byte("read-after-write")) && kind == Primary || !bytes.Contains(back["observations"], []byte("ready")) && kind == Content {
					t.Fatalf("Source cannot read native committed row: %s", raw(back))
				}
			})
		}
	}
}
func TestDatabaseFTS5RollbackAndWAL(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDatabase(ctx, DatabaseOptions{Path: filepath.Join(t.TempDir(), "index.sqlite"), Kind: Content})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Transaction(ctx, func(op *Operation) error {
		return op.Exec(`INSERT INTO search_sources VALUES('fixture','project','owner','owner.jsonl',42,100,42,NULL,'proof',100,1,1,'ready',NULL,42); INSERT INTO search_documents(source_path,jsonl_line,byte_start,byte_length,segment_index,role,body,normalized_body) VALUES('fixture',1,0,100,0,'user','你好 Native FTS5','你好 native fts5')`)
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows any
	err = db.Read(ctx, func(op *Operation) error {
		r, e := op.All(`SELECT document.body FROM search_documents_fts JOIN search_documents AS document ON document.id=search_documents_fts.rowid WHERE search_documents_fts MATCH ?`, `"native"`)
		rows = r
		return e
	})
	if err != nil || !bytes.Contains(raw(rows), []byte("Native FTS5")) {
		t.Fatalf("FTS5 %v %s", err, raw(rows))
	}
	expected := errors.New("rollback proof")
	if err = db.Transaction(ctx, func(op *Operation) error {
		if e := op.Exec("DELETE FROM search_sources"); e != nil {
			return e
		}
		return expected
	}); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	err = db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT COUNT(*) AS total FROM search_documents")
		if e == nil && intValue(r.Get("total")) != 1 {
			t.Fatal("Rollback lost canonical projection")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(ctx, func(op *Operation) error { return op.Exec("DELETE FROM search_sources") })
	if err != nil {
		t.Fatal(err)
	}
	err = db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT COUNT(*) AS total FROM search_documents_fts WHERE search_documents_fts MATCH ?", `"native"`)
		if e == nil && intValue(r.Get("total")) != 0 {
			t.Fatal("Cascade left FTS entry")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := db.StorageStats()
	if err != nil || stats.DatabaseBytes <= 0 || stats.WALBytes <= 0 {
		t.Fatalf("Real WAL missing: %+v %v", stats, err)
	}
	if _, err = db.CheckpointPassive(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestDatabaseFutureSchemaAndUnsafePaths(t *testing.T) {
	for _, kind := range []DatabaseKind{Primary, Content} {
		t.Run(string(kind), func(t *testing.T) {
			home := disposableHome(t)
			observed := sourceIndex(t, home, map[string]any{"action": "database", "kind": kind, "seedVersion": 99})
			p := filepath.Join(home, "fixture.sqlite")
			before, _ := os.ReadFile(p)
			_, err := OpenDatabase(context.Background(), DatabaseOptions{Path: p, Kind: kind})
			var e *Error
			if !errors.As(err, &e) || !bytes.Equal(raw(e.Code), observed["code"]) {
				t.Fatalf("Future schema: %v %s", err, raw(observed))
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("Unsupported schema was modified")
			}
		})
	}
	root := t.TempDir()
	scope := filepath.Join(root, "config")
	_ = os.MkdirAll(scope, 0700)
	outside := filepath.Join(root, "outside")
	_ = os.MkdirAll(outside, 0700)
	if err := os.Symlink(outside, filepath.Join(scope, "cc-haha")); err != nil {
		t.Fatal(err)
	}
	if err := PrepareManagedDatabasePath(filepath.Join(scope, "cc-haha", "db", "index-v1.sqlite"), scope, "index-v1.sqlite"); err == nil || !strings.Contains(err.Error(), "UNSAFE_PATH") {
		t.Fatalf("Followed managed descendant symlink: %v", err)
	}
	real := filepath.Join(root, "real")
	_ = os.MkdirAll(real, 0700)
	alias := filepath.Join(root, "alias")
	_ = os.Symlink(real, alias)
	if err := PrepareManagedDatabasePath(filepath.Join(alias, "cc-haha", "db", "index-v1.sqlite"), alias, "index-v1.sqlite"); err != nil {
		t.Fatalf("Configured relocated trust root rejected: %v", err)
	}
	file := filepath.Join(root, "file.sqlite")
	_ = os.WriteFile(file, []byte("fixture"), 0600)
	if err := os.Link(file, file+"-wal"); err != nil {
		t.Fatal(err)
	}
	if err := PrepareManagedDatabasePath(file, "", "index-v1.sqlite"); err == nil {
		t.Fatal("Accepted hardlinked database family")
	}
}
