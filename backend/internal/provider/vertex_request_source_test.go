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

func TestReviewedVertexRequestVersionModelPathStreamAndGoogleHeaders(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, stream := range []any{nil, false, true, "enabled", 0} {
		body := map[string]any{"model": "claude-sonnet@20250514", "messages": []any{}}
		if stream != nil {
			body["stream"] = stream
		}
		rows = append(rows, map[string]any{"body": body, "project": "private-project", "authHeaders": map[string]string{"authorization": "Bearer ADC_PRIVATE", "x-goog-user-project": "QUOTA_PRIVATE"}, "headers": map[string]string{"anthropic-beta": "a,b"}})
	}
	rows = append(rows, map[string]any{"body": map[string]any{"model": 17, "stream": true, "anthropic_version": "custom"}, "project": "private-project"}, map[string]any{"body": map[string]any{"messages": []any{}}, "project": "private-project", "path": "/v1/messages"}, map[string]any{"body": map[string]any{"model": "claude-private"}, "clientProject": "FROM_KEY", "authHeaders": map[string]string{"authorization": "Bearer ADC_PRIVATE"}}, map[string]any{"body": map[string]any{"model": "claude-private"}, "authHeaders": map[string]string{"x-goog-user-project": "QUOTA_IS_NOT_PROJECT"}}, map[string]any{"body": map[string]any{"model": "claude-private"}, "project": "private-project", "authHeaders": map[string]string{"authorization": "Bearer ADC_PRIVATE"}, "headers": map[string]string{"authorization": "Bearer REQUEST_PRIVATE"}}, map[string]any{"body": map[string]any{"model": "kept", "messages": []any{}}, "project": "private-project", "path": "/v1/messages/count_tokens?beta=true", "region": "global", "base": "https://aiplatform.googleapis.com/v1"})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-vertex-request.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_VERTEX_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []map[string]json.RawMessage }
	if json.Unmarshal(out, &source) != nil {
		t.Fatal(string(out))
	}
	for i, row := range rows {
		base, _ := row["base"].(string)
		if base == "" {
			base = "https://us-east5-aiplatform.googleapis.com/v1"
		}
		region, _ := row["region"].(string)
		if region == "" {
			region = "us-east5"
		}
		path, _ := row["path"].(string)
		if path == "" {
			path = "/v1/messages?beta=true"
		}
		project, _ := row["project"].(string)
		clientProject, _ := row["clientProject"].(string)
		headers := http.Header{}
		if fields, ok := row["headers"].(map[string]string); ok {
			for key, value := range fields {
				headers.Set(key, value)
			}
		}
		body, _ := json.Marshal(row["body"])
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		request.Header = http.Header{}
		config := VertexConfig{Region: region, ProjectID: project, Headers: func(context.Context) (http.Header, string, error) {
			auth := http.Header{}
			if fields, ok := row["authHeaders"].(map[string]string); ok {
				for key, value := range fields {
					auth.Set(key, value)
				}
			}
			return auth, clientProject, nil
		}}
		err = config.adapt(ctx, request, &MessagesRequest{Path: path, Body: body, Headers: headers})
		if err != nil {
			var text string
			_ = json.Unmarshal(source.Rows[i]["error"], &text)
			if err.Error() != text {
				t.Fatalf("case%d Go%v Source%s", i, err, source.Rows[i]["error"])
			}
			continue
		}
		result := map[string]any{"url": request.URL.String(), "body": string(readVertexBody(t, request)), "headers": map[string]string{}}
		for key, value := range request.Header {
			result["headers"].(map[string]string)[http.CanonicalHeaderKey(key)] = value[0]
		}
		var expected map[string]json.RawMessage
		_ = json.Unmarshal(source.Rows[i]["value"], &expected)
		for _, field := range []string{"url", "body"} {
			var want string
			_ = json.Unmarshal(expected[field], &want)
			if result[field] != want {
				t.Fatalf("case%d %s Go%v Source%s", i, field, result[field], want)
			}
		}
		var wantHeaders map[string]string
		_ = json.Unmarshal(expected["headers"], &wantHeaders)
		normalized := map[string]string{}
		for key, value := range wantHeaders {
			normalized[http.CanonicalHeaderKey(key)] = value
		}
		if !reflect.DeepEqual(result["headers"], normalized) {
			t.Fatal("headers", result["headers"], normalized)
		}
	}
}

func readVertexBody(t *testing.T, request *http.Request) []byte {
	t.Helper()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
