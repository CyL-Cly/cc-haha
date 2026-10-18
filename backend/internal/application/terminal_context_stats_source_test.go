package application

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/ansi"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceTerminalContextStatsRegistrationPNGAndRange(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-context-stats.fixture.ts")
	images := []string{"", "Actual Stats\nSessions: 37\n/stats", "\x1b[38;2;215;119;87mClaude · Tokens: 120k\x1b[0m\n\x1b[1m● Max ░▒▓█\x1b[0m\n\x1b[38;5;208mα😀\u0301\x1b[0m", "\x1b[31mred\x1b[0m\x1b[34mblue\n\x1b[90m───┼───\x1b[0m\n\n"}
	stats := json.RawMessage(`{"totalSessions":2,"totalDays":3,"activeDays":2,"longestSession":{"duration":300000},"streaks":{"currentStreak":1,"longestStreak":2},"peakActivityHour":7,"peakActivityDay":"2026-10-12","dailyActivity":[{"date":"2026-10-11","messageCount":7},{"date":"2026-10-12","messageCount":10}],"dailyModelTokens":[{"date":"2026-10-11","tokensByModel":{"fixture-model":33,"other":2}},{"date":"2026-10-12","tokensByModel":{"fixture-model":27,"other":6}}],"modelUsage":{"other":{"inputTokens":7,"outputTokens":1},"fixture-model":{"inputTokens":41,"outputTokens":19}}}`)
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "TZ=UTC", "FORCE_COLOR=3", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONTEXT_STATS_FIXTURE=1"}
	effort := []any{map[string]any{"args": "low", "settings": map[string]any{"future": 37}}, map[string]any{"args": "xhigh", "settings": map[string]any{"future": 37, "effortLevel": "low"}}, map[string]any{"args": "auto", "settings": map[string]any{"future": 37, "effortLevel": "high"}}, map[string]any{"args": "medium", "env": "high", "settings": map[string]any{"future": 37}}}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "images": images, "effort": effort, "stats": []any{stats}, "now": "2026-10-14T00:00:00.000Z", "render": []any{map[string]any{"stats": stats, "tab": "Overview"}, map[string]any{"stats": stats, "tab": "Models"}}}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		PNG                                       []string
		Screenshots                               []string
		Ranges                                    []string
		ContextInteractive, ContextNonInteractive bool
		Registrations                             []tui.Completion
		Stats                                     []struct {
			Models      []string
			TotalTokens float64
		}
		Rows []struct {
			Result   struct{ Message string }
			Settings protocol.JSONObject
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if !source.ContextInteractive || source.ContextNonInteractive || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 || !reflect.DeepEqual(source.Ranges, []string{"7d", "30d", "all"}) {
		t.Fatal("Source gate/range/boundary mismatch", source)
	}
	registrations := terminalContextStatsCommands()
	if len(source.Registrations) != len(registrations) {
		t.Fatal(source.Registrations)
	}
	for index, command := range registrations {
		want := source.Registrations[index]
		if command.Name != want.Name || command.Kind != want.Kind && want.Kind != "" || command.Description != want.Description || command.ArgumentHint != want.ArgumentHint {
			t.Fatal("original registration differs", command, want)
		}
	}
	var data protocol.JSONObject
	_ = data.UnmarshalJSON(stats)
	clock, _ := time.Parse(time.RFC3339Nano, "2026-10-14T00:00:00.000Z")
	for _, tab := range []string{"Overview", "Models"} {
		images = append(images, tui.StatsScreenshotANSI(tui.StatsPresentation{Stats: data, AllTime: data, Range: "all", Tab: tab, Now: clock}))
	}
	for index, text := range images {
		want, err := base64.StdEncoding.DecodeString(source.PNG[index])
		if err != nil {
			t.Fatal(err)
		}
		got, err := tui.StatsScreenshotPNG(text)
		if err != nil {
			t.Fatal(err)
		}
		left, err := png.Decode(bytes.NewReader(want))
		if err != nil {
			t.Fatal(err)
		}
		right, err := png.Decode(bytes.NewReader(got))
		if err != nil {
			t.Fatal(err)
		}
		if left.Bounds() != right.Bounds() {
			t.Fatal("Source PNG dimensions", index, left.Bounds(), right.Bounds())
		}
		for y := left.Bounds().Min.Y; y < left.Bounds().Max.Y; y++ {
			for x := left.Bounds().Min.X; x < left.Bounds().Max.X; x++ {
				lr, lg, lb, la := left.At(x, y).RGBA()
				rr, rg, rb, ra := right.At(x, y).RGBA()
				if lr != rr || lg != rg || lb != rb || la != ra {
					t.Fatalf("Source PNG pixel mismatch row %d (%d,%d): %d,%d,%d,%d / %d,%d,%d,%d", index, x, y, lr, lg, lb, la, rr, rg, rb, ra)
				}
			}
		}
	}
	for index, tab := range []string{"Overview", "Models"} {
		actual := tui.StatsScreenshotANSI(tui.StatsPresentation{Stats: data, AllTime: data, Range: "all", Tab: tab, Now: clock})
		if ansi.Strip(actual) != ansi.Strip(source.Screenshots[index]) {
			t.Errorf("Source actual screenshot renderer %s differs\nGo:\n%s\nSource:\n%s", tab, ansi.Strip(actual), ansi.Strip(source.Screenshots[index]))
		}
	}
	models, total := terminalStatsModels(data)
	if total != source.Stats[0].TotalTokens || models[0].Name != source.Stats[0].Models[0] {
		t.Fatal("original stats model sorting/total differs", models, total, source.Stats)
	}
	messages := []string{"Set effort level to low: Quick, straightforward implementation with minimal overhead", "Set effort level to xhigh (this session only): Extra-high reasoning for especially complex tasks", "Effort level set to auto", "CLAUDE_CODE_EFFORT_LEVEL=high overrides this session — clear it and medium takes over"}
	for index, row := range source.Rows {
		if row.Result.Message != messages[index] || string(row.Settings.Get("future")) != "37" {
			t.Fatal("actual Source effort saved-state/message", index, row)
		}
	}
}
