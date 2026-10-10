package builtintools

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkingPathsAgainstActualSourceAliasesCaseAndTraversal(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun unavailable")
	}
	rows := []struct {
		Path string `json:"path"`
		Root string `json:"root"`
	}{{"/private/var/test/a", "/var/test"}, {"/var/test/a", "/private/var/test"}, {"/private/tmp/a", "/tmp"}, {"/tmp/a", "/private/tmp"}, {"/var/a", "/private/var"}, {"/p/a", "/p"}, {"/p", "/p"}, {"/p2/a", "/p"}, {"/p/../../x", "/p"}, {"/p/dir\\..\\x", "/p"}, {"/P/A", "/p"}, {"/ΣΟΣ/x", "/σος"}, {"/ΟΣ/x", "/ος"}, {"/İ/x", "/i\u0307"}, {"/𐐀/x", "/𐐨"}, {"/private/tmp2/a", "/tmp2"}, {"/private/var2/a", "/var2"}, {"/P/CHILD", "/p/child"}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-working-paths.fixture.ts")
	data, _ := json.Marshal(rows)
	home := t.TempDir()
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_WORKING_PATH_FIXTURE=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var expected []bool
	if e := json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		if got := pathInWorkingPath(row.Path, row.Root); got != expected[i] {
			t.Fatalf("%d %s within %s Source=%v Go=%v", i, row.Path, row.Root, expected[i], got)
		}
	}
}
func TestAllowedWorkingRootsKeepSymlinkEscapesAndSessionDirectories(t *testing.T) {
	root := t.TempDir()
	work, external, second := filepath.Join(root, "work"), filepath.Join(root, "outside"), filepath.Join(root, "second")
	for _, dir := range []string{work, external, second} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink(external, filepath.Join(work, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(work, filepath.Join(root, "work-alias")); e != nil {
		t.Fatal(e)
	}
	service := &Service{options: Options{CWD: filepath.Join(root, "work-alias")}}
	ctx := protocol.NewJSONObject(protocol.JSONField{Name: "additionalWorkingDirectories", Value: []byte(`{"map":[]}`)})
	if !service.pathInAllowedWorkingPath(ctx, filepath.Join(work, "a")) {
		t.Fatal("canonical working root denied")
	}
	if service.pathInAllowedWorkingPath(ctx, filepath.Join(work, "escape", "a")) {
		t.Fatal("symlink escape allowed")
	}
	if service.pathInAllowedWorkingPath(ctx, filepath.Join(second, "a")) {
		t.Fatal("unapproved additional root allowed")
	}
	var entries = []any{[]any{second, map[string]any{"path": second, "source": "session"}}}
	data, _ := json.Marshal(entries)
	dirs := protocol.NewJSONObject(protocol.JSONField{Name: "map", Value: data})
	data, _ = dirs.MarshalJSON()
	ctx.Set("additionalWorkingDirectories", data)
	if !service.pathInAllowedWorkingPath(ctx, filepath.Join(second, "a")) {
		t.Fatal("actual session additional directory ignored")
	}
}
