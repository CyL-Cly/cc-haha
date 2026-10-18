package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalExpandedViewActualRootStoreCallbackGlobalSaveAndOldStartupValues(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned mounted original root consumer required")
	}
	rows := []map[string]any{
		{"help": true, "fullscreen": false, "restoreExpanded": true, "global": map[string]any{"showExpandedTodos": false, "showSpinnerTree": false, "futureRootView": map[string]any{"keep": true}}, "steps": []string{"\x14", "\x14"}},
		{"help": true, "fullscreen": false, "restoreExpanded": true, "global": map[string]any{"showExpandedTodos": "legacy", "showSpinnerTree": false, "futureRootView": map[string]any{"keep": true}}, "steps": []string{"\x14"}},
		{"help": true, "fullscreen": true, "restoreExpanded": true, "global": map[string]any{"showExpandedTodos": true, "showSpinnerTree": true, "futureRootView": map[string]any{"keep": true}}, "steps": []string{"\x14"}},
		{"help": true, "fullscreen": true, "busy": true, "restoreExpanded": true, "global": map[string]any{"showExpandedTodos": false, "showSpinnerTree": false, "futureRootView": map[string]any{"keep": true}}, "steps": []string{"\x14"}},
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-root-shortcuts.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ROOT_SHORTCUTS_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3", "PATH=/usr/bin:/bin", "USER_TYPE=ant"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("Original root store callback", err, string(output))
	}
	var source struct {
		Results [][]struct {
			ExpandedView   string
			GlobalExpanded protocol.JSONObject
		}
		Blocked []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.Interactive = true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, row := range rows {
		if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			value := current.Clone()
			for name, raw := range row["global"].(map[string]any) {
				value.Set(name, rawMain(raw))
			}
			return &value, nil
		}); err != nil {
			t.Fatal(err)
		}
		for j, frame := range source.Results[i] {
			if j > 0 {
				if err = m.SetTerminalExpandedView(ctx, frame.ExpandedView); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := m.TerminalExpandedView(ctx)
			global, readErr := m.Global.Get()
			if err != nil || readErr != nil || actual != frame.ExpandedView {
				t.Fatal("Actual old startup values", i, j, actual, frame.ExpandedView, err, readErr)
			}
			for _, key := range []string{"showExpandedTodos", "showSpinnerTree", "futureRootView"} {
				var actual, expected bytes.Buffer
				if err := json.Compact(&actual, global.Get(key)); err != nil {
					t.Fatal(err)
				}
				if err := json.Compact(&expected, frame.GlobalExpanded.Get(key)); err != nil {
					t.Fatal(err)
				}
				if actual.String() != expected.String() {
					t.Fatal("Original onChangeAppState global write", i, j, key, string(global.Get(key)), string(frame.GlobalExpanded.Get(key)))
				}
			}
		}
	}
	before, _ := m.Global.Get()
	if err = m.SetTerminalExpandedView(ctx, "foreign"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Invalid expanded view admitted", err)
	}
	after, _ := m.Global.Get()
	if !reflect.DeepEqual(rawMain(*before), rawMain(*after)) {
		t.Fatal("Rejected value mutated actual global config")
	}
}

func TestTerminalExpandedViewCommittedSaveDuringActualHeldModelUsesSameOwner(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(entered)
		select {
		case <-release:
			emitPlanFixture(w, 1, "", "EXPANDED_VIEW_HELD_DONE")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Interactive = true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Actual held expanded-view parent"})))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if err := m.SetTerminalExpandedView(writeCtx, "tasks"); err != nil {
		t.Fatal("Committed view save waited behind unrelated model HTTP", err)
	}
	if value, err := m.TerminalExpandedView(writeCtx); err != nil || value != "tasks" {
		t.Fatal(value, err)
	}
	select {
	case err := <-done:
		t.Fatal("Expanded view save canceled or completed held model", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
