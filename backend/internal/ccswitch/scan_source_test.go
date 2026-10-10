package ccswitch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func seedFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, value, 0600); e != nil {
		t.Fatal(e)
	}
}
func legacyProvider(name string) map[string]any {
	return map[string]any{"name": name, "settingsConfig": map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://private.invalid/", "ANTHROPIC_AUTH_TOKEN": "fake-long-credential", "ANTHROPIC_MODEL": "model[1m]"}}}
}
func createDB(t *testing.T, path, schema string, rows []map[string]any) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		names, qs := "", ""
		values := []any{}
		for key, value := range row {
			if names != "" {
				names += ","
				qs += ","
			}
			names += key
			qs += "?"
			if v, ok := value.(json.RawMessage); ok {
				value = string(v)
			}
			values = append(values, value)
		}
		if _, e = db.Exec("INSERT INTO providers ("+names+") VALUES ("+qs+")", values...); e != nil {
			t.Fatal(e)
		}
	}
}
func directoryBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() && path == filepath.Join(root, "Library", "Caches") {
			return filepath.SkipDir
		}
		if d.IsDir() && filepath.Dir(path) == root {
			switch filepath.Base(path) {
			case ".cc-switch", "elsewhere", "Library", "AppData", ".config":
			default:
				return filepath.SkipDir
			}
		}
		if !d.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(root, path)
			digest := sha256.Sum256(b)
			if filepath.Dir(path) != root {
				out[rel] = fmt.Sprintf("%x", digest)
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestCcSwitchReadOnlyStoresAgainstActualOriginalProducer(t *testing.T) {
	profiles := []string{"missing", "empty-dir", "v1", "bad-json", "bom-json", "migrated", "bad-then-migrated", "scalar", "legacy-sorted", "legacy-cross-dedupe", "legacy-same-app", "legacy-different-notes", "override-home", "override-fixed-current", "missing-override", "sqlite-priority", "sqlite-corrupt-no-fallback", "sqlite-no-table", "sqlite-missing-column", "sqlite-known-other", "sqlite-unknown-other", "sqlite-unknown-delimited", "sqlite-empty", "sqlite-optional-columns", "sqlite-sorted", "sqlite-current"}
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".cc-switch")
			config := map[string]any{"claude": map[string]any{"providers": map[string]any{"a": legacyProvider("Private provider")}, "current": "a"}}
			if profile != "missing" {
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
			}
			switch profile {
			case "v1":
				seedFile(t, filepath.Join(dir, "config.json"), []byte(`{"providers":{},"current":"a"}`))
			case "bad-json":
				seedFile(t, filepath.Join(dir, "config.json"), []byte("{"))
			case "bom-json":
				seedFile(t, filepath.Join(dir, "config.json"), append([]byte{239, 187, 191}, raw(config)...))
			case "migrated":
				seedFile(t, filepath.Join(dir, "config.json.migrated"), raw(config))
			case "bad-then-migrated":
				seedFile(t, filepath.Join(dir, "config.json"), []byte("{"))
				seedFile(t, filepath.Join(dir, "config.json.migrated"), raw(config))
			case "scalar":
				seedFile(t, filepath.Join(dir, "config.json"), []byte("null"))
			case "legacy-sorted":
				a, b, c := legacyProvider("A"), legacyProvider("B"), legacyProvider("C")
				a["sortIndex"] = "0x10"
				b["sortIndex"] = ""
				c["sortIndex"] = 0
				c["createdAt"] = 1
				config = map[string]any{"claude": map[string]any{"providers": map[string]any{"\ue000": a, "😀": b, "surrogate": c, "10": false, "0": legacyProvider("numeric")}, "current": "😀"}}
				seedFile(t, filepath.Join(dir, "config.json"), raw(config))
			case "legacy-cross-dedupe", "legacy-same-app", "legacy-different-notes":
				other := legacyProvider("Private provider")
				if profile == "legacy-different-notes" {
					other["notes"] = "Different saved note"
				}
				app := map[string]any{"providers": map[string]any{"b": other}}
				if profile == "legacy-same-app" {
					config["claude"].(map[string]any)["providers"].(map[string]any)["b"] = other
				} else {
					config["claude-desktop"] = app
				}
				seedFile(t, filepath.Join(dir, "config.json"), raw(config))
			case "override-home", "override-fixed-current", "missing-override":
				override := "~/elsewhere"
				target := filepath.Join(home, "elsewhere")
				if profile == "missing-override" {
					target = dir
				} else {
					if e := os.MkdirAll(target, 0700); e != nil {
						t.Fatal(e)
					}
				}
				store := filepath.Join(home, "Library", "Application Support", "com.ccswitch.desktop", "app_paths.json")
				if runtime.GOOS == "linux" {
					store = filepath.Join(home, ".config", "com.ccswitch.desktop", "app_paths.json")
				}
				if runtime.GOOS == "windows" {
					store = filepath.Join(home, "AppData", "Roaming", "com.ccswitch.desktop", "app_paths.json")
				}
				seedFile(t, store, raw(map[string]string{"app_config_dir_override": override}))
				seedFile(t, filepath.Join(target, "config.json"), raw(config))
				if profile == "override-fixed-current" {
					seedFile(t, filepath.Join(dir, "settings.json"), []byte(`{"currentProviderClaude":"a"}`))
				}
			default:
				if len(profile) >= 6 && profile[:6] == "sqlite" {
					schema := "CREATE TABLE providers(id TEXT,app_type TEXT,settings_config TEXT)"
					rows := []map[string]any{}
					p := legacyProvider("Private provider")
					row := map[string]any{"id": "a", "app_type": "claude", "settings_config": raw(p["settingsConfig"])}
					switch profile {
					case "sqlite-corrupt-no-fallback":
						seedFile(t, filepath.Join(dir, "cc-switch.db"), []byte("not a database"))
						seedFile(t, filepath.Join(dir, "config.json"), raw(config))
					case "sqlite-no-table":
						schema = "CREATE TABLE unrelated(id TEXT)"
					case "sqlite-missing-column":
						schema = "CREATE TABLE providers(id TEXT,app_type TEXT)"
					case "sqlite-known-other":
						row["app_type"] = "codex"
						rows = append(rows, row)
					case "sqlite-unknown-other", "sqlite-unknown-delimited":
						row["app_type"] = "new-app-taxonomy"
						if profile == "sqlite-unknown-delimited" {
							row["app_type"] = "codex|gemini"
						}
						rows = append(rows, row)
					case "sqlite-priority":
						rows = append(rows, row)
						seedFile(t, filepath.Join(dir, "config.json"), []byte(`{"providers":{},"current":"old"}`))
					case "sqlite-optional-columns":
						rows = append(rows, row)
					case "sqlite-sorted", "sqlite-current":
						schema = "CREATE TABLE providers(id TEXT,app_type TEXT,settings_config TEXT,name TEXT,meta TEXT,notes TEXT,sort_index TEXT,created_at TEXT,is_current INTEGER)"
						row["name"] = " Private provider "
						row["meta"] = `{"apiFormat":"openai_chat"}`
						row["sort_index"] = ""
						row["created_at"] = "0x10"
						row["is_current"] = 1
						row["notes"] = "note fake-long-credential"
						rows = append(rows, row)
						if profile == "sqlite-current" {
							seedFile(t, filepath.Join(dir, "settings.json"), []byte(`{"currentProviderClaude":"other"}`))
						}
					}
					if profile != "sqlite-corrupt-no-fallback" {
						createDB(t, filepath.Join(dir, "cc-switch.db"), schema, rows)
					}
				}
			}
			before := directoryBytes(t, home)
			existing := []protocol.JSONObject{obj([]byte(`{"id":"already","name":"private provider","apiKey":"another-key","baseUrl":"HTTPS://PRIVATE.INVALID"}`))}
			ids := []string{"claude:a", "claude:a", "claude-desktop:b", "unknown"}
			want := obj(source(t, home, map[string]any{"home": home, "cases": []any{}, "scan": true, "existing": existing, "ids": ids}))
			scan, e := Read(context.Background(), Options{Home: home, Platform: runtime.GOOS, Existing: existing, Presets: presets(t)})
			if e != nil {
				t.Fatal(e)
			}
			compare(t, want.Get("scan"), scan.Public())
			inputs, skipped := scan.Resolve(ids)
			compare(t, want.Get("imports"), map[string]any{"inputs": inputs, "skipped": skipped})
			compare(t, want.Get("entries"), scan.Entries)
			if !reflect.DeepEqual(before, directoryBytes(t, home)) {
				t.Fatal("cc-switch scan changed source store bytes or created files")
			}
			if strings.Contains(string(raw(scan.Public())), "fake-long-credential") {
				t.Fatal("scan leaked full resolved credential")
			}
		})
	}
}
