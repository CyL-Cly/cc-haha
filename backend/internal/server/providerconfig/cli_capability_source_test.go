package providerconfig

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

func TestCLICapabilityFormatsAgainstActualSource(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cases := []map[string]string{}
	actual := []string{}
	for _, model := range []string{"owned-unknown", "namespace/KIMI-K3[1M]", "glm-5.3", "glm-5.2-flash", "glm-4.7", "kimi-k2.5", "minimax-m3", "deepseek-v4-flash"} {
		for _, format := range []string{"anthropic", "openai_chat", "openai_responses", "openai", "", "future-format"} {
			for _, kind := range []string{"", "zhipu_standard_api"} {
				cases = append(cases, map[string]string{"model": model, "format": format, "kind": kind})
				actual = append(actual, capability(model, kind, format))
			}
		}
	}
	input, _ := json.Marshal(map[string]any{"home": root, "cases": cases})
	fixture, _ := filepath.Abs("cli_capability.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	child.Dir = root
	child.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "TMPDIR=" + root, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(root), "CC_HAHA_CLI_PROVIDER_CAPABILITY_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(input)
	out, e := child.CombinedOutput()
	if e != nil {
		t.Fatal("actual Source capability", e, string(out))
	}
	var original []string
	if e = json.Unmarshal(out, &original); e != nil {
		t.Fatal(e, string(out))
	}
	if !reflect.DeepEqual(original, actual) {
		for i, value := range original {
			if value != actual[i] {
				t.Errorf("%v Source=%q Go=%q", cases[i], value, actual[i])
			}
		}
	}
}
