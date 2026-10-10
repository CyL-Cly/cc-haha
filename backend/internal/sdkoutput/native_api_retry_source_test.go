package sdkoutput

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func TestNativeAPIRetryNoticesRemainInternalAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	n, allocations := fixture(t)
	rows := []protocol.JSONObject{}
	for _, status := range []int{401, 429, 500} {
		failure := &provider.SDKStreamError{Class: "APIError", Message: "private fixture retry", Status: &status}
		record, err := (retrypolicy.RetryNotice{Type: "system", Subtype: "api_error", Level: "error", Error: failure, RetryInMillis: 500, RetryAttempt: 1, MaxRetries: 2, Timestamp: "2026-10-08T00:00:00.000Z", UUID: "private-retry"}).Envelope()
		if err != nil {
			t.Fatal(err)
		}
		if !messages.IsNativeAPIRetryRecord(record) {
			t.Fatal("actual notice rejected")
		}
		rows = append(rows, record)
		data, _ := record.MarshalJSON()
		decoded, err := Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		before := *allocations
		if err = n.NormalizeMessage(decoded, func(*View) error { t.Error("original internal retry notice leaked into SDK output"); return nil }); err != nil || *allocations != before {
			t.Fatal("retry output allocation changed", err)
		}
	}
	home := t.TempDir()
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auth-helper-schema.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTH_HELPER_SCHEMA_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external"}
	input, _ := json.Marshal(map[string]any{"schemas": []any{}, "notices": rows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var result struct{ Notices [][]json.RawMessage }
	if err = json.Unmarshal(output, &result); err != nil || len(result.Notices) != len(rows) {
		t.Fatal(err, string(output))
	}
	for _, row := range result.Notices {
		if len(row) != 0 {
			t.Fatal("Source converted api retry to public SDK", string(output))
		}
	}
}
