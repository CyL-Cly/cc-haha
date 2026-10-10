package ccswitch

import (
	"context"
	"database/sql"
	"net/url"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	_ "modernc.org/sqlite"
)

func queryRecords(ctx context.Context, db *sql.DB, query string) ([]protocol.JSONObject, error) {
	rows, e := db.QueryContext(ctx, query)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	columns, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if e = rows.Scan(pointers...); e != nil {
			return nil, e
		}
		o := record()
		for i, column := range columns {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			o.Set(column, raw(v))
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func sqliteRows(ctx context.Context, path string) ([]Row, string) {
	if !isPath(path, false) {
		return nil, "not-found"
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, "unreadable"
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tables, e := queryRecords(ctx, db, "SELECT name FROM sqlite_master WHERE type='table' AND name='providers'")
	if e != nil {
		return nil, "unreadable"
	}
	if len(tables) == 0 {
		return nil, "schema-unsupported"
	}
	columns, e := queryRecords(ctx, db, "PRAGMA table_info(providers)")
	if e != nil {
		return nil, "unreadable"
	}
	known := map[string]bool{}
	for _, row := range columns {
		known[text(trimmed(row.Get("name")))] = true
	}
	for _, required := range []string{"id", "app_type", "settings_config"} {
		if !known[required] {
			return nil, "schema-unsupported"
		}
	}
	values, e := queryRecords(ctx, db, "SELECT * FROM providers WHERE app_type IN ('claude','claude-desktop')")
	if e != nil {
		return nil, "unreadable"
	}
	rows := []Row{}
	for _, v := range values {
		id := trimmed(v.Get("id"))
		app := text(trimmed(v.Get("app_type")))
		if id == nil || (app != "claude" && app != "claude-desktop") {
			continue
		}
		name := v.Get("name")
		if !isString(name) {
			name = str("")
		}
		rows = append(rows, Row{ID: id, Name: name, AppType: app, SettingsConfig: v.Get("settings_config"), Meta: v.Get("meta"), Notes: trimmed(v.Get("notes")), SortIndex: number(v.Get("sort_index"), true), CreatedAt: number(v.Get("created_at"), true), IsCurrent: boolean(v.Get("is_current"))})
	}
	if len(rows) == 0 {
		apps, e := queryRecords(ctx, db, "SELECT DISTINCT app_type FROM providers")
		if e != nil {
			return nil, "unreadable"
		}
		hasNamed, hasKnown := false, false
		for _, v := range apps {
			app := text(trimmed(v.Get("app_type")))
			if app == "" {
				continue
			}
			hasNamed = true
			switch app {
			case "claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes":
				hasKnown = true
			}
		}
		if hasNamed && !hasKnown {
			return nil, "schema-unsupported"
		}
	}
	return sorted(rows), ""
}
