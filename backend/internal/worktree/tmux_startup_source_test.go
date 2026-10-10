package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualOriginalTmuxWorktreeFastPathArgumentsNestedControlAndErrors(t *testing.T) {
	rows := []map[string]any{}
	for _, classic := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			for _, exists := range []bool{false, true} {
				for _, iterm := range []bool{false, true} {
					flag := "--tmux"
					if classic {
						flag = "--tmux=classic"
					}
					env := map[string]string{"PRIVATE_ENV": "retained"}
					if nested {
						env["TMUX"] = "owned,3,0"
					}
					rows = append(rows, map[string]any{"args": []string{"--worktree=user/a.b", flag, "--model", "actual-model", "prompt"}, "env": env, "exists": exists, "iterm": iterm})
				}
			}
		}
	}
	rows = append(rows, map[string]any{"args": []string{"-w", "sample", "--tmux"}, "platform": "win32"}, map[string]any{"args": []string{"--tmux", "--worktree"}, "missing": true}, map[string]any{"args": []string{"--worktree", "--tmux", "--unknown", "anything"}}, map[string]any{"args": []string{"--worktree", "#12", "--tmux"}, "prefix": "prefix C-a\n"}, map[string]any{"args": []string{"--worktree", "valid", "--tmux"}, "root": false}, map[string]any{"args": []string{"-w", "valid", "--tmux"}, "hook": true, "root": false}, map[string]any{"args": []string{"-w", "valid", "--tmux"}, "hook": true, "hookError": "original hook failed"}, map[string]any{"args": []string{"-w", "valid", "--tmux"}, "createError": "original git failed"}, map[string]any{"args": []string{"-w", "valid", "--tmux"}, "spawnExit": 27}, map[string]any{"args": []string{"-w", "valid", "--tmux"}, "env": map[string]string{"USER_TYPE": "ant"}, "root": "/actual/claude-cli-internal"})
	for _, name := range []string{"../escape", "user/../path", "/absolute", "bad value", "", "a" + strings.Repeat("b", 64), "#0", "#9007199254740993", "#9223372036854775807", "#18446744073709551615", "#1000000000000000000000", "#" + strings.Repeat("9", 309), "https://ghe.invalid/o/r/pull/17?view=1"} {
		if name != "" {
			rows = append(rows, map[string]any{"args": []string{"--tmux", "--worktree=" + name}})
		}
	}
	home := t.TempDir()
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	fixture, _ := filepath.Abs("testdata/tmux-startup.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TMUX_FAST_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []map[string]any
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for index, row := range rows {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			args := row["args"].([]string)
			env, _ := row["env"].(map[string]string)
			if env == nil {
				env = map[string]string{}
			}
			platform, _ := row["platform"].(string)
			if platform == "" {
				platform = "darwin"
			}
			root := "/actual/repo.name"
			if row["root"] == false {
				root = ""
			} else if text, ok := row["root"].(string); ok {
				root = text
			}
			var output bytes.Buffer
			calls := []map[string]any{}
			counter := 0
			value, e := ExecIntoTmuxWorktree(ctx, args, TmuxStartupOptions{Platform: platform, CWD: "/actual/original", Executable: "/actual/go-binary", Environment: env, Output: &output, Random: func() float64 { values := []float64{.1, .4, .125}; v := values[counter]; counter++; return v }, ITerm2: func() bool { return row["iterm"] == true }, CanonicalRoot: func(string) string { return root }, HasCreateHook: func() (bool, error) { return row["hook"] == true, nil }, CreateHook: func(context.Context, string) (string, error) {
				if x, ok := row["hookError"].(string); ok {
					return "", errors.New(x)
				}
				return "/actual/hook", nil
			}, GetOrCreate: func(context.Context, string, Request) (GitWorktree, error) {
				if x, ok := row["createError"].(string); ok {
					return GitWorktree{}, errors.New(x)
				}
				return GitWorktree{Existed: row["worktreeExists"] == true, BaseBranch: "HEAD"}, nil
			}, PostSetup: func(context.Context, string, string) error { return nil }, Run: func(_ context.Context, r TmuxProcessRequest) (ProcessResult, error) {
				call := map[string]any{"command": r.Command, "args": r.Arguments}
				if r.CWD != "/actual/original" {
					call["cwd"] = r.CWD
				}
				if r.Environment["CLAUDE_CODE_TMUX_SESSION"] != "" {
					call["env"] = r.Environment
				}
				if r.Inherit {
					call["inherit"] = true
				}
				calls = append(calls, call)
				if r.Arguments[0] == "-V" && row["missing"] == true {
					return ProcessResult{Code: 1}, nil
				}
				if r.Arguments[0] == "show-options" {
					p, _ := row["prefix"].(string)
					if p == "" {
						p = "prefix C-b\n"
					}
					return ProcessResult{Stdout: p}, nil
				}
				if r.Arguments[0] == "has-session" {
					code := 1
					if row["exists"] == true {
						code = 0
					}
					return ProcessResult{Code: code}, nil
				}
				return ProcessResult{}, nil
			}})
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(map[string]any{"value": map[string]any{"handled": value.Handled}, "calls": calls, "output": output.String()})
			if value.Error != "" {
				raw, _ = json.Marshal(map[string]any{"value": map[string]any{"handled": false, "error": value.Error}, "calls": calls, "output": output.String()})
			}
			var got any
			json.Unmarshal(raw, &got)
			if !reflect.DeepEqual(got, source[index]) {
				t.Fatalf("actual original fastpath mismatch Go%s Source%v", raw, source[index])
			}
		})
	}
}
