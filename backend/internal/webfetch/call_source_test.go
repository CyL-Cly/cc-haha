package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSourceWebFetchCallHasRealStreamingSecondaryRequest(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	var posts atomic.Int32
	var observed []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["stream"] != true || body["model"] != "deepseek-flash" {
				t.Errorf("wrong Source secondary request %v", body)
			}
			observed = append(observed, body)
			posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"source-web-model\",\"role\":\"assistant\",\"type\":\"message\",\"model\":\"deepseek-flash\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"actual model response\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>actual page</h1><p>content</p>")
	}))
	defer server.Close()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-webfetch-http.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WEBFETCH_HTTP_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "origin": server.URL, "now": 1770000000000, "modelCalls": true, "sessionId": "11111111-1111-4111-8111-111111111111", "deviceId": strings.Repeat("a", 64), "cases": []map[string]any{{"url": "https://one.fixture:$PORT/page", "call": true, "prompt": "Find heading", "settings": map[string]any{"skipWebFetchPreflight": true}}}})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, stderr.String())
	}
	var source struct {
		Rows         []Output
		Physical     []map[string]any
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || posts.Load() != 1 || len(source.Rows) != 1 || len(source.Capabilities.Blocked) > 0 || string(source.Rows[0].Result) != `"actual model response"` {
		t.Fatalf("Source body/model incomplete posts%d blocked%v output%.3000s stderr%.3000s", posts.Load(), source.Capabilities.Blocked, out, stderr.String())
	}
}
