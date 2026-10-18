package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginDetailsActualSourceScopedInstallHomepageAndGitHubRows(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	entries := []map[string]any{
		{"source": "./plugins/sample"},
		{"source": "./plugins/sample", "homepage": "http://127.0.0.1:1/owned-homepage"},
		{"source": map[string]any{"source": "github", "repo": "owned/private-plugin"}},
		{"source": map[string]any{"source": "github", "repo": "owned/private-plugin"}, "homepage": "http://127.0.0.1:1/owned-homepage"},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-details.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_DETAILS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": entries}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original plugin details %v %.3000s", err, output)
	}
	var wants [][]struct{ Label, Action string }
	if err = json.Unmarshal(output, &wants); err != nil || len(wants) != len(entries) {
		t.Fatal(err, string(output))
	}
	for index, entry := range entries {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			prepareTerminalPluginFixture(t, o)
			catalogPath := filepath.Join(o.WorkDir, "market", ".claude-plugin", "marketplace.json")
			catalogBytes, err := os.ReadFile(catalogPath)
			if err != nil {
				t.Fatal(err)
			}
			catalog := obj(catalogBytes)
			rows := array(catalog.Get("plugins"))
			for key, value := range entry {
				rows[0].Set(key, raw(value))
			}
			catalog.Set("plugins", raw(rows))
			_ = os.WriteFile(catalogPath, raw(catalog), 0600)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var panel *tui.LocalPanel
			_, err = s.InvokeTerminalCommand(context.Background(), "plugin", "install sample@mkt", func(event tui.Event) error {
				if event.LocalPanel != nil {
					props := *event.LocalPanel
					panel = &props
				}
				return nil
			})
			if err != nil || panel == nil {
				t.Fatal(err, panel)
			}
			labels, expected := []string{}, []string{}
			for _, row := range panel.Rows {
				labels = append(labels, row.Label)
			}
			for _, row := range wants[index] {
				expected = append(expected, row.Label)
			}
			if !reflect.DeepEqual(labels, expected) {
				t.Fatal("actual normal detail consumer differs from original menu", labels, expected)
			}
		})
	}
}
