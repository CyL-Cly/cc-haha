package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestTerminalPluginDataSizeActualSourceLazyWalkSymlinkTargetsAndInaccessiblePaths(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	for _, profile := range []string{"absent", "empty", "nested", "symlinks", "not-directory", "cycle"} {
		t.Run(profile, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-data.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_DATA_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "profile": profile}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source data walk %v %.4000s", err, output)
			}
			var source protocol.JSONObject
			if err = source.UnmarshalJSON(output); err != nil {
				t.Fatal(err, string(output))
			}
			size, err := terminalPluginDataDirectorySize(context.Background(), source.String("directory"))
			if err != nil {
				t.Fatal(err)
			}
			var actual json.RawMessage = []byte("null")
			if size > 0 {
				actual = rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "bytes", Value: rawMain(size)}, protocol.JSONField{Name: "human", Value: quoteMain(toolschema.FormatFileSize(size))}))
			}
			got, _ := protocol.ParseAndStringifyJavaScriptJSON(actual)
			want, _ := protocol.ParseAndStringifyJavaScriptJSON(source.Get("size"))
			if string(got) != string(want) {
				t.Fatalf("Source data profile %s actual%s Source%s", profile, got, want)
			}
		})
	}
}
