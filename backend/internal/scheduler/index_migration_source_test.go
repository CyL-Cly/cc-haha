package scheduler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchedulerIndexActualSourceMigrationScrubsBodiesAndFutureVersionPreserved(t *testing.T) {
	data := actualSource(t, "page", map[string]any{"rows": []any{}, "migrations": []int{1, 3}})
	var source struct {
		Migrations []struct {
			Columns        []string
			Version        int
			HasPrivateBody bool
			Error          *struct{ Message, Code string }
			Unchanged      bool
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	for n, version := range []int{1, 3} {
		scope := t.TempDir()
		path := filepath.Join(scope, "cc-haha/db/scheduled-runs-v1.sqlite")
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		db, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		_, e = db.Exec("CREATE TABLE scheduled_run_source(singleton INTEGER PRIMARY KEY);CREATE TABLE scheduled_runs(run_id TEXT PRIMARY KEY,run_json TEXT);INSERT INTO scheduled_runs VALUES('old','private-legacy-body');PRAGMA user_version=" + stringID(version))
		if e != nil {
			t.Fatal(e)
		}
		_ = db.Close()
		before, _ := os.ReadFile(path)
		index, e := openRunIndex(scope, func() int64 { return 0 })
		if version == 3 {
			own, ok := e.(*Error)
			after, _ := os.ReadFile(path)
			if !ok || own.Code != source.Migrations[n].Error.Code || own.Message != source.Migrations[n].Error.Message || !bytes.Equal(before, after) || !source.Migrations[n].Unchanged {
				t.Fatal("future index destroyed or changed", e)
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		var actualVersion int
		if e = index.db.QueryRow("PRAGMA user_version").Scan(&actualVersion); e != nil {
			t.Fatal(e)
		}
		rows, e := index.db.Query("PRAGMA table_info(scheduled_runs)")
		if e != nil {
			t.Fatal(e)
		}
		columns := []string{}
		for rows.Next() {
			var cid, notNull, pk int
			var name, kind string
			var defaultValue any
			if e = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); e != nil {
				t.Fatal(e)
			}
			columns = append(columns, name)
		}
		_ = rows.Close()
		_ = index.db.Close()
		after, _ := os.ReadFile(path)
		if actualVersion != source.Migrations[n].Version || !equalJSON(raw(columns), raw(source.Migrations[n].Columns)) || bytes.Contains(after, []byte("private-legacy-body")) || source.Migrations[n].HasPrivateBody {
			t.Fatal("v1 metadata migration did not match or scrub", actualVersion, columns)
		}
	}
}
