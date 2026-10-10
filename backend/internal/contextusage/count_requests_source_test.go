package contextusage

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
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestCountingRequestProducersMatchActualSourceAPIAndHaiku(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun not configured")
	}
	for _, profile := range []string{"direct", "disabled-1m", "capability", "override-haiku"} {
		t.Run(profile, func(t *testing.T) {
			type wire struct {
				Path, Beta string
				Body       protocol.JSONObject
			}
			var mu sync.Mutex
			rows := []wire{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var obj protocol.JSONObject
				_ = obj.UnmarshalJSON(body)
				mu.Lock()
				rows = append(rows, wire{r.URL.RequestURI(), r.Header.Get("anthropic-beta"), obj})
				mu.Unlock()
				w.Header().Set("content-type", "application/json")
				if r.URL.Path == "/v1/messages/count_tokens" {
					io.WriteString(w, `{"input_tokens":37}`)
				} else {
					io.WriteString(w, `{"id":"count-response","type":"message","role":"assistant","model":"deepseek-flash","content":[{"type":"text","text":"x"}],"stop_reason":"max_tokens","stop_sequence":null,"usage":{"input_tokens":19,"output_tokens":1,"cache_creation_input_tokens":7,"cache_read_input_tokens":11}}`)
				}
			}))
			defer peer.Close()
			allocation := t.TempDir()
			home := filepath.Join(allocation, "source-home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"ANTHROPIC_API_KEY": "offline-count-source", "ANTHROPIC_MODEL": "deepseek-flash[1m]", "ANTHROPIC_SMALL_FAST_MODEL": "deepseek-flash[1m]", "ANTHROPIC_BASE_URL": peer.URL, "CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_EXTRA_METADATA": `{"fixture":"actual-original-input"}`, "CLAUDE_CODE_EXTRA_BODY": `{"temperature":0.25,"native_unknown":"kept"}`, "ANTHROPIC_BETAS": "explicit-beta, explicit-beta"}
			settings := protocol.NewJSONObject()
			switch profile {
			case "disabled-1m":
				env["CLAUDE_CODE_DISABLE_1M_CONTEXT"] = "true"
			case "capability":
				env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "deepseek-flash"
				env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "interleaved_thinking"
			case "override-haiku":
				settings = sourceObject(`{"modelOverrides":{"claude-haiku-4-5-20251001":"deepseek-flash[1m]"}}`)
			}
			messages := [][]protocol.JSONObject{
				{}, {sourceObject(`{"role":"user","content":"actual < & > 😀 text","unknown":"retained"}`)},
				{sourceObject(`{"role":"assistant","content":[{"type":"thinking","thinking":"actual thinking","signature":"s"},{"type":"text","text":"done"}]}`)},
				{sourceObject(`{"role":"assistant","content":[{"type":"redacted_thinking","data":"actual opaque"}]}`)},
				{sourceObject(`{"role":"assistant","content":[{"type":"tool_use","id":"actual-call","name":"mcp__actual__read","input":{"file":"actual"},"caller":{"type":"direct"},"extra":"strip"}]}`), sourceObject(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"actual-call","content":[{"type":"tool_reference","tool_name":"mcp__actual__read"}]}]}`)},
				{sourceObject(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"actual-call","content":[{"type":"text","text":"keep"},{"type":"tool_reference","tool_name":"other"}],"unknown":"keep"}]}`)},
				{sourceObject(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"actual-call","content":[]}]}`)},
			}
			tools := []protocol.JSONObject{sourceObject(`{"name":"actual-tool","description":"actual schema","input_schema":{"type":"object","properties":{"x":{"type":"string"}}}}`)}
			inputs := []map[string]any{}
			for _, list := range messages {
				for _, fallback := range []bool{false, true} {
					inputs = append(inputs, map[string]any{"messages": list, "tools": tools, "fallback": fallback})
				}
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-token-count-requests.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_TOKEN_REQUEST_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
			input, _ := json.Marshal(map[string]any{"home": home, "url": peer.URL, "env": env, "model": "deepseek-flash[1m]", "settings": settings, "device": "fixture-device", "session": "00000000-0000-4000-8000-000000000000", "rows": inputs})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Results    []float64
				Betas      []string
				SmallModel string
			}
			if err = json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			betas, err := requestcontext.DirectCountingBetas("deepseek-flash[1m]", env, settings)
			if err != nil || !reflect.DeepEqual(betas, source.Betas) {
				t.Fatalf("beta producer: Go %v Source %v error %v", betas, source.Betas, err)
			}
			meta, err := requestcontext.CountingMetadata(env, "fixture-device", "00000000-0000-4000-8000-000000000000")
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			observed := append([]wire{}, rows...)
			mu.Unlock()
			if len(observed) != len(inputs) {
				t.Fatal("physical request count", len(observed), stderr.String())
			}
			for i, row := range observed {
				list := messages[i/2]
				var want protocol.JSONObject
				if i%2 == 0 {
					want = CountRequest("deepseek-flash[1m]", list, tools, betas)
				} else {
					want = HaikuCountRequest(source.SmallModel, list, tools, betas, meta, requestcontext.CountingExtraBody(env))
				}
				want.Delete("betas")
				var left, right any
				a, _ := want.MarshalJSON()
				b, _ := row.Body.MarshalJSON()
				_ = json.Unmarshal(a, &left)
				_ = json.Unmarshal(b, &right)
				if !reflect.DeepEqual(left, right) {
					t.Errorf("row %d request: Go %s Source %s", i, a, b)
				}
				if source.Results[i] != 37 {
					t.Errorf("row %d count %v", i, source.Results[i])
				}
			}
		})
	}
}
