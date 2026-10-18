package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func seedTerminalStats(t *testing.T, config string, now time.Time) {
	t.Helper()
	directory := filepath.Join(config, "projects", "private-stats-project")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for index, days := range []int{1, 12} {
		date := now.AddDate(0, 0, -days)
		lines := []any{map[string]any{"type": "user", "timestamp": date.Format(time.RFC3339Nano), "message": map[string]any{"content": "OLD_PRIVATE_SESSION"}}, map[string]any{"type": "assistant", "timestamp": date.Add(5 * time.Minute).Format(time.RFC3339Nano), "message": map[string]any{"model": "fixture-model", "usage": map[string]int{"input_tokens": 120 + index, "output_tokens": 30, "cache_read_input_tokens": 7, "cache_creation_input_tokens": 3}, "content": []any{map[string]string{"type": "text", "text": "PRIVATE_STATS_RESPONSE"}}}}}
		data := []byte{}
		for _, line := range lines {
			data = append(data, raw(line)...)
			data = append(data, '\n')
		}
		if err := os.WriteFile(filepath.Join(directory, []string{"9d488476-ec12-46dc-9b69-472f91a60001.jsonl", "9d488476-ec12-46dc-9b69-472f91a60002.jsonl"}[index]), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestActualTerminalContextEffortStatsPhysicalDataAndNextModel(t *testing.T) {
	var inference, counting atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/messages/count_tokens" {
			counting.Add(1)
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"input_tokens":37}`)
			return
		}
		n := inference.Add(1)
		if strings.Contains(string(body), "/context") || strings.Contains(string(body), "/effort") || strings.Contains(string(body), "/stats") {
			t.Error("local command reached inference", string(body))
		}
		if !bytes.Contains(body, []byte(`"effort":"low"`)) && n == 1 || !bytes.Contains(body, []byte(`"effort":"high"`)) && n == 2 {
			t.Error("saved effort missed next actual native request", n, string(body))
		}
		response(w, "actual-context-stats", "", nil, "NEXT_MODEL_ACTUAL_COMPLETE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
		env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,xhigh_effort"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	seedTerminalStats(t, config, o.Native.Now())
	settings := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"future":{"preserve":37}}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	receipt := filepath.Join(bin, "screenshot.png")
	// The helper receives the exact Source AppleScript argument and resolves
	// its real temporary PNG, rather than substituting a screenshot payload.
	if err := os.WriteFile(filepath.Join(bin, "osascript"), []byte("#!/bin/sh\npath=$(printf '%s' \"$2\" | /usr/bin/sed -n 's/.*POSIX file \"\\([^\"]*\\)\".*/\\1/p')\n/bin/cp \"$path\" \"$SCREENSHOT_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":/usr/bin:/bin"
		env["SCREENSHOT_RECEIPT"] = receipt
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	var panel *tui.LocalPanel
	var output string
	var contextData contextusage.Data
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Kind == tui.LocalCommandOutput {
			output = event.Text
		}
		if event.Kind == tui.EventKind("context_usage_generated") {
			return json.Unmarshal(event.Result, &contextData)
		}
		return nil
	}
	invoke := func(name, args string) {
		t.Helper()
		handled, err := session.InvokeTerminalCommand(context.Background(), name, args, emit)
		if err != nil || !handled {
			t.Fatal(name, handled, err)
		}
	}
	invoke("context", "")
	if counting.Load() == 0 || contextData.RawMax <= 0 || len(contextData.Grid) == 0 || contextData.Model != "fixture-model" {
		t.Fatal("actual analyzer/counter missing", counting.Load(), contextData)
	}
	invoke("effort", "low")
	if output != "Set effort level to low: Quick, straightforward implementation with minimal overhead" {
		t.Fatal(output)
	}
	saved, _ := os.ReadFile(settings)
	if !bytes.Contains(saved, []byte(`"effortLevel": "low"`)) && !bytes.Contains(saved, []byte(`"effortLevel":"low"`)) || !bytes.Contains(saved, []byte(`"preserve"`)) {
		t.Fatal("saved effort/unknown settings missing", string(saved))
	}
	if err = session.Submit(context.Background(), tui.Input{Text: "FIRST_REAL_EFFORT_REQUEST"}, emit); err != nil {
		t.Fatal(err)
	}
	invoke("effort", "high")
	if err = session.Submit(context.Background(), tui.Input{Text: "SECOND_REAL_EFFORT_REQUEST"}, emit); err != nil {
		t.Fatal(err)
	}
	invoke("stats", "")
	if panel == nil {
		t.Fatal("actual stats consumer not opened")
	}
	var presentation tui.StatsPresentation
	_ = json.Unmarshal(panel.Presentation, &presentation)
	if statsNumber(presentation.Stats, "totalSessions") < 2 {
		t.Fatal("private actual transcript stats absent", string(panel.Presentation))
	}
	allDays := len(presentation.AllTime.Get("dailyActivity"))
	id := panel.ID
	if err = session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "stats-range"}, emit); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(panel.Presentation, &presentation)
	if presentation.Range != "7d" || statsNumber(presentation.Stats, "totalSessions") >= statsNumber(presentation.AllTime, "totalSessions") || len(presentation.AllTime.Get("dailyActivity")) != allDays {
		t.Fatal("range actual filter/all-time heatmap changed", string(panel.Presentation))
	}
	if err = session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "stats-tab"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "stats-copy"}, emit); err != nil {
		t.Fatal(err)
	}
	png, err := os.ReadFile(receipt)
	if err != nil || !bytes.HasPrefix(png, []byte{137, 80, 78, 71}) || panel.Notice != "copied!" {
		t.Fatal("actual PNG clipboard child/receipt missing", err, panel.Notice)
	}
	paths, _ := filepath.Glob(filepath.Join(o.Native.Temp, "claude-code-screenshots", "*.png"))
	if len(paths) != 0 {
		t.Fatal("screenshot temporary file not removed", paths)
	}
	if err = session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: "stats-tab"}, emit); err == nil {
		t.Fatal("closed stats ID still admitted")
	}
	invoke("effort", "auto")
	saved, _ = os.ReadFile(settings)
	if bytes.Contains(saved, []byte(`"effortLevel"`)) || !bytes.Contains(saved, []byte(`"preserve"`)) {
		t.Fatal("Source auto deletion lost settings shape", string(saved))
	}
	if inference.Load() != 2 {
		t.Fatal("local commands opened inference", inference.Load())
	}
}
