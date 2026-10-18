package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalConfigExternalImportsActualDiscoveryProjectSaveClearAndNextHarness(t *testing.T) {
	var systems []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		systems = append(systems, string(body))
		response(w, "external-import", "", nil, "EXTERNAL_IMPORT_ACTUAL_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.SystemPrompt, o.Interactive = "", true
	external := filepath.Join(filepath.Dir(o.WorkDir), "outside-instructions.md")
	_ = os.WriteFile(external, []byte("EXTERNAL_CONFIG_ACTUAL_INSTRUCTIONS_572"), 0600)
	_ = os.WriteFile(filepath.Join(o.WorkDir, "CLAUDE.md"), []byte("PROJECT_CONFIG_ACTUAL_INSTRUCTIONS_572\n@"+external+"\n"), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err := s.process.Global.SaveProject(o.WorkDir, func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("futureProject", raw(map[string]any{"keep": true}))
		return &next, nil
	}); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := terminalPanelProjection(*event.LocalPanel)
			panel = &copy
		}
		return nil
	}
	submit := func(text string) {
		t.Helper()
		if err := s.Submit(context.Background(), tui.Input{Text: text, Mode: "prompt"}, emit); err != nil {
			t.Fatal(err)
		}
	}
	act := func(action, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row}, emit); err != nil {
			t.Fatal(err)
		}
	}
	submit("first original cached memory")
	if _, err := s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range panel.Rows {
		found = found || row.ID == "showExternalIncludesDialog" && strings.HasSuffix(row.Label, "false")
	}
	if !found {
		t.Fatal("real forced external-file discovery did not admit Config row", panel.Rows)
	}
	act("edit-config", "showExternalIncludesDialog")
	if !panel.Submenu || panel.DismissAction != "config-external-disable" || !strings.Contains(panel.Description, external) || len(panel.Rows) != 2 {
		t.Fatal(panel)
	}
	act("config-external-select", "yes")
	act("close", "")
	project, err := s.process.Global.GetProject(o.WorkDir)
	if err != nil || string(project.Get("hasClaudeMdExternalIncludesApproved")) != "true" || string(project.Get("hasClaudeMdExternalIncludesWarningShown")) != "true" || string(obj(project.Get("futureProject")).Get("keep")) != "true" {
		t.Fatal(project, err)
	}
	submit("second original cache remains before clear")
	submit("/clear")
	submit("third fresh cache consumes approved external instructions")
	if _, err := s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
		t.Fatal(err)
	}
	act("edit-config", "showExternalIncludesDialog")
	act("config-external-disable", "")
	act("next-config", "verbose")
	act("close", "")
	project, err = s.process.Global.GetProject(o.WorkDir)
	if err != nil || string(project.Get("hasClaudeMdExternalIncludesApproved")) != "true" {
		t.Fatal("dirty Config Escape did not restore original project approval", project, err)
	}
	if _, err := s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
		t.Fatal(err)
	}
	act("edit-config", "showExternalIncludesDialog")
	act("config-external-disable", "")
	act("commit-config", "")
	submit("/new")
	submit("fourth fresh cache excludes disabled external instructions")
	if len(systems) != 4 {
		t.Fatal("local dialogs/clear reached the model", len(systems))
	}
	for i, text := range systems {
		if !strings.Contains(text, "PROJECT_CONFIG_ACTUAL_INSTRUCTIONS_572") || strings.Contains(text, "EXTERNAL_CONFIG_ACTUAL_INSTRUCTIONS_572") != (i == 2) {
			t.Errorf("request %d original memory consumer: project=%v external=%v expected external=%v", i, strings.Contains(text, "PROJECT_CONFIG_ACTUAL_INSTRUCTIONS_572"), strings.Contains(text, "EXTERNAL_CONFIG_ACTUAL_INSTRUCTIONS_572"), i == 2)
		}
	}
	project, err = s.process.Global.GetProject(o.WorkDir)
	if err != nil || string(project.Get("hasClaudeMdExternalIncludesApproved")) != "false" || string(project.Get("hasClaudeMdExternalIncludesWarningShown")) != "true" || string(obj(project.Get("futureProject")).Get("keep")) != "true" {
		t.Fatal(project, err)
	}
}

func TestTerminalConfigExternalIncludesActualOriginalForcedGatePathsAndApprovalCallbacks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source Bun required")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "work")
	rows := [][]map[string]string{
		{},
		{{"type": "Project", "path": filepath.Join(home, "outside.md"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "User", "path": filepath.Join(home, "outside.md"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "Project", "path": filepath.Join(home, "outside.md")}},
		{{"type": "Project", "path": filepath.Join(cwd, "nested.md"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "Local", "path": strings.ToUpper(filepath.Join(cwd, "nested.md")), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "Managed", "path": filepath.Join(home, "outside.md"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "Project", "path": strings.TrimPrefix(filepath.Join(cwd, "nested.md"), "/private"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
		{{"type": "Project", "path": filepath.Join(cwd+"sibling", "outside.md"), "parent": filepath.Join(cwd, "CLAUDE.md")}},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config-external.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_EXTERNAL_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "cwd": cwd, "rows": rows, "project": map[string]any{"futureProject": map[string]any{"keep": true}, "hasClaudeMdExternalIncludesApproved": false}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source callback %v %.6000s", err, output)
	}
	var source struct {
		Rows []struct {
			Includes []map[string]string
			Admitted bool
		}
		Declined, Accepted, RolledBack protocol.JSONObject
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, files := range rows {
		expected := []map[string]string{}
		for _, file := range files {
			if file["type"] != "User" && file["parent"] != "" && !builtintools.PathInWorkingPath(file["path"], cwd) {
				expected = append(expected, map[string]string{"path": file["path"], "parent": file["parent"]})
			}
		}
		if string(raw(expected)) != string(raw(source.Rows[i].Includes)) || source.Rows[i].Admitted != (len(expected) > 0) {
			t.Fatal(i, string(output), string(raw(expected)))
		}
	}
	if string(source.Declined.Get("hasClaudeMdExternalIncludesApproved")) != "false" || string(source.Accepted.Get("hasClaudeMdExternalIncludesApproved")) != "true" || string(source.Declined.Get("hasClaudeMdExternalIncludesWarningShown")) != "true" || string(obj(source.Accepted.Get("futureProject")).Get("keep")) != "true" || string(source.RolledBack.Get("hasClaudeMdExternalIncludesApproved")) != "false" || len(source.RolledBack.Get("hasClaudeMdExternalIncludesWarningShown")) != 0 {
		t.Fatal(string(output))
	}
}
