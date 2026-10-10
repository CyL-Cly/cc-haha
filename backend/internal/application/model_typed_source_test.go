package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTypedModelHelpUsesActualLocalJSXCommandWithoutASK(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual original Source requires pinned Bun")
	}
	home := t.TempDir()
	for _, leaf := range []string{"tmp", "config", "cache"} {
		if err := os.Mkdir(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := filepath.Abs("../../testdata/native-model-command.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := filepath.Abs("../../testdata/loopback-only.sb")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": []any{map[string]any{"name": "actual-help", "args": "  --help  ", "settings": map[string]any{"future": true}, "state": map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil, "fastMode": false}}}})
	cmd := exec.Command("/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"BUN_OPTIONS=--no-env-file", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=fixture-model-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:43210", "CC_HAHA_MODEL_COMMAND_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("actual Source failed %v: %s", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Notice string `json:"notice"`
		} `json:"rows"`
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
		t.Fatal(err, string(out))
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:43210")
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var notices []string
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model --help"}, func(event tui.Event) error {
		if event.Kind == tui.Notice {
			notices = append(notices, event.Text)
		}
		return nil
	}); err != nil {
		t.Fatalf("Go typed command differs from original %q: %v", source.Rows[0].Notice, err)
	}
	if len(notices) != 1 || notices[0] != source.Rows[0].Notice {
		t.Fatal("actual JSX notice", notices, source.Rows[0].Notice)
	}
}
