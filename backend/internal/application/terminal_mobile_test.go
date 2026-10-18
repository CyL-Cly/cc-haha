package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalMobileActualSourceMountedTabsQRCloseAndSessionConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mobile.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MOBILE_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": [][]string{{"\t", "\x1b[D", "\x1b[C", "q"}, {"\r", "\x03"}, {"\x1b"}, {"n"}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual Source mobile %v %.5000s", err, output)
	}
	var source struct {
		Declaration struct {
			Name, Type string
			Aliases    []string
		}
		Rows []struct {
			Frames []struct {
				Key, Output string
				Events      []json.RawMessage
			}
			Events []json.RawMessage
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || source.Declaration.Name != "mobile" || source.Declaration.Type != "local-jsx" || len(source.Declaration.Aliases) != 2 || len(source.Rows) != 4 {
		t.Fatal(err, string(output))
	}
	for _, row := range source.Rows {
		if len(row.Events) != 1 || !strings.Contains(row.Frames[0].Output, "https://apps.apple.com/app/claude-by-anthropic/id6473753684") || !strings.ContainsAny(row.Frames[0].Output, "█▀▄") {
			t.Fatal("Actual mounted Source QR/close", string(output))
		}
	}
	if !strings.Contains(source.Rows[0].Frames[1].Output, "https://play.google.com/store/apps/details?id=com.anthropic.claude") || !strings.Contains(source.Rows[0].Frames[2].Output, "https://apps.apple.com/app/claude-by-anthropic/id6473753684") || len(source.Rows[1].Frames[1].Events) != 0 {
		t.Fatal("Actual Source tabs and Enter must not close", string(output))
	}
	options := nativeSessionOptions(t, "http://127.0.0.1:1")
	options.Interactive = true
	s, err := NewSession(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, name := range []string{"mobile", "ios", "android"} {
		var panel *tui.LocalPanel
		emit := func(e tui.Event) error {
			if e.Kind == tui.LocalPanelOpened || e.Kind == tui.LocalPanelRefreshed {
				copy := *e.LocalPanel
				panel = &copy
			}
			return nil
		}
		handled, err := s.InvokeTerminalCommand(context.Background(), name, "android", emit)
		if err != nil || !handled || panel == nil {
			t.Fatalf("Source ordinary /%s is missing actual Session consumer: handled=%v err=%v", name, handled, err)
		}
		if !bytes.Contains(panel.Presentation, []byte("apps.apple.com")) {
			t.Fatal("Aliases ignore arguments and start iOS", panel)
		}
		for i := 0; i < 3; i++ {
			if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "mobile-switch"}, emit); err != nil {
				t.Fatal(err)
			}
			expected := "play.google.com"
			if i%2 == 1 {
				expected = "apps.apple.com"
			}
			if !bytes.Contains(panel.Presentation, []byte(expected)) {
				t.Fatal("Platform switch", panel)
			}
		}
		if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "close"}, emit); err != nil {
			t.Fatal(err)
		}
	}
}
