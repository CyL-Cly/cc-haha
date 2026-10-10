package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceModelPickerNavigationDuplicateValuesAndSentinel(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires explicitly supplied Bun 1.3.14")
	}
	type profile struct {
		Values  []*string `json:"values"`
		Initial *string   `json:"initial"`
		Actions []string  `json:"actions"`
	}
	ptr := func(value string) *string { return &value }
	actions := []string{"focus-next-option", "focus-previous-option", "focus-next-page", "focus-next-option", "focus-previous-page", "focus-previous-option", "focus-next-option"}
	for range 15 {
		actions = append(actions, "focus-next-option")
	}
	for range 15 {
		actions = append(actions, "focus-previous-option")
	}
	var values []*string
	for i := range 13 {
		values = append(values, ptr(fmt.Sprintf("model-%d", i)))
	}
	profiles := []profile{
		{Values: values, Initial: values[11], Actions: actions},
		{Values: append(append([]*string(nil), values...), values[2]), Initial: values[2], Actions: actions},
		{Values: append(append([]*string(nil), values...), values[11]), Initial: values[11], Actions: actions},
		{Values: []*string{nil, ptr("__NO_PREFERENCE__"), ptr("other")}, Actions: actions},
		{Values: []*string{ptr("duplicate"), ptr("duplicate"), ptr("duplicate")}, Initial: ptr("duplicate"), Actions: actions},
	}
	home := t.TempDir()
	for _, leaf := range []string{"tmp", "config", "cache"} {
		if err := os.Mkdir(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("testdata/model_picker_navigation.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"home": home, "cases": profiles})
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=fixture", "CC_HAHA_MODEL_PICKER_NAVIGATION_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1"}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("original select navigation: %v %s", err, stderr.String())
	}
	type snapshot struct {
		Focused, From, To    int
		Highlighted, Visible []int
	}
	var source struct {
		Rows         [][]snapshot
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Capabilities.Blocked) > 0 || len(source.Rows) != len(profiles) {
		t.Fatal("original fixture escaped boundary", string(output))
	}
	for i, profile := range profiles {
		s := NewState(nil, 80, 24)
		var options []ModelOption
		for _, value := range profile.Values {
			options = append(options, ModelOption{Value: value, Label: "fixture", DefaultEffort: "high"})
		}
		s.OpenModelPicker(ModelPicker{Options: options, Initial: profile.Initial})
		check := func(step int) {
			p := s.ModelPicker
			got := snapshot{Focused: p.focused, From: p.from, To: p.to, Highlighted: []int{}, Visible: []int{}}
			for index, option := range p.props.Options {
				if sameModel(option.Value, p.props.Options[p.focused].Value) {
					got.Highlighted = append(got.Highlighted, index)
				}
			}
			from, to := p.from, p.to
			if from < 0 {
				from = max(0, len(options)+from)
			}
			if to < 0 {
				to = max(0, len(options)+to)
			}
			for index := min(from, len(options)); index < min(to, len(options)); index++ {
				got.Visible = append(got.Visible, index)
			}
			if !reflect.DeepEqual(got, source.Rows[i][step]) {
				t.Fatalf("profile %d step %d Go=%+v Source=%+v", i, step, got, source.Rows[i][step])
			}
			_ = p.lines(80)
		}
		check(0)
		for step, action := range profile.Actions {
			switch action {
			case "focus-next-option":
				s.ModelPicker.navigate(1, false)
			case "focus-previous-option":
				s.ModelPicker.navigate(-1, false)
			case "focus-next-page":
				s.ModelPicker.navigate(1, true)
			case "focus-previous-page":
				s.ModelPicker.navigate(-1, true)
			}
			check(step + 1)
		}
	}
	// Original NO_PREFERENCE collision returns null, including numeric selection.
	s := NewState(nil, 80, 24)
	s.OpenModelPicker(ModelPicker{ID: "sentinel", Options: []ModelOption{{Label: "Default"}, {Value: ptr("__NO_PREFERENCE__"), Label: "Current"}}})
	result := s.modelPickerResult(1, false)
	if len(result) != 1 || result[0].modelPickerResult.Model != nil {
		t.Fatal("Source sentinel callback was not null", result)
	}
}
