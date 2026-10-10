package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestReviewedOriginalBedrockRequestRewritePreservesRawFieldsAndCaller(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, model := range []string{"us.anthropic.claude:v1", "arn:aws:bedrock:us-east-1:123:application-inference-profile/id", "model whitespace", "模型+!", "percent%2Fvalue"} {
		for _, stream := range []bool{false, true} {
			rows = append(rows, map[string]any{"path": "/v1/messages?beta=true", "body": map[string]any{"model": model, "stream": stream, "messages": []any{}, "anthropic_version": "", "extraUnknown": map[string]any{"nested": true}}, "headers": map[string]string{"anthropic-beta": "one, two,"}})
		}
	}
	rows = append(rows, map[string]any{"path": "/v1/messages", "body": map[string]any{"model": "private", "stream": true, "anthropic_version": "original", "anthropic_beta": []string{}, "unknown": "<keep>"}, "headers": map[string]string{"anthropic-beta": "ignored"}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-bedrock-request.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BEDROCK_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []map[string]any }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		body, _ := json.Marshal(row["body"])
		original := bytes.Clone(body)
		headers := http.Header{}
		for key, value := range row["headers"].(map[string]string) {
			headers.Set(key, value)
		}
		prepared := &MessagesRequest{Path: row["path"].(string), Body: body, Headers: headers}
		request, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:9999/prefix"+prepared.Path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("content-type", "application/json")
		if err = (&BedrockConfig{Region: "us-east-1", SkipAuth: true}).adapt(ctx, request, prepared); err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got any
		if err = json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, source.Rows[index]["body"]) {
			t.Errorf("case%d body Go%s Source%v", index, raw, source.Rows[index]["body"])
		}
		if request.URL.EscapedPath() != "/prefix"+source.Rows[index]["path"].(string) || request.URL.RawQuery != "" {
			t.Errorf("case%d rewrittenURL %s Source%v", index, request.URL, source.Rows[index]["path"])
		}
		if !bytes.Equal(body, original) || source.Rows[index]["originalUnchanged"] != true {
			t.Fatal("retained original body mutated")
		}
	}
}
