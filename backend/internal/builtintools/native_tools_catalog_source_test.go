package builtintools

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

func TestActualSourceAllBaseOrderPreservedForEveryMigratedNativeEntry(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-pool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_POOL_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-tool-pool-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	rows := []map[string]any{}
	for _, images := range []bool{false, true} {
		env := map[string]string{"ENABLE_TOOL_SEARCH": "true", "CC_HAHA_AGENT_TEAMS_ENABLED": "true", "CLAUDE_CODE_ENABLE_TASKS": "1"}
		if images {
			env["CC_HAHA_IMAGE_PROVIDER_KIND"], env["CC_HAHA_IMAGE_PROVIDER_ID"], env["CC_HAHA_IMAGE_MODEL"] = "openai_oauth", "private", "gpt-image-2"
		}
		rows = append(rows, map[string]any{"env": env, "nonInteractive": true, "model": "fixture-model"})
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": rows})
	command.Stdin = bytes.NewReader(payload)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source base order", err, string(data))
	}
	var result struct {
		Profiles     []struct{ AllBase []string }
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &result); err != nil || len(result.Profiles) != 2 || len(result.Capabilities.Blocked) != 0 {
		t.Fatal("Source catalog proof", err, string(data))
	}
	for index, enabled := range []bool{false, true} {
		actual := nativeBaseToolNames(enabled)
		set := map[string]bool{}
		for _, name := range actual {
			set[name] = true
		}
		want := []string{}
		for _, name := range result.Profiles[index].AllBase {
			if set[name] {
				want = append(want, name)
			}
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatal("original complete migrated registry order", actual, want)
		}
	}
}
