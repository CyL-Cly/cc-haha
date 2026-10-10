package commandnames

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
)

func TestOriginalCompiledBuiltinNamesAndAliasesAcrossProviderFlags(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, flags := range []map[string]string{{}, {"CLAUDE_CODE_USE_BEDROCK": "1"}, {"CLAUDE_CODE_USE_VERTEX": " true "}, {"CLAUDE_CODE_USE_FOUNDRY": "yes"}, {"CLAUDE_CODE_USE_AZURE_OPENAI": "ON"}, {"CLAUDE_CODE_USE_BEDROCK": "false"}, {"ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "DISABLE_COMPACT": "1"}} {
		home := t.TempDir()
		fixture, _ := filepath.Abs("../goalstate/testdata/goal-command.fixture.ts")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = home
		env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": home, "CC_HAHA_FIXTURE_TEMP_ROOT": filepath.Dir(home), "CC_HAHA_GOAL_COMMAND_FIXTURE": "1", "PATH": "/usr/bin:/bin", "BUN_OPTIONS": "--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "NODE_ENV": "production"}
		for key, value := range flags {
			env[key] = value
		}
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		input, _ := json.Marshal(map[string]any{"home": home, "builtinNames": true})
		cmd.Stdin = bytes.NewReader(input)
		data, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatal(err, string(data))
		}
		var source struct{ Names []string }
		if err = json.Unmarshal(data, &source); err != nil {
			t.Fatal(err, string(data))
		}
		actual, err := Names(env)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, source.Names) {
			t.Errorf("flags%v Go%v Source%v", flags, actual, source.Names)
		}
	}
}
