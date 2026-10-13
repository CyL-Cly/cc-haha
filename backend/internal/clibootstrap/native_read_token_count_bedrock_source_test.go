package clibootstrap

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceBedrockFoundationReadCountAWSBodyAndFailure(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, status := range []int{200, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			type observed struct {
				Path string
				Body json.RawMessage
			}
			var mu sync.Mutex
			requests := []observed{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, observed{r.URL.RequestURI(), append(json.RawMessage(nil), body...)})
				mu.Unlock()
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					io.WriteString(w, `{"inputTokens":37}`)
				} else {
					io.WriteString(w, `{"message":"offline count error","__type":"InternalServerException"}`)
				}
			}))
			defer peer.Close()
			model, content := "anthropic.claude-sonnet-4-5-20250929-v1:0", strings.Repeat("AWS_READ_<>&😀\u2028", 2000)
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				n.Environment["CLAUDE_CODE_USE_BEDROCK"] = "1"
				n.Environment["AWS_ACCESS_KEY_ID"] = "owned-count-access"
				n.Environment["AWS_SECRET_ACCESS_KEY"] = "owned-count-secret"
				n.Environment["ANTHROPIC_BEDROCK_BASE_URL"] = peer.URL
				n.Environment["AWS_REGION"] = "us-east-1"
			}, func(o *MainPrintOptions) { o.Arguments.Model = model; o.Arguments.Tools = []string{"Read"} })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var got *float64
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				got, err = m.nativeReadTokenCount(ctx, scope, content)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			goRequests := append([]observed{}, requests...)
			requests = nil
			mu.Unlock()
			allocation := t.TempDir()
			home := filepath.Join(allocation, "source-home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-bedrock-read-count.fixture.ts")
			input, _ := json.Marshal(map[string]any{"home": home, "url": peer.URL, "env": map[string]string{"AWS_ACCESS_KEY_ID": "owned-count-access", "AWS_SECRET_ACCESS_KEY": "owned-count-secret", "ANTHROPIC_BEDROCK_BASE_URL": peer.URL, "AWS_REGION": "us-east-1"}, "credentials": map[string]string{"accessKeyId": "owned-count-access", "secretAccessKey": "owned-count-secret"}, "rows": []map[string]any{{"model": model, "content": content, "betas": []string{"claude-code-20250219"}}}})
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_BEDROCK_READ_COUNT_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var want []*float64
			if err := json.Unmarshal(output, &want); err != nil {
				t.Fatal(err, string(output))
			}
			mu.Lock()
			sourceRequests := append([]observed{}, requests...)
			mu.Unlock()
			if len(want) != 1 || !reflect.DeepEqual(got, want[0]) {
				t.Fatal("Source Bedrock count response differs", got, want)
			}
			if !reflect.DeepEqual(goRequests, sourceRequests) {
				t.Fatalf("actual AWS body/path/attempt count differs: GoN=%d SourceN=%d GoPath=%s SourcePath=%s bodyEqual=%v", len(goRequests), len(sourceRequests), goRequests[0].Path, sourceRequests[0].Path, bytes.Equal(goRequests[0].Body, sourceRequests[0].Body))
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
