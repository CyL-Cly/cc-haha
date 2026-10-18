//go:build !windows

package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceAndNativeModelResponseRateHeadersReachMountedStatusLine(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	for _, profile := range []string{"subscriber-success", "subscriber-429", "api-key-success", "subscriber-numeric-edge", "subscriber-radix-rounding"} {
		t.Run(profile, func(t *testing.T) {
			subscriber := profile != "api-key-success"
			header := map[string]string{"anthropic-ratelimit-unified-5h-utilization": "0.125", "anthropic-ratelimit-unified-5h-reset": "1700000300", "anthropic-ratelimit-unified-7d-utilization": "0.875", "anthropic-ratelimit-unified-7d-reset": "1700000900", "content-type": "text/event-stream"}
			if profile == "subscriber-numeric-edge" {
				header["anthropic-ratelimit-unified-5h-utilization"], header["anthropic-ratelimit-unified-5h-reset"] = "-0", "-0"
				header["anthropic-ratelimit-unified-7d-utilization"], header["anthropic-ratelimit-unified-7d-reset"] = "0x100000000000000000000", "0b1"+strings.Repeat("0", 70)
			}
			if profile == "subscriber-radix-rounding" {
				// One integer above a binary64 midpoint at 2^100. A preliminary
				// 64-bit big.Float conversion loses this deciding low bit.
				header["anthropic-ratelimit-unified-7d-utilization"], header["anthropic-ratelimit-unified-7d-reset"] = "0x10000000000000800000000001", "0x10000000000000800000000001"
			}
			status := 200
			var sourceWire bytes.Buffer
			response(&statusLineCaptureWriter{header: http.Header{}, buffer: &sourceWire}, "statusline-rate", "", nil, "ACTUAL RATE RESPONSE")
			if profile == "subscriber-429" {
				status = 429
				header["content-type"] = "application/json"
				sourceWire.Reset()
				sourceWire.WriteString(`{"type":"error","error":{"type":"rate_limit_error","message":"actual private quota rejection"}}`)
			}
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(home, "statusline-script")
			if err = os.WriteFile(script, []byte("#!/bin/sh\nIFS= read -r input\nprintf '%s\\n' \"$input\" >> \"$HOME/statusline-inputs.jsonl\"\nprintf ACTUAL_STATUS_RATE\n"), 0700); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"home": home, "trusted": true, "cachedSubscriber": subscriber, "settings": map[string]any{"statusLine": map[string]any{"type": "command", "command": "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"}}, "apiResponses": []any{map[string]any{"status": status, "headers": header, "wire": sourceWire.String()}}, "steps": []any{map[string]any{"op": "wait", "count": 1}, map[string]any{"op": "query", "text": "STATUSLINE_ACTUAL_QUOTA"}, map[string]any{"op": "wait", "count": 2}}}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-statusline.fixture.ts")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUSLINE_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "ANTHROPIC_API_KEY=private-source-statusline", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "CLAUDE_CODE_ENTRYPOINT=cli"}
			command.Stdin = bytes.NewReader(raw(input))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("actual whole Source query→responseheaders→StatusLine", err, string(output))
			}
			var original struct {
				Records          []protocol.JSONObject
				PhysicalRequests []protocol.JSONObject
				Blocked          []string
			}
			if err = json.Unmarshal(output, &original); err != nil || len(original.Blocked) != 0 || len(original.Records) != 2 || len(original.PhysicalRequests) != 1 {
				t.Fatal(err, string(output))
			}
			if subscriber && original.PhysicalRequests[0].String("authorization") != "Bearer private-statusline-subscriber" {
				t.Fatal("Source fake cached subscriber actual client did not supply owned token", string(output))
			}
			var nativeRequest []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nativeRequest, _ = io.ReadAll(r.Body)
				if subscriber && (r.Header.Get("authorization") != "Bearer private-statusline-subscriber" || r.Header.Get("x-api-key") != "") {
					t.Error("native owned cached subscriber client", r.Header)
				}
				for key, value := range header {
					w.Header().Set(key, value)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, sourceWire.String())
			}))
			defer server.Close()
			s, nativeHome := newTerminalStatusLinePhysicalSession(t, server.URL, "printf ACTUAL_NATIVE_RATE\n", subscriber)
			_, remove, err := s.SubscribeTerminalStatusLine(context.Background(), func(json.RawMessage) {})
			if err != nil {
				t.Fatal(err)
			}
			defer remove()
			waitTerminalStatusLinePhysicalRecord(t, nativeHome, 1)
			if err = s.Submit(context.Background(), tui.Input{Text: "STATUSLINE_ACTUAL_QUOTA"}, nil); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(nativeRequest), "STATUSLINE_ACTUAL_QUOTA") {
				t.Fatal("actual model HTTP was not reached", string(nativeRequest))
			}
			if err = s.UpdateTerminalStatusLine(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			rows := waitTerminalStatusLinePhysicalRecord(t, nativeHome, 2)
			var native protocol.JSONObject
			if err = native.UnmarshalJSON([]byte(strings.SplitN(rows[1], " ", 2)[1])); err != nil {
				t.Fatal(err)
			}
			want, got := original.Records[1].Get("rate_limits"), native.Get("rate_limits")
			if string(want) != string(got) {
				t.Fatal("actual Source/native response utilization callback mismatch", string(want), string(got))
			}
			if subscriber && profile != "subscriber-numeric-edge" && profile != "subscriber-radix-rounding" && (!bytes.Contains(got, []byte(`12.5`)) || !bytes.Contains(got, []byte(`87.5`))) || !subscriber && got != nil {
				t.Fatal("rate header subscriber gate/value", string(got))
			}
			t.Logf("actual whole Source/native HTTP→StatusLine rate_limits: %s", got)
		})
	}
}

type statusLineCaptureWriter struct {
	header http.Header
	buffer *bytes.Buffer
}

func (w *statusLineCaptureWriter) Header() http.Header { return w.header }
func (w *statusLineCaptureWriter) WriteHeader(int)     {}
func (w *statusLineCaptureWriter) Write(p []byte) (int, error) {
	return fmt.Fprint(w.buffer, string(p))
}
