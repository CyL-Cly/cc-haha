package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWorkspaceDirectoryValidationMatchesActualSourceAndSingleStat(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	external := filepath.Join(home, "external")
	for _, dir := range []string{cwd, external, filepath.Join(external, "child"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(external, "file"), []byte("physical"), 0600)
	type row struct {
		Path    string   `json:"path"`
		Working []string `json:"working"`
		Errno   string   `json:"errno,omitempty"`
	}
	rows := []row{{"", []string{}, ""}, {"  ", []string{}, ""}, {cwd + "/", []string{}, ""}, {external + "/", []string{}, ""}, {"../external", []string{}, ""}, {"~/external/child", []string{external}, ""}, {filepath.Join(external, "file"), []string{}, ""}, {filepath.Join(external, "missing"), []string{}, ""}, {filepath.Join(external, "file", "child"), []string{}, ""}}
	for _, code := range []string{"ENOENT", "ENOTDIR", "EACCES", "EPERM", "EIO"} {
		rows = append(rows, row{external, []string{}, code})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-directory.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "originalCwd": cwd, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source %v %.4000s", err, output)
	}
	var source []struct {
		Value       protocol.JSONObject
		Error, Help string
		Stats       int
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		t.Run(row.Path+row.Errno, func(t *testing.T) {
			stats := 0
			startup := &NativeStartup{options: NativeStartupOptions{Home: home, CWD: cwd, OriginalCWD: cwd, Platform: "darwin", Stat: func(file string) (fs.FileInfo, error) {
				stats++
				if row.Errno != "" {
					codes := map[string]syscall.Errno{"ENOENT": syscall.ENOENT, "ENOTDIR": syscall.ENOTDIR, "EACCES": syscall.EACCES, "EPERM": syscall.EPERM, "EIO": syscall.EIO}
					return nil, &os.PathError{Op: "stat", Path: file, Err: codes[row.Errno]}
				}
				return os.Stat(file)
			}}}
			entries := []any{}
			for _, dir := range row.Working {
				entries = append(entries, []any{dir, map[string]any{"path": dir, "source": "session"}})
			}
			serialized, _ := json.Marshal(map[string]any{"map": entries})
			permission := protocol.NewJSONObject(protocol.JSONField{Name: "additionalWorkingDirectories", Value: serialized})
			actual, err := startup.ValidateWorkspaceDirectory(row.Path, permission)
			if source[i].Error != "" {
				if err == nil || !errors.Is(err, syscall.EIO) {
					t.Fatal("unexpected stat error swallowed", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				a, _ := actual.MarshalJSON()
				b, _ := source[i].Value.MarshalJSON()
				_ = json.Unmarshal(a, &got)
				_ = json.Unmarshal(b, &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Go=%s Source=%s", a, b)
				}
			}
			if stats != source[i].Stats {
				t.Fatal("stat boundary", stats, source[i].Stats)
			}
		})
	}
}
