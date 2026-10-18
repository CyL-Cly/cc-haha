package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualSourceFileEditorPhysicalArgvOriginalFileFailuresAndWaitFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("original physical POSIX command; Windows machine required")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$@" > "$EDITOR_ARGUMENTS"
last=''
for arg do last="$arg"; done
if [ "$EDITOR_FAIL" = 1 ]; then exit 17; fi
printf '%s' "$EDITOR_CONTENT" > "$last"
`
	for _, name := range []string{"code", "subl", "custom"} {
		if err = os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rows := []map[string]any{}
	for i, editor := range []string{"code", "subl", "custom --flag", filepath.Join(bin, "custom") + " --exact", "custom", "", "custom", "missing-private-editor"} {
		path := filepath.Join(home, "plan with spaces "+string(rune('a'+i))+".md")
		env := map[string]string{"PATH": bin + ":/usr/bin:/bin", "EDITOR_ARGUMENTS": filepath.Join(home, string(rune('a'+i))+"-argv"), "EDITOR_CONTENT": "edited 中文 😀\n\n", "EDITOR_FAIL": "0"}
		if i == 4 {
			env["EDITOR_FAIL"] = "1"
		}
		rows = append(rows, map[string]any{"path": path, "editor": editor, "env": env, "missing": i == 6})
	}
	rows = append(rows, map[string]any{"path": "relative plan 中文.md", "editor": "custom", "env": map[string]string{"PATH": bin + ":/usr/bin:/bin", "EDITOR_ARGUMENTS": filepath.Join(home, "relative-argv"), "EDITOR_CONTENT": "relative actual editor bytes\n", "EDITOR_FAIL": "0"}, "missing": false})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("testdata/file-editor.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FILE_EDITOR_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	command.Stdin = bytes.NewReader(input)
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	data, err := command.Output()
	if err != nil {
		t.Fatalf("actual file editor Source %v %.4000s", err, string(data)+diagnostic.String())
	}
	var source struct {
		Rows []struct {
			Result struct {
				Content *string
				Error   string
			}
			After     *string
			Argv      string
			Lifecycle []string
			Commands  []string
		}
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("invalid Source physical evidence %v %.3000s", err, data)
	}
	for index, row := range rows {
		path, editor := row["path"].(string), row["editor"].(string)
		physical := path
		if !filepath.IsAbs(physical) {
			physical = filepath.Join(home, physical)
		}
		env := row["env"].(map[string]string)
		_ = os.Remove(env["EDITOR_ARGUMENTS"])
		if row["missing"].(bool) {
			_ = os.Remove(physical)
		} else if err = os.WriteFile(physical, []byte("ORIGINAL_PLAN_BYTES"), 0600); err != nil {
			t.Fatal(err)
		}
		values := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")}
		for key, value := range env {
			values = append(values, key+"="+value)
		}
		execution := &fileEditorExecution{ctx: ctx, request: FileEditorRequest{Path: path, Editor: editor, Environment: values, Directory: home}, stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
		if err = execution.Run(); err != nil || execution.result.Error != source.Rows[index].Result.Error {
			t.Errorf("row%d result%+v Source%+v failure%v", index, execution.result, source.Rows[index].Result, err)
		}
		actual, e := os.ReadFile(physical)
		if source.Rows[index].After == nil {
			if !os.IsNotExist(e) {
				t.Fatal("missing actual file recreated", index, e)
			}
		} else if e != nil || base64.StdEncoding.EncodeToString(actual) != *source.Rows[index].After {
			t.Fatal("original file bytes differ", index, e)
		}
		argv, _ := os.ReadFile(env["EDITOR_ARGUMENTS"])
		if string(argv) != source.Rows[index].Argv {
			t.Errorf("row%d actualargv%q Source%q", index, argv, source.Rows[index].Argv)
		}
	}
}
