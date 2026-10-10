package desktopoauth

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

func TestOpenAIRefreshFetchRoutingAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, env := range []map[string]string{{}, {"HTTP_PROXY": "http://127.0.0.1:11"}, {"HTTPS_PROXY": "http://127.0.0.1:22", "HTTP_PROXY": "http://127.0.0.1:11"}, {"https_proxy": "http://127.0.0.1:33", "HTTPS_PROXY": "http://127.0.0.1:22", "http_proxy": "http://127.0.0.1:44", "HTTP_PROXY": "http://127.0.0.1:11"}, {"https_proxy": "", "http_proxy": "http://127.0.0.1:44", "NO_PROXY": "*"}, {"HTTP_PROXY": "http://127.0.0.1:11", "no_proxy": "auth.openai.com", "ANTHROPIC_UNIX_SOCKET": "/private/unowned.sock"}} {
		for _, disabled := range []bool{false, true} {
			for _, override := range []any{nil, "", "http://127.0.0.1:55"} {
				rows = append(rows, map[string]any{"env": env, "disabled": disabled, "override": override})
			}
			rows = append(rows, map[string]any{"env": env, "disabled": disabled})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-refresh-options.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_REFRESH_OPTIONS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source refresh routing: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		var override *string
		if value, present := row["override"]; present {
			valueString := ""
			if value != nil {
				valueString = value.(string)
			}
			override = &valueString
		}
		result := OpenAIRefreshFetchOptions(row["env"].(map[string]string), override, row["disabled"].(bool))
		var got, want any
		_ = json.Unmarshal(encode(result), &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d Go%s Source%s", i, encode(result), expected[i])
		}
	}
}
