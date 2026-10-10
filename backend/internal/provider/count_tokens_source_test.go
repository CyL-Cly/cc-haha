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

func TestNativeCountTokensUsesActualPinnedSDKPathBodyAndBetaHeaders(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	type observation struct {
		Path, Key, Beta string
		Body            json.RawMessage
	}
	var mu sync.Mutex
	observed := []observation{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		observed = append(observed, observation{r.URL.RequestURI(), r.Header.Get("x-api-key"), r.Header.Get("anthropic-beta"), body})
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"input_tokens":37}`)
	}))
	defer server.Close()
	params := []protocol.JSONObject{}
	for _, raw := range []string{`{"model":"deepseek-flash","messages":[{"role":"user","content":"actual count request"}],"tools":[]}`, `{"model":"deepseek-flash","messages":[{"role":"user","content":"< & > 😀"}],"tools":[],"betas":["context-1m-2025-08-07","explicit-fixture-beta"]}`} {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		params = append(params, value)
	}
	client := NewAnthropic(AnthropicConfig{APIKey: "offline-count-key", BaseURL: server.URL, Timeout: 5 * time.Second, DefaultHeaders: http.Header{"Anthropic-Beta": []string{"original-client-default"}}})
	for _, value := range params {
		before, _ := value.MarshalJSON()
		result, err := client.CountTokens(context.Background(), value)
		if err != nil || !bytes.Contains(result.Raw, []byte(`37`)) {
			t.Fatal(err, string(result.Raw))
		}
		after, _ := value.MarshalJSON()
		if !bytes.Equal(before, after) {
			t.Fatal("counter changed caller params")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "source-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-count-tokens.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_COUNT_SDK_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"home": home, "url": server.URL, "params": params})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var results []any
	if err = json.Unmarshal(data, &results); err != nil {
		t.Fatal(err, string(data))
	}
	mu.Lock()
	rows := append([]observation{}, observed...)
	mu.Unlock()
	if len(rows) != 4 {
		t.Fatal("unexpected physical request count", len(rows))
	}
	for i := 0; i < 2; i++ {
		goRow, sourceRow := rows[i], rows[i+2]
		var goBody, sourceBody any
		_ = json.Unmarshal(goRow.Body, &goBody)
		_ = json.Unmarshal(sourceRow.Body, &sourceBody)
		if goRow.Path != sourceRow.Path || goRow.Key != sourceRow.Key || goRow.Beta != sourceRow.Beta || !reflect.DeepEqual(goBody, sourceBody) {
			t.Fatalf("count SDK mismatch: Go %+v Source %+v", goRow, sourceRow)
		}
		if !bytes.Equal(goRow.Body, sourceRow.Body) {
			t.Fatalf("count SDK serialized bytes differ: Go %q Source %q", goRow.Body, sourceRow.Body)
		}
	}
	if client.config.DefaultHeaders.Get("anthropic-beta") != "original-client-default" {
		t.Fatal("counter changed the existing Messages client headers")
	}
}
