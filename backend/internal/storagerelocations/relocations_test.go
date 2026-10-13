package storagerelocations

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOldMetadataAndCopiedAttachmentsPreserveUnknownFieldsAndRejectInvalid(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "absent")
	current := filepath.Join(root, "current")
	input := filepath.Join(old, "uploads", "session", "a.png")
	result, err := Resolve(input, current)
	if err != nil || result != input {
		t.Fatal(result, err)
	}
	if err := os.MkdirAll(filepath.Join(current, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(current, "cc-haha", "storage-relocations.json")
	data, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{old}, "futureOwner": true})
	if err := os.WriteFile(metadata, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = Resolve(input, current)
	if err != nil || result != filepath.Join(current, "uploads", "session", "a.png") {
		t.Fatal(result, err)
	}
	parsed, err := Read(current, "darwin")
	if err != nil || string(parsed.Document["futureOwner"]) != "true" {
		t.Fatal(parsed, err)
	}
	for _, invalid := range []string{`{`, `null`, `[]`, `{"version":2,"previousRoots":[]}`, `{"version":1,"previousRoots":["relative"]}`, `{"version":1,"previousRoots":null}`} {
		if err := os.WriteFile(metadata, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(input, current); err == nil {
			t.Fatal("invalid metadata fell back to old root", invalid)
		}
	}
}

func TestManagedMapMatchesActualMainOnPOSIXWindowsUNCAndUnicode(t *testing.T) {
	type profile struct {
		Input, Config, Platform string
		Roots                   []string
	}
	rows := []profile{}
	for _, prefix := range ManagedPrefixes {
		rows = append(rows, profile{"/old/" + prefix + "/session/a.png", "/new", "darwin", []string{"/older", "/old"}})
	}
	for _, input := range []string{"/old/projects/repo/file", "/old/settings.json", "/oldish/uploads/a.png", "/old/uploads/../settings.json", "/project/image.png", "uploads/a.png", "/old/uploads", "/old/uploads/", "/old/uploads/session/", "/old/uploads/../uploads/a.png", "/old/uploads/e\u0301.png", "/old/uploads/\x00.png"} {
		rows = append(rows, profile{input, "/new", "darwin", []string{"/old"}})
	}
	for _, input := range []string{`c:/OLD/Uploads/session/a.png`, `C:\old2\uploads\a.png`, `C:\old\uploads\..\settings.json`, `C:\old\cc-haha\Generated-Images\a.png`, `C:\old\Uploads\目录\a.png`} {
		rows = append(rows, profile{input, `D:\data`, "win32", []string{`C:\old`}})
	}
	rows = append(rows, profile{`\\Server\Share\Old\Uploads\a.png`, `E:\new`, "win32", []string{`\\server\share\old`}}, profile{`\\Server\Other\Old\Uploads\a.png`, `E:\new`, "win32", []string{`\\server\share\old`}})
	actual := []string{}
	for _, row := range rows {
		actual = append(actual, Map(row.Input, row.Config, row.Roots, row.Platform))
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual MAIN comparator requires pinned Bun")
	}
	source, _ := filepath.Abs("../../../src/utils/storageRelocations.ts")
	script := `import {mapRelocatedAttachmentPath} from ` + string(mustJSON(source)) + `; const rows=JSON.parse(await Bun.stdin.text());console.log(JSON.stringify(rows.map(r=>mapRelocatedAttachmentPath(r.Input,r.Config,r.Roots,r.Platform))))`
	cmd := exec.Command(bun, "--no-env-file", "-e", script)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"HOME=" + cmd.Dir, "CLAUDE_CONFIG_DIR=" + cmd.Dir, "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(mustJSON(rows))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err, string(output))
	}
	if !reflect.DeepEqual(actual, expected) {
		for i, row := range rows {
			if actual[i] != expected[i] {
				t.Errorf("%+v Go=%q MAIN=%q", row, actual[i], expected[i])
			}
		}
	}
}
func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
