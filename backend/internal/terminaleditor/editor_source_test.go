package terminaleditor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceEditorSelectionPhysicalExecutablesNamesAndMemoization(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	if err = os.WriteFile(filepath.Join(home, "code"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "relativebin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "relativebin", "vi"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for i, commands := range [][]string{{}, {"nano"}, {"vi", "nano"}, {"code", "vi", "nano"}} {
		dir := filepath.Join(home, string(rune('a'+i)))
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, dir)
		for _, name := range commands {
			if err = os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows := []map[string]any{}
	for _, path := range []string{"", ":", "relativebin", ":relativebin", "relativebin:", ".", "./relativebin"} {
		rows = append(rows, map[string]any{"env": map[string]string{"PATH": path}, "platform": "darwin"})
	}
	for _, platform := range []string{"darwin", "linux", "win32"} {
		for _, path := range paths {
			for _, values := range [][2]string{{"", ""}, {" \ufeff\u3000 ", " nano --flag "}, {" custom --wait ", "vi"}} {
				rows = append(rows, map[string]any{"env": map[string]string{"PATH": path, "VISUAL": values[0], "EDITOR": values[1]}, "platform": platform})
			}
		}
	}
	names := []string{"", "code", "CODE", "vscode", "VSCODE", "intellij", "pycharm", "PyCharm", "/private/tools/code --wait", "/private/Tools/MyEDITOR --flag", "nano", "subl", "start /wait notepad", "unknown --args", " 中文 😀"}
	fixture, _ := filepath.Abs("testdata/editor.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_EDITOR_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows, "names": names})
	command.Stdin = bytes.NewReader(input)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual editor Source %v %.3000s", err, data)
	}
	var source struct {
		Rows  []struct{ Selected, Retained string }
		Names []string
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("invalid Source %v %.3000s", err, data)
	}
	for index, row := range rows {
		env := row["env"].(map[string]string)
		actual := Select(env, row["platform"].(string), os.Stat, home)
		if actual != source.Rows[index].Selected || source.Rows[index].Selected != source.Rows[index].Retained {
			t.Fatalf("row%d selected%q Source%+v", index, actual, source.Rows[index])
		}
	}
	for index, name := range names {
		if actual := DisplayName(name); actual != source.Names[index] {
			t.Errorf("display %q Go%q Source%q", name, actual, source.Names[index])
		}
	}
}
