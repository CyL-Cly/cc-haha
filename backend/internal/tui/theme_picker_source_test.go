package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type actualThemeCase struct {
	Name             string           `json:"name"`
	Theme            string           `json:"theme"`
	User             map[string]any   `json:"user"`
	SyntaxEnv        *string          `json:"syntaxEnv"`
	Width            int              `json:"width"`
	AutoBuildFeature bool             `json:"autoBuildFeature"`
	Steps            []map[string]any `json:"steps"`
}

func actualThemeFixture(t *testing.T, cases []actualThemeCase) []protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual frozen Source Theme fixture requires pinned Bun")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-theme-picker.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_THEME_PICKER_SOURCE=1", "CC_HAHA_THEME_PICKER_TRACE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Theme Source %v %.3000s", err, stderr.String())
	}
	var result []protocol.JSONObject
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatalf("actual Source response %v %.600s", err, data)
	}
	return result
}
func TestThemePickerActualSourcePreviewSyntaxModuleGatesAndFallbackRows(t *testing.T) {
	cases := []actualThemeCase{}
	for _, option := range TerminalThemeOptions(false) {
		for _, width := range []int{1, 7, 13, 24, 38, 80} {
			cases = append(cases, actualThemeCase{Name: option.ID, Theme: option.ID, User: map[string]any{}, Width: width, Steps: []map[string]any{}})
		}
	}
	cases = append(cases, actualThemeCase{Name: "optional-auto-build-feature", Theme: "dark", User: map[string]any{}, Width: 80, AutoBuildFeature: true, Steps: []map[string]any{}})
	plain := func(name string, env *string, steps []map[string]any) {
		cases = append(cases, actualThemeCase{Name: name, Theme: "dark", User: map[string]any{"futureUser": true}, SyntaxEnv: env, Width: 80, Steps: steps})
	}
	plain("focus-only", nil, []map[string]any{{"action": "focus", "theme": "light"}})
	plain("toggle-cancel", nil, []map[string]any{{"action": "focus", "theme": "light"}, {"action": "toggle"}, {"action": "cancel"}})
	plain("accept", nil, []map[string]any{{"action": "focus", "theme": "light"}, {"action": "accept", "theme": "light"}})
	for _, value := range []string{"0", "false", " NO ", "OFF", "\ufeff false \ufeff", "", "true", "monokai"} {
		env := value
		plain("env/"+value, &env, []map[string]any{{"action": "toggle"}})
	}
	source := actualThemeFixture(t, cases)
	if len(source) != len(cases) {
		t.Fatal("Source cases missing")
	}
	for index, row := range source {
		item := cases[index]
		t.Run(item.Name+"/"+strconv.Itoa(item.Width), func(t *testing.T) {
			var options []struct{ Label, Value string }
			if err := json.Unmarshal(row.Get("options"), &options); err != nil {
				t.Fatal(err)
			}
			expected := TerminalThemeOptions(item.AutoBuildFeature)
			if len(options) != len(expected) {
				t.Fatal(options)
			}
			for i, option := range options {
				if option.Value != expected[i].ID || option.Label != expected[i].Label {
					t.Fatal(options, expected)
				}
			}
			if !bytes.HasSuffix(row.Get("moduleResolution"), []byte(`/stubs/color-diff-napi.ts"`)) {
				t.Fatal("different actual Source color profile", string(row.Get("moduleResolution")))
			}
			if string(row.Get("stubRender")) != "null" {
				t.Fatal("shipped Source renderer changed", string(row.Get("stubRender")))
			}
			var rows [][]TerminalThemeSpan
			if err := json.Unmarshal(row.Get("styledRows"), &rows); err != nil {
				t.Fatal(err)
			}
			theme := item.Theme
			var observations []protocol.JSONObject
			_ = json.Unmarshal(row.Get("observations"), &observations)
			if len(observations) > 0 {
				theme = observations[len(observations)-1].String("current")
			}
			got := TerminalThemePreviewStyledRows(theme, item.Width)
			if !reflect.DeepEqual(got, rows) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(rows)
				t.Fatalf("actual Source fallback differs width%d theme%s\nGo %.2500s\nSource %.2500s", item.Width, theme, a, b)
			}
			palette := presentationObject(row.Get("palette"))
			for role, value := range terminalThemeRoles[theme] {
				if palette.String(role) != value {
					t.Fatal(theme, role, value, palette.String(role))
				}
			}
			for _, spanRow := range got {
				for _, span := range spanRow {
					if TerminalThemeRoleEscape(theme, span.Color, false) == "" {
						t.Fatal("unconsumed Source foreground role", theme, span)
					}
				}
			}
			if len(observations) > 0 {
				if item.Name == "focus-only" {
					if observations[0].String("saved") != "dark" || observations[0].String("current") != "light" || string(observations[0].Get("saveCalls")) != "[]" {
						t.Fatal("Source preview writes", observations)
					}
				}
				if item.Name == "accept" {
					var saves []string
					_ = json.Unmarshal(observations[len(observations)-1].Get("saveCalls"), &saves)
					if !reflect.DeepEqual(saves, []string{"light", "light"}) {
						t.Fatal("actual savePreview and callback persistence", saves)
					}
				}
			}
		})
	}
}
