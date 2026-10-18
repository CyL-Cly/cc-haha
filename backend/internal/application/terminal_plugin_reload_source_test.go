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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginReloadActualFullSourceMetricsCanonicalReplayAndPersistentOutput(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source and verified official MCPB reference required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-reload.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_RELOAD_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "USER_TYPE=external"}
	cmd.Stdin = bytes.NewReader(raw(map[string]string{"home": home, "entrypoint": "sdk-cli"}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("whole Source reload %v %.5000s", err, output)
	}
	var source struct {
		Caller struct {
			Messages    []protocol.JSONObject
			ResultText  string
			ShouldQuery bool
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Caller.Messages) != 3 || source.Caller.ShouldQuery || len(source.Caps.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:9")
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var received tui.Event
	if err = s.Submit(context.Background(), tui.Input{TurnID: "reload-queued", Text: "/reload-plugins kept args", Mode: "prompt"}, func(event tui.Event) error {
		if event.Kind == tui.LocalCommandOutput {
			received = event
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if received.Text != source.Caller.ResultText || !received.Background {
		t.Fatalf("actual output %+v Source %q", received, source.Caller.ResultText)
	}
	semantic := func(rows []protocol.JSONObject) []map[string]any {
		out := []map[string]any{}
		for _, row := range rows {
			api, _ := row.Object("message")
			out = append(out, map[string]any{"type": row.String("type"), "subtype": row.String("subtype"), "isMeta": string(row.Get("isMeta")), "role": api.String("role"), "messageContent": api.String("content"), "content": row.String("content"), "level": row.String("level")})
		}
		return out
	}
	if actual := s.process.Controller.History(); !reflect.DeepEqual(semantic(actual), semantic(source.Caller.Messages)) {
		t.Fatalf("same-Harness local replay %+v Source %+v", semantic(actual), semantic(source.Caller.Messages))
	}
}
