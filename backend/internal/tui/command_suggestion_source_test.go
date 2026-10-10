package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCommandSelectionEnterAndTabAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type scenario struct {
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		Args    []string `json:"args"`
		Execute bool     `json:"execute"`
	}
	cases := []scenario{}
	for _, name := range []string{"cost", "plugin:skill", "测试😀"} {
		for _, kind := range []string{"local", "local-jsx", "prompt"} {
			for _, args := range [][]string{{}, {"required", "optional"}} {
				for _, execute := range []bool{false, true} {
					cases = append(cases, scenario{name, kind, args, execute})
				}
			}
		}
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	fixture, _ := filepath.Abs("testdata/command_suggestion.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_COMMAND_SUGGESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	hintArgs := []string{"", "one ", "one two ", "one two three ", "\"two words\" ", "'two words' x ", "$FOO ${BAR} ", "a\\ b ", "* ", "hello #comment ", "a | b ", "${broken "}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": cases, "hints": hintArgs})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source command selection %v %.4000s", e, out)
	}
	var source struct {
		Hints []*string
		Rows  []struct {
			Inputs    []string
			Submitted []struct {
				Text  string
				Slash bool
			}
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	if len(source.Hints) != len(hintArgs) {
		t.Fatal("original Source hints missing")
	}
	for i, args := range hintArgs {
		s := NewState([]Completion{{Name: "owned", Kind: "prompt", ArgumentNames: []string{"target", "region", "extra"}}}, 100, 30)
		input := "/owned " + args
		// A hint is only rendered at a trailing space. The Source helpers see
		// exactly the argument substring, including its quotes/operators.
		if input[len(input)-1] != ' ' {
			input += " "
		}
		s.Editor.Set(input)
		expected := ""
		if source.Hints[i] != nil {
			expected = *source.Hints[i]
		}
		if actual := s.commandArgumentHint(input); actual != expected {
			t.Errorf("hint%d %q Go%q Source%q", i, args, actual, expected)
		}
	}
	for i, row := range cases {
		s := NewState([]Completion{{Name: row.Name, Kind: row.Kind, RequiresArguments: len(row.Args) > 0}}, 100, 30)
		s.Editor.Set("/partial")
		s.Completions = append([]Completion{}, s.commands...)
		key := "tab"
		if row.Execute {
			key = "enter"
		}
		actions := s.HandleKey(Key{Name: key}, time.Now())
		if !reflect.DeepEqual(source.Rows[i].Inputs, []string{"/" + row.Name + " "}) {
			t.Fatal("Source formatted choice changed", source.Rows[i])
		}
		if len(source.Rows[i].Submitted) > 0 {
			if len(actions) != 1 || actions[0].kind != actionSubmit || actions[0].input.Text != source.Rows[i].Submitted[0].Text || !source.Rows[i].Submitted[0].Slash {
				t.Errorf("case%d Source%v Go%v", i, source.Rows[i], actions)
			}
		} else if len(actions) != 0 || s.Editor.Text() != source.Rows[i].Inputs[0] {
			t.Errorf("case%d must retain original editable args Source%v Go%v/%q", i, source.Rows[i], actions, s.Editor.Text())
		}
	}
}
