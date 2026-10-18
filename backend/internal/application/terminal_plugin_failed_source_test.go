package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalFailedPluginActualSourceUninstallFallbackAllEditableScopesAndDataPreservation(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source Bun and validated missing-package reference required")
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	settingsText := `{"futureSource":{"keep":true},"enabledPlugins":{"bad@ghost":true,"other@ghost":false}}`
	paths := []string{filepath.Join(config, "settings.json"), filepath.Join(o.WorkDir, ".claude", "settings.json"), filepath.Join(o.WorkDir, ".claude", "settings.local.json")}
	for _, path := range paths {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, []byte(settingsText), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dataPath := filepath.Join(config, "plugins", "data", "bad-ghost", "keep.txt")
	_ = os.MkdirAll(filepath.Dir(dataPath), 0700)
	_ = os.WriteFile(dataPath, []byte("PERSISTENT_PLUGIN_DATA_KEEP"), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	_, err = s.InvokeTerminalCommand(context.Background(), "plugin", "uninstall bad@ghost", emit)
	if err != nil || panel == nil || !strings.Contains(panel.Title, "bad @ ghost") || len(panel.Rows) != 1 || panel.Rows[0].Label != "Remove" {
		t.Fatal("actual failed-plugin detail missing", err, panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Row: "remove", Action: "plugin-remove-failed"}, emit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dataPath)
	if err != nil || string(data) != "PERSISTENT_PLUGIN_DATA_KEEP" {
		t.Fatal("failed recovery deleted persistent data", err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	sourcePaths := []string{filepath.Join(home, ".claude", "settings.json"), filepath.Join(work, ".claude", "settings.json"), filepath.Join(work, ".claude", "settings.local.json")}
	files := map[string]string{}
	for _, path := range sourcePaths {
		files[path] = base64.StdEncoding.EncodeToString([]byte(settingsText))
	}
	files[filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")] = base64.StdEncoding.EncodeToString([]byte(`{}`))
	files[filepath.Join(home, ".claude", "plugins", "installed_plugins.json")] = base64.StdEncoding.EncodeToString([]byte(`{"version":2,"plugins":{}}`))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-recovery.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_RECOVERY_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "files": files, "observe": sourcePaths, "plugin": map[string]any{"id": "bad@ghost", "name": "bad", "marketplace": "ghost", "scope": "local"}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original failed recovery %v %.5000s", err, output)
	}
	var original struct {
		View  string
		Error *string
		Files map[string]json.RawMessage
	}
	if err = json.Unmarshal(output, &original); err != nil || original.View != "plugin-list" || original.Error != nil {
		t.Fatal("original recovery not successful", err, string(output))
	}
	for index, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		expected := original.Files[strings.TrimPrefix(sourcePaths[index], home)]
		var a, b any
		_ = json.Unmarshal(data, &a)
		_ = json.Unmarshal(expected, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("actual Source scope recovery differs %s %s Source%s", path, data, expected)
		}
	}
}
