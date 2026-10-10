package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoverage893PublicMaxTurnsStopsAfterActualWriteWithoutSecondModelRequest(t *testing.T) {
	n := nativeForTest(t)
	raw, e := os.ReadFile("testdata/public-tool-model-inputs.json")
	if e != nil {
		t.Fatal(e)
	}
	raw = bytes.ReplaceAll(raw, []byte("${TEMP_ROOT}"), []byte(filepath.Dir(n.CWD)))
	var fixture struct {
		Responses []struct {
			Headers map[string]string `json:"responseHeaders"`
			SSE     []struct {
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			}
		} `json:"declaredProviderResponsesOnly"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	var delta struct {
		Delta struct {
			Partial string `json:"partial_json"`
		} `json:"delta"`
	}
	if e = json.Unmarshal(fixture.Responses[0].SSE[2].Data, &delta); e != nil {
		t.Fatal(e)
	}
	var toolInput struct {
		Path    string `json:"file_path"`
		Content string `json:"content"`
	}
	if e = json.Unmarshal([]byte(delta.Delta.Partial), &toolInput); e != nil {
		t.Fatal(e)
	}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number := posts.Add(1)
		body, e := io.ReadAll(r.Body)
		if e != nil || number != 1 || r.Method != "POST" || r.URL.RequestURI() != "/v1/messages?beta=true" {
			t.Error("unexpected original provider request", number, e, r.URL.RequestURI())
			http.Error(w, "unexpected", 422)
			return
		}
		if !bytes.Contains(body, []byte("Create the requested evidence file.")) {
			t.Error("original input lost", string(body))
		}
		for k, v := range fixture.Responses[0].Headers {
			w.Header().Set(k, v)
		}
		for _, f := range fixture.Responses[0].SSE {
			var compact bytes.Buffer
			if e := json.Compact(&compact, f.Data); e != nil {
				t.Error(e)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.Event, compact.Bytes())
		}
	}))
	defer server.Close()
	n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	args := mainRecipeArgs()
	for i := range args {
		if args[i] == "--max-turns" {
			args[i+1] = "1"
		}
	}
	parsed, e := ParsePrintArguments(args, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: parsed, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"Create the requested evidence file.\"}}\n"), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, e := p.Run(ctx)
	if e != nil || code != 1 {
		t.Fatal(code, e, errOut.String())
	}
	if posts.Load() != 1 {
		t.Fatal("max-turn terminal sent another model request", posts.Load())
	}
	disk, e := os.ReadFile(toolInput.Path)
	if e != nil || string(disk) != toolInput.Content {
		t.Fatal("allowed Write did not finish before max-turn terminal", e, string(disk))
	}
	var terminal map[string]any
	for _, line := range bytes.Split(out.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		if e = json.Unmarshal(line, &row); e != nil {
			t.Fatal(e)
		}
		if row["type"] == "result" {
			terminal = row
		}
	}
	if terminal["subtype"] != "error_max_turns" || terminal["is_error"] != true || terminal["num_turns"] != float64(2) {
		t.Fatal("actual max-turn policy result", terminal)
	}
	t.Logf("actual final stdout=%s disk=%q providerPOST=%d", out.String(), disk, posts.Load())
}
