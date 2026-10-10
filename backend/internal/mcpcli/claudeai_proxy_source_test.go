package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestClaudeAIProxyActualSourceSentTokenRetryAndPhysicalBodies(t *testing.T) {
	for _, row := range []map[string]any{{"status": 200}, {"status": 401}, {"status": 401, "change": true, "changedResult": true}, {"status": 401, "change": true, "throwRefresh": true}, {"status": 401, "changedResult": true}, {"status": 401, "change": true, "changedResult": true, "secondFailure": true}, {"missing": true}} {
		t.Run(string(mustJSON(row)), func(t *testing.T) {
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				t.Skip("actual Source Bun not configured")
			}
			type frame struct{ Authorization, Owned, Body string }
			var mu sync.Mutex
			frames := []frame{}
			ordinal := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				mu.Lock()
				ordinal++
				n := ordinal
				frames = append(frames, frame{r.Header.Get("Authorization"), r.Header.Get("X-Owned"), string(body)})
				mu.Unlock()
				if row["secondFailure"] == true && n == 2 {
					h := w.(http.Hijacker)
					conn, _, _ := h.Hijack()
					_ = conn.Close()
					return
				}
				status := 200
				if n == 1 {
					if v, ok := row["status"].(int); ok {
						status = v
					}
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "owned-response")
			}))
			defer server.Close()
			home := t.TempDir()
			input := map[string]any{"homeDir": home, "address": server.URL}
			for k, v := range row {
				input[k] = v
			}
			data, _ := json.Marshal(input)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			fixture, _ := filepath.Abs("claudeai_proxy.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDEAI_PROXY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			mu.Lock()
			sourceFrames := append([]frame{}, frames...)
			frames = nil
			ordinal = 0
			mu.Unlock()
			token := "fake-old"
			checks := 0
			handled := []string{}
			transport := &ClaudeAIProxyTransport{Inner: server.Client().Transport, CheckAndRefresh: func(context.Context) error { checks++; return nil }, Token: func(context.Context) (*string, error) {
				if row["missing"] == true {
					return nil, nil
				}
				value := token
				return &value, nil
			}, Handle401: func(_ context.Context, sent string) (bool, error) {
				handled = append(handled, sent)
				if row["change"] == true {
					token = "fake-new"
				}
				if row["throwRefresh"] == true {
					return false, errors.New("owned refresh error")
				}
				return row["changedResult"] == true, nil
			}}
			request, _ := http.NewRequestWithContext(ctx, "POST", server.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
			request.Header.Set("X-Owned", "retained")
			request.Header.Set("Authorization", "will-be-replaced")
			response, e := transport.RoundTrip(request)
			actual := map[string]any{"checks": checks, "handled": handled}
			if e != nil {
				actual["error"] = e.Error()
			} else {
				body, e := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if e != nil {
					t.Fatal(e)
				}
				actual["status"] = response.StatusCode
				actual["body"] = string(body)
			}
			var expected, decoded any
			encoded, _ := json.Marshal(actual)
			if json.Unmarshal(out, &expected) != nil || json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(expected, decoded) {
				t.Fatalf("Source %s Go %s", out, encoded)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(sourceFrames, frames) {
				t.Fatalf("actual physical request Source%+v Go%+v", sourceFrames, frames)
			}
		})
	}
}

func mustJSON(value any) []byte { out, _ := json.Marshal(value); return out }
