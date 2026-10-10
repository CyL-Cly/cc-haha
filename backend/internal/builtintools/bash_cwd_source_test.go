package builtintools

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeShellCwdOriginalForegroundBackgroundMissingAndUnicodeResultSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Content *string `json:"content"`
		CWD     string  `json:"cwd"`
		Prevent bool    `json:"prevent"`
		Result  any     `json:"result"`
	}
	rows := []row{}
	for _, content := range []string{" /original/new\n", "/original/cwd\n", "/original/é\n", "/original/e\u0301\n"} {
		for _, mode := range []string{"normal", "background", "prevent"} {
			r := row{Content: &content, CWD: "/original/cwd", Result: map[string]any{"code": 0}}
			if mode == "background" {
				r.Result = map[string]any{"backgroundTaskId": "actual-task"}
			}
			if mode == "prevent" {
				r.Prevent = true
			}
			rows = append(rows, r)
		}
	}
	rows = append(rows, row{CWD: "/original/cwd", Result: map[string]any{"code": 1}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-shell-cwd.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SHELL_CWD_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Rows []struct {
			Changed, Hooks [][]string
			Invalidated    int
			Exists         bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, r := range rows {
		path := filepath.Join(home, "actual-cwd-"+string(rune('a'+i)))
		if r.Content != nil {
			if err = os.WriteFile(path, []byte(*r.Content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		execution := bashExecution{Path: path, OriginalCwd: r.CWD}
		cwd := execution.finish()
		changed := [][]string{}
		owner := cooperative.New()
		binding := &BashCwdBindings{Owner: owner, PreventChanges: r.Prevent, Changed: func(_ *cooperative.Scope, old, new string) error {
			changed = append(changed, []string{new, old})
			return nil
		}}
		service := &Service{options: Options{Owner: owner, BashCwd: binding}}
		background := false
		if result, ok := r.Result.(map[string]any); ok {
			background = result["backgroundTaskId"] != nil
		}
		err = owner.Run(t.Context(), func(scope *cooperative.Scope) error { return service.consumeBashCwd(scope, execution, cwd, background) })
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(changed, source.Rows[i].Changed) {
			t.Fatalf("row %d changed %v/%v", i, changed, source.Rows[i].Changed)
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) || source.Rows[i].Exists {
			t.Fatal("Source cwd file cleanup", i, err)
		}
		if len(changed) > 0 {
			if source.Rows[i].Invalidated != 1 || !reflect.DeepEqual(source.Rows[i].Hooks, [][]string{{r.CWD, changed[0][0]}}) {
				t.Fatal(i, source.Rows[i])
			}
		}
	}
}
