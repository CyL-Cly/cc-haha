package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestColdCompiledSlashMetadataAgainstActualSourceDeclarations(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, compact := range []string{"", "true", "false", "1", "on"} {
		home := t.TempDir()
		tmp := filepath.Join(home, "tmp")
		_ = os.MkdirAll(tmp, 0700)
		fixture, _ := filepath.Abs("../server/testdata/slash_commands.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "CC_HAHA_FIXTURE_TEMP_ROOT": filepath.Dir(home), "CC_HAHA_SLASH_COMMANDS_FIXTURE": "1", "NODE_ENV": "production", "BUN_OPTIONS": "--no-env-file", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "ANTHROPIC_API_KEY": "fake-offline-only", "PATH": "/usr/bin:/bin", "TZ": "UTC", "DISABLE_COMPACT": compact}
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		input, _ := json.Marshal(map[string]any{"home": home})
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("actual Source compiled metadata %v %.3000s", e, out)
		}
		var source struct{ Commands []map[string]any }
		if e = json.Unmarshal(out, &source); e != nil {
			t.Fatalf("Source shape %v %.3000s", e, out)
		}
		bundled, e := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: env})
		if e != nil {
			t.Fatal(e)
		}
		goJSON, _ := json.Marshal(CompiledHeadlessCommands(env, bundled))
		var actual []map[string]any
		_ = json.Unmarshal(goJSON, &actual)
		if !reflect.DeepEqual(actual, source.Commands) {
			t.Errorf("compact%q Go%s Source%s", compact, goJSON, out)
		}
	}
}
