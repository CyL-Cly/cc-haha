package retrypolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestSDK128ErrorMetadataMatchesActualConstructor(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []struct {
		HTTP    bool        `json:"http"`
		Status  int         `json:"status"`
		Body    string      `json:"body"`
		Headers [][2]string `json:"headers"`
	}{
		{true, 429, `{"error":{"type":"rate_limit_error","message":"busy"}}`, [][2]string{{"request-id", "request-a"}, {"anthropic-workspace-id", "workspace-a"}}},
		{true, 400, `{"error":{"type":false}}`, [][2]string{}},
		{true, 500, `{"type":"outside","error":{"type":null}}`, [][2]string{}},
		{false, 0, `{"error":{"type":"api_error","message":"busy"}}`, [][2]string{{"anthropic-workspace-id", ""}}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/sdk-error-metadata.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_ERROR_METADATA_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "cases": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual SDK: %v %s", err, stderr.String())
	}
	var source struct {
		Version string
		Results []map[string]any
	}
	if err = json.Unmarshal(raw, &source); err != nil || source.Version != "0.128.0" || len(source.Results) != len(rows) {
		t.Fatalf("SDK output: %v %s", err, raw)
	}
	for i, row := range rows {
		pairs := []provider.HeaderPair{}
		for _, pair := range row.Headers {
			pairs = append(pairs, provider.HeaderPair{Name: pair[0], Value: pair[1]})
		}
		headers, err := provider.NewConstructorHeaders(pairs)
		if err != nil {
			t.Fatal(err)
		}
		var status *int
		if row.HTTP {
			status = &row.Status
		}
		var sdk *provider.SDKStreamError
		if !errors.As(provider.NewSDKResponseError(status, row.Body, headers, row.HTTP), &sdk) {
			t.Fatal("SDK response error missing")
		}
		if err = headers.Set("anthropic-workspace-id", "after-construction"); err != nil {
			t.Fatal(err)
		}
		got, err := serializedSDKError(sdk)
		if err != nil {
			t.Fatal(err)
		}
		var observed map[string]any
		if err = json.Unmarshal(got, &observed); err != nil || !reflect.DeepEqual(observed, source.Results[i]) {
			t.Fatalf("case %d constructor metadata changed: %s source %#v (%v)", i, got, source.Results[i], err)
		}
	}
}
