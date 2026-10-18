package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeAPIPreconnectActualOriginalTargetTransportGateAndOnce(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	cases := []map[string]string{{}, {"ANTHROPIC_BASE_URL": "http://127.0.0.1:1/private-base"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		for _, value := range []string{"true", "0", "false", " yes "} {
			cases = append(cases, map[string]string{key: value})
		}
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ANTHROPIC_UNIX_SOCKET", "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY"} {
		cases = append(cases, map[string]string{key: "0"}, map[string]string{key: ""})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-api-preconnect.fixture.ts")
	for _, env := range cases {
		home := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		command.Dir = home
		command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_API_PRECONNECT=1", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
		input, _ := json.Marshal(map[string]any{"home": home, "env": env})
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		cancel()
		if err != nil {
			t.Fatal("actual Source preconnect", env, err, string(output), stderr.String())
		}
		var source struct {
			Calls []struct {
				URL, Method string
				Signal      bool
			}
			Timeouts     []int
			ReturnedVoid bool
			Blocked      []string
		}
		if err := json.Unmarshal(output, &source); err != nil || !source.ReturnedVoid || len(source.Blocked) != 0 || len(source.Calls) > 1 {
			t.Fatal("original fireforget/once", env, err, string(output))
		}
		target, err := nativeAPIPreconnectTarget(env)
		if err != nil {
			t.Fatal(err)
		}
		if target == "" {
			if len(source.Calls) != 0 || len(source.Timeouts) != 0 {
				t.Fatal("native skip differs from actual Source", env, target, string(output))
			}
		} else if len(source.Calls) != 1 || source.Calls[0].URL != target || source.Calls[0].Method != "HEAD" || !source.Calls[0].Signal || len(source.Timeouts) != 1 || source.Timeouts[0] != 10000 {
			t.Fatal("actual original HEAD target/signal", env, target, string(output))
		}
	}
}
