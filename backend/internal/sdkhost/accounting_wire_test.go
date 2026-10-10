package sdkhost_test

import (
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func accountingWireBoundary(t *testing.T, firstWire string, control nativeControls) ([]byte, map[string]any) {
	t.Helper()
	data, e := os.ReadFile("testdata/source_contract.json")
	must(e)
	var items []frozenCase
	must(json.Unmarshal(data, &items))
	item := items[0]
	home, e := filepath.EvalSymlinks(t.TempDir())
	must(e)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("BUN_OPTIONS", "--no-env-file")
	t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", os.TempDir())
	t.Setenv("CC_HAHA_SESSION_CONTROLLER_FIXTURE", "1")
	t.Setenv("NODE_ENV", "production")
	t.Setenv("TZ", "UTC")
	t.Chdir(home)
	project := projectpath.Sanitize(home)
	replace := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "<home>", home), "<project>", project)
	}
	for path, value := range item.Files {
		path = replace(path)
		must(os.MkdirAll(filepath.Dir(path), 0700))
		must(os.WriteFile(path, []byte(replace(value)), 0600))
		must(os.Chtimes(path, time.UnixMilli(1700000000000), time.UnixMilli(1700000000000)))
	}
	ready, release := make(chan struct{}), make(chan struct{})
	var onceReady, onceRelease sync.Once
	var count int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__fixture/first-user-persisted":
			onceReady.Do(func() { close(ready) })
			_, _ = w.Write([]byte("OK"))
			return
		case "/__fixture/release-final":
			onceRelease.Do(func() { close(release) })
			_, _ = w.Write([]byte("OK"))
			return
		}
		count++
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 1 {
			select {
			case <-ready:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write([]byte(firstWire))
			return
		}
		_, _ = w.Write([]byte(item.Wires.SecondWire))
	}))
	defer server.Close()
	ca := "-----BEGIN CERTIFICATE-----\n" + base64.StdEncoding.EncodeToString(server.Certificate().Raw) + "\n-----END CERTIFICATE-----\n"
	must(os.WriteFile(filepath.Join(home, "original-ca.pem"), []byte(ca), 0600))
	item.Input.Home = home
	item.Input.Origin = server.URL
	stats := item.Input.Stat
	item.Input.Stat = map[string]resumestate.Stat{}
	_ = stats
	// raw original stat values are only path-relocated, not Source projections.
	for path, value := range stats {
		item.Input.Stat[replace(path)] = value
	}
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	t.Setenv("ANTHROPIC_API_KEY", "offline-fixture-key")
	return runNative(t, item.Input, control)
}

// Go-only native regression derived from original response inputs. This does
// not claim a newly executed TS/Go counterexample or full API error migration.
func TestActualSDKFinallyChargeIndependentOfResultSuccess(t *testing.T) {
	start := `event: message_start
data: {"type":"message_start","message":{"id":"original-start","type":"message","role":"assistant","model":"fixture-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":0}}}

`
	delta := `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

`
	for _, kind := range []string{"terminal-after-delta", "start-only"} {
		t.Run(kind, func(t *testing.T) {
			firstWire := start
			firstCost := 0.0
			if kind == "terminal-after-delta" {
				firstWire += delta
				firstCost = (5*3 + 9*15) / 1e6
			}
			stdout, result := accountingWireBoundary(t, firstWire, nativeControls{})
			if result["runError"] != nil || len(result["requests"].([]string)) != 2 {
				t.Fatalf("actual two ASK failed: %v", result["runError"])
			}
			var results []protocol.JSONObject
			for _, line := range splitAccountingLines(stdout) {
				v := object(line)
				if v.String("type") == "result" {
					results = append(results, v)
				}
			}
			if len(results) != 2 {
				t.Fatalf("result count %d", len(results))
			}
			if string(results[0].Get("is_error")) != "true" {
				t.Fatal("empty-content original stream did not reach API terminal")
			}
			var got float64
			if e := json.Unmarshal(results[1].Get("total_cost_usd"), &got); e != nil {
				t.Fatal(e)
			}
			want := firstCost + (13*3+7*15)/1e6
			if math.Abs(got-want) > 1e-15 {
				t.Fatalf("API terminal producer-finally cost got %.17g want %.17g", got, want)
			}
		})
	}
}
