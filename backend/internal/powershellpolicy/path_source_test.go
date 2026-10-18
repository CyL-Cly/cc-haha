package powershellpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownedCanonical(path string) (string, bool, error) {
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		if real, err := filepath.EvalSymlinks(ancestor); err == nil {
			suffix, _ := filepath.Rel(ancestor, path)
			return filepath.Join(real, suffix), true, nil
		}
		if filepath.Dir(ancestor) == ancestor {
			return path, false, nil
		}
	}
}
func TestActualSourcePowerShellPathNormalizationAndNativeCanonicalResolution(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	outside := filepath.Join(home, "outside")
	for _, dir := range []string{work, outside} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "private.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "HEAD"), []byte("ref: refs/heads/private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	values := []string{"private.txt", "./private.txt", "./missing.txt", "../outside/private.txt", `dir\..\..\outside\private.txt`, "./link/private.txt", "./{literal}/private.txt", "./*/private.txt", "./*/../link/private.txt", "*.txt", "${PRIVATE}", "%PRIVATE%", "env:HOME", "Z:/private", "a1:/private", "1:/private", "//private.invalid/path", `\\private.invalid\path`, "private@SSL@path", "FileSystem::private.txt", "`private.txt", "~/private.txt", "ſ:private.txt", "K:private.txt", "HKLM:private.txt"}
	rows := []map[string]any{}
	for _, value := range values {
		for _, operation := range []string{"read", "write", "create"} {
			for _, mode := range []string{"default", "acceptEdits"} {
				rows = append(rows, map[string]any{"path": value, "operation": operation, "context": map[string]any{"mode": mode}})
			}
		}
	}
	var source struct {
		PathRows []struct{ Path json.RawMessage }
	}
	sourcePolicy(t, map[string]any{"home": home, "cwd": work, "pathRows": rows}, &source)
	if len(source.PathRows) != len(rows) {
		t.Fatal("actual Source path corpus missing")
	}
	for i, row := range rows {
		mode := row["context"].(map[string]any)["mode"].(string)
		context := PathContext{Platform: "darwin", CWD: work, Home: home, Mode: mode, WorkingDirectories: []string{work}, Resolve: SafeResolvePath, Deny: func(string, string) (*Rule, error) { return nil, nil }, Check: func(resolved, operation string, canonical bool) (PathCheck, error) {
			targets := []string{resolved}
			if !canonical {
				real, _, _ := ownedCanonical(resolved)
				targets = append(targets, real)
			}
			inside := true
			for _, target := range targets {
				rel, err := filepath.Rel(work, target)
				inside = inside && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
			}
			return PathCheck{Allowed: inside && (operation == "read" || mode == "acceptEdits")}, nil
		}}
		result, err := context.ValidatePath(row["path"].(string), row["operation"].(string))
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, "canonical path "+row["path"].(string)+"/"+row["operation"].(string)+"/"+mode, result, source.PathRows[i].Path)
	}
}
