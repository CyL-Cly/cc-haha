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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPureOpenAIRefreshProtocolAgainstActualClientSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	for _, token := range []any{nil, false, 0, 17, "", "fake +/界", json.RawMessage(`"unpaired-\ud800"`), []any{1, nil, "x"}, map[string]any{"x": 1}} {
		rows = append(rows, map[string]any{"operation": "refresh", "refreshToken": token, "status": 200, "body": `{"access_token":"fake-new","refresh_token":"fake-refresh"}`})
	}
	rows = append(rows, map[string]any{"operation": "refresh", "status": 200, "body": `null`})
	for _, body := range []string{`false`, `42`, `"text"`, `[]`, `[1,null]`, `{"expires_in":1e999,"id_token":"\ud800"}`, "\ufeff{\"access_token\":\"bom\"}"} {
		rows = append(rows, map[string]any{"operation": "refresh", "refreshToken": "fake-refresh", "status": 200, "body": body})
	}
	for _, response := range []struct {
		status int
		body   string
	}{{401, `{"access_token":"private-fixture","CODE_VERIFIER":"private-verifier","other":"keep"}`}, {403, `refresh_token=private&id_token=private&other=keep`}, {500, "private upstream failure"}} {
		rows = append(rows, map[string]any{"operation": "refresh", "refreshToken": "fake-refresh", "status": response.status, "body": response.body})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_CLIENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"now": 1770000000000, "rows": []any{}, "transportRows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source refresh protocol: %v %s", err, stderr.String())
	}
	var source struct{ Requests, TransportResults []json.RawMessage }
	if json.Unmarshal(data, &source) != nil || len(source.Requests) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		var rawToken json.RawMessage
		if token, exists := row["refreshToken"]; exists {
			rawToken = encode(token)
		}
		request, err := OpenAIRefreshRequest(rawToken)
		if err != nil {
			t.Fatal(err)
		}
		headers := map[string]any{"accept": nil, "content-type": nil, "user-agent": nil, "authorization": nil}
		for _, h := range request.Headers {
			switch h[0] {
			case "Accept":
				headers["accept"] = h[1]
			case "Content-Type":
				headers["content-type"] = h[1]
			case "User-Agent":
				headers["user-agent"] = h[1]
			}
		}
		gotRequest := map[string]any{"method": request.Method, "path": "/oauth/token", "body": request.Body, "headers": headers}
		if request.URL != OpenAIAuthIssuer+"/oauth/token" {
			t.Fatal("wrong refresh endpoint")
		}
		var got, want any
		_ = json.Unmarshal(encode(gotRequest), &got)
		_ = json.Unmarshal(source.Requests[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d request mismatch", i)
		}
		value, err := DecodeOpenAITokenResponse(row["status"].(int), []byte(row["body"].(string)), nil)
		result := map[string]any{}
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["value"] = json.RawMessage(protocol.StringifyValidatedJavaScriptJSON(value))
		}
		_ = json.Unmarshal(encode(result), &got)
		_ = json.Unmarshal(source.TransportResults[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d decode Go%s Source%s", i, encode(result), source.TransportResults[i])
		}
	}
}
