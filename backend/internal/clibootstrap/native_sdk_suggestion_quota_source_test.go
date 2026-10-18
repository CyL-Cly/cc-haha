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

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestActualSourcePromptSuggestionQuotaStatusProjection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	fixture := os.Getenv("CC_HAHA_PROMPT_SUGGESTION_SOURCE")
	if fixture == "" {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-prompt-suggestions.fixture.ts")
	}
	rows := []map[string]string{
		{},
		{"anthropic-ratelimit-unified-status": "allowed_warning"},
		{"anthropic-ratelimit-unified-status": "allowed", "anthropic-ratelimit-unified-5h-surpassed-threshold": ""},
		{"anthropic-ratelimit-unified-overage-surpassed-threshold": "0.9"},
		{"anthropic-ratelimit-unified-5h-utilization": "0.9", "anthropic-ratelimit-unified-5h-reset": "2000000000000"},
		{"anthropic-ratelimit-unified-7d-utilization": "0.25", "anthropic-ratelimit-unified-7d-reset": "2000000000000"},
		{"anthropic-ratelimit-unified-5h-utilization": "not-a-number", "anthropic-ratelimit-unified-5h-reset": "2000000000000"},
		{"anthropic-ratelimit-unified-status": "rejected"},
		{"anthropic-ratelimit-unified-status": "unknown-runtime-value"},
	}
	profiles := []map[string]any{}
	for _, headers := range rows {
		profiles = append(profiles, map[string]any{"name": "quota", "headers": headers, "request": map[string]any{"promptSuggestions": false}})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_PROMPT_SUGGESTION_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-suggestion", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_MAX_RETRIES=0", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"suggestions": profiles, "cachedSubscriber": true}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source quota response consumer", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			QuotaStatus string
			Calls       int
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(data))
	}
	for index, headers := range rows {
		pairs := []provider.HeaderPair{}
		for key, value := range headers {
			pairs = append(pairs, provider.HeaderPair{Name: key, Value: value})
		}
		nativeHeaders, err := provider.NewConstructorHeaders(pairs)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := nativeSuggestionQuotaStatus(nativeHeaders, float64(time.Now().UnixMilli()))
		if err != nil || actual != source.Rows[index].QuotaStatus || source.Rows[index].Calls != 0 {
			t.Error("original Source status (including allowed_warning normalization)", index, actual, source.Rows[index], err)
		}
	}
}
