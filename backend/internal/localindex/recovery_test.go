package localindex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func backupTree(t *testing.T, path string) map[string]any {
	t.Helper()
	entries, e := os.ReadDir(path)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]any{}
	pattern := regexp.MustCompile(`[0-9a-f]{8}$`)
	for _, entry := range entries {
		name := pattern.ReplaceAllString(entry.Name(), "$$TOKEN")
		child := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			out[name] = backupTree(t, child)
		} else {
			data, e := os.ReadFile(child)
			if e != nil {
				t.Fatal(e)
			}
			out[name] = string(data)
		}
	}
	return out
}
func TestBackupActualSourceFamilyRetentionAndRollback(t *testing.T) {
	ops := []map[string]any{{"files": map[string]string{"": "db0", "-wal": "wal0", "-shm": "shm0", "-journal": "untouched"}}, {}, {"files": map[string]string{"": "db2", "-wal": "wal2"}, "failAt": 2}, {"files": map[string]string{"": "db3", "-wal": "wal3"}}, {"files": map[string]string{"": "db4"}}, {"files": map[string]string{"": "db5"}}, {"files": map[string]string{"": "db6"}, "failAt": 2}, {"files": map[string]string{"": "db7"}}}
	source := sourceIndex(t, disposableHome(t), map[string]any{"action": "recovery", "operations": ops})
	var rows []json.RawMessage
	if e := json.Unmarshal(source["observations"], &rows); e != nil {
		t.Fatal(e)
	}
	home := disposableHome(t)
	scope := filepath.Join(home, ".claude")
	path := filepath.Join(scope, "cc-haha", "db", filename(Primary))
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	for n, row := range ops {
		if files, ok := row["files"].(map[string]string); ok {
			for suffix, data := range files {
				if e := os.WriteFile(path+suffix, []byte(data), 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
		calls := 0
		o := BackupOptions{Scope: scope, DatabasePath: path, Reason: "MANUAL_REBUILD", Now: func() time.Time { return time.UnixMilli(int64(n) * 1000) }, Token: func() (string, error) { return "12345678", nil }}
		if fail, ok := row["failAt"].(int); ok {
			o.Rename = func(a, b string) error {
				calls++
				if calls == fail {
					return errors.New("Injected rename failure")
				}
				return os.Rename(a, b)
			}
		}
		result, e := BackupDatabaseFamily(o)
		observed := map[string]any{"tree": backupTree(t, filepath.Dir(path))}
		if e == nil {
			result.BackupPath = strings.ReplaceAll(result.BackupPath, home, "$HOME")
			result.BackupPath = regexp.MustCompile(`[0-9a-f]{8}$`).ReplaceAllString(result.BackupPath, "$$TOKEN")
			observed["result"] = result
		} else {
			observed["error"] = e.Error()
			observed["code"] = errorCode(e, "")
		}
		if !reflect.DeepEqual(decoded(observed), decoded(rows[n])) {
			t.Fatalf("Backup case %d\nNative=%s\nSource=%s", n, raw(observed), rows[n])
		}
	}
}
func TestBackupRejectsRedirectedFilesAndPreservesCanonicalData(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "ancestor"} {
		t.Run(kind, func(t *testing.T) {
			scope := filepath.Join(t.TempDir(), "scope")
			canonical := filepath.Join(t.TempDir(), "canonical.jsonl")
			_ = os.WriteFile(canonical, []byte("protected"), 0600)
			path := filepath.Join(scope, "cc-haha", "db", filename(Primary))
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			switch kind {
			case "symlink":
				_ = os.Symlink(canonical, path)
			case "hardlink":
				_ = os.Link(canonical, path)
			case "ancestor":
				_ = os.Remove(filepath.Dir(path))
				_ = os.Symlink(filepath.Dir(canonical), filepath.Dir(path))
			}
			_, e := BackupDatabaseFamily(BackupOptions{Scope: scope, DatabasePath: path, Reason: "MANUAL_REBUILD"})
			if errorCode(e, "") != "LOCAL_INDEX_UNSAFE_PATH" {
				t.Fatalf("Unsafe backup %v", e)
			}
			data, _ := os.ReadFile(canonical)
			if string(data) != "protected" {
				t.Fatal("Canonical user data mutated")
			}
		})
	}
}
