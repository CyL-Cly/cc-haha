package apipresentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func TestNativeHTTPFailureMatchesActualMainPresentation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type row struct {
		Status              *int
		Kind, Body, Message string
		NonInteractive      bool
	}
	rows := []row{}
	sourceRows := []map[string]any{}
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 500, 503, 529} {
		for _, nonInteractive := range []bool{false, true} {
			for _, body := range []string{`{"type":"error","error":{"type":"api_error","message":"physical upstream failed\ntry again 😀"}}`, `{"message":"<html><body>private upstream</body></html>"}`} {
				statusCopy := status
				rows = append(rows, row{Status: &statusCopy, Kind: "api", Body: body, NonInteractive: nonInteractive})
				sourceRows = append(sourceRows, map[string]any{"kind": "api", "status": status, "payload": json.RawMessage(body), "message": "", "model": "fixture-model", "nonInteractive": nonInteractive})
			}
		}
	}
	for _, extra := range []row{
		{Kind: "api", Body: `{"type":"error","error":{"type":"api_error","message":"private SSE failure 😀\ntry again"}}`},
		{Kind: "api", Body: `"private SSE text"`},
		{Kind: "api", Body: `{"message":"<html><title>Bad Gateway</title></html>"}`},
		{Kind: "connection", Message: "gzip: invalid header"},
		{Kind: "connection", Message: "连接异常 😀"},
		{Kind: "connection", Message: "自定义 Timeout 信息"},
		{Kind: "connection", Message: "<html><title> Gateway problem </title></html>"},
		{Kind: "connection", Message: "<html>without a title</html>"},
		{Kind: "ordinary", Message: "Malformed provider JSON: invalid character 😀"},
	} {
		rows = append(rows, extra)
		input := map[string]any{"kind": extra.Kind, "message": extra.Message, "model": "fixture-model", "nonInteractive": extra.NonInteractive}
		if extra.Kind == "api" {
			input["payload"] = json.RawMessage(extra.Body)
		}
		sourceRows = append(sourceRows, input)
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-media-error.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_MEDIA_ERROR_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-fake-media-key", "ANTHROPIC_BASE_URL=https://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": sourceRows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual MAIN %v %s", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Message, Value string
			Allocations    []string
		}
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatalf("actual errors %v %s", err, output)
	}
	domain, _ := DomainFromOriginalEnv(originalDomainEnv())
	for i, row := range rows {
		headers, _ := provider.NewConstructorHeaders(nil)
		var sdk *provider.SDKStreamError
		if row.Kind == "api" {
			if !errors.As(provider.NewSDKResponseError(row.Status, row.Body, headers, row.Status != nil), &sdk) {
				t.Fatal("SDK error missing")
			}
		} else {
			class := "SyntaxError"
			if row.Kind == "connection" {
				class = "APIConnectionError"
			}
			sdk = &provider.SDKStreamError{Class: class, Message: row.Message}
		}
		calls := 0
		allocations := []string{}
		factory, _ := messages.NewFactory(func() string {
			calls++
			allocations = append(allocations, fmt.Sprintf("uuid:%d", calls))
			return fmt.Sprintf("media-id-%d", calls)
		}, func() time.Time { allocations = append(allocations, "date"); return time.UnixMilli(1700000000000) })
		message, err := NativeCreationFailure(&retrypolicy.CannotRetryError{Original: sdk}, domain, factory, MediaOptions{Model: "fixture-model", NonInteractive: row.NonInteractive})
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		raw, err := message.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		_ = json.Unmarshal(raw, &got)
		_ = json.Unmarshal([]byte(source.Rows[i].Value), &want)
		if sdk.Message != source.Rows[i].Message || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(allocations, source.Rows[i].Allocations) {
			t.Fatalf("case %d/%v projection changed: %s Source %s allocations%v/%v", i, row.Status, raw, source.Rows[i].Value, allocations, source.Rows[i].Allocations)
		}
	}
}
