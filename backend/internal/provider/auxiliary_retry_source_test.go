package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAuxiliaryOneRetryMatchesActualSDKPhysicalAttempts(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	for _, profile := range []struct {
		name      string
		status    int
		directive string
		fallback  bool
		custom    string
	}{{"server", 500, "", false, ""}, {"timeout", 408, "", false, ""}, {"lock", 409, "", false, ""}, {"rate", 429, "", false, ""}, {"explicit-true", 400, "true", false, ""}, {"explicit-false", 500, "false", false, ""}, {"auth", 401, "", false, ""}, {"fallback", 500, "", true, ""}, {"custom-zero", 500, "", false, "0"}, {"custom-word", 500, "", false, "original-custom"}} {
		t.Run(profile.name, func(t *testing.T) {
			type row struct {
				Path, Retry, Beta string
				Body              json.RawMessage
			}
			var mu sync.Mutex
			rows := []row{}
			ordinal := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				ordinal++
				n := ordinal
				rows = append(rows, row{r.URL.RequestURI(), r.Header.Get("x-stainless-retry-count"), r.Header.Get("anthropic-beta"), body})
				mu.Unlock()
				w.Header().Set("content-type", "application/json")
				if n == 1 {
					w.Header().Set("retry-after-ms", "1")
					if profile.directive != "" {
						w.Header().Set("x-should-retry", profile.directive)
					}
					w.WriteHeader(profile.status)
					io.WriteString(w, `{"type":"error","error":{"type":"actual_loopback_failure","message":"first request fails"}}`)
					return
				}
				if profile.fallback {
					io.WriteString(w, `{"type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":37,"output_tokens":1}}`)
				} else {
					io.WriteString(w, `{"input_tokens":37}`)
				}
			}))
			defer peer.Close()
			var params protocol.JSONObject
			_ = params.UnmarshalJSON([]byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"actual count"}],"tools":[],"betas":["context-1m-2025-08-07"]}`))
			if profile.fallback {
				params.Set("max_tokens", json.RawMessage("1"))
			}
			config := AnthropicConfig{APIKey: "offline-count-key", BaseURL: peer.URL, Timeout: 5 * time.Second}
			headers := map[string]string{}
			if profile.custom != "" {
				copy := profile.custom
				config.AuxiliaryRetryHeader = &copy
				headers["x-stainless-retry-count"] = copy
			}
			client := NewAnthropic(config)
			_, nativeErr := client.WithOneSDKRetry(context.Background(), AuxiliaryRetry{Now: time.Now, Random: func() float64 { return .25 }, Sleep: func(_ context.Context, n float64) error {
				if n != 1 {
					t.Errorf("retry wait %v", n)
				}
				return nil
			}}, func(c *Anthropic) (MessageResponse, error) {
				if profile.fallback {
					return c.CreateBetaMessage(context.Background(), params)
				}
				return c.CountTokens(context.Background(), params)
			})
			mu.Lock()
			native := append([]row{}, rows...)
			rows = nil
			ordinal = 0
			mu.Unlock()
			allocation := t.TempDir()
			home := filepath.Join(allocation, "source-home")
			_ = os.MkdirAll(home, 0700)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-count-tokens.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_COUNT_SDK_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			input, _ := json.Marshal(map[string]any{"home": home, "url": peer.URL, "params": []protocol.JSONObject{params}, "retries": 1, "headers": headers, "fallback": profile.fallback, "catchErrors": true})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var results []map[string]any
			if err = json.Unmarshal(output, &results); err != nil {
				t.Fatal(err, string(output))
			}
			mu.Lock()
			source := append([]row{}, rows...)
			mu.Unlock()
			if len(native) != len(source) {
				t.Fatalf("physical attempts: native %d Source %d", len(native), len(source))
			}
			for i := range source {
				a, b := native[i], source[i]
				var left, right any
				_ = json.Unmarshal(a.Body, &left)
				_ = json.Unmarshal(b.Body, &right)
				if a.Path != b.Path || a.Retry != b.Retry || a.Beta != b.Beta || !reflect.DeepEqual(left, right) {
					t.Errorf("attempt %d Go %+v Source %+v", i, a, b)
				}
			}
			_, sourceFailed := results[0]["error"]
			if (nativeErr != nil) != sourceFailed {
				t.Fatalf("outcome Go %v Source %s", nativeErr, output)
			}
		})
	}
}
