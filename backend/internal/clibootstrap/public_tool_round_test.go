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
)

func TestPublicMainRealWriteToolRoundCountsYieldedUser(t *testing.T) {
	n := nativeForTest(t)
	raw, err := os.ReadFile("testdata/public-tool-model-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("${TEMP_ROOT}"), []byte(filepath.Dir(n.CWD)))
	var fixture struct {
		Responses []struct {
			Headers map[string]string `json:"responseHeaders"`
			SSE     []struct {
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			} `json:"SSE"`
		} `json:"declaredProviderResponsesOnly"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var delta struct {
		Delta struct {
			Partial string `json:"partial_json"`
		} `json:"delta"`
	}
	if err = json.Unmarshal(fixture.Responses[0].SSE[2].Data, &delta); err != nil {
		t.Fatal(err)
	}
	var toolInput struct {
		Path    string `json:"file_path"`
		Content string `json:"content"`
	}
	if err = json.Unmarshal([]byte(delta.Delta.Partial), &toolInput); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinal := int(posts.Add(1))
		if r.Method != "POST" || r.URL.RequestURI() != "/v1/messages?beta=true" || ordinal > 2 {
			t.Error("undeclared provider request", r.Method, r.URL.RequestURI(), ordinal)
			http.Error(w, "unexpected request", 422)
			return
		}
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			http.Error(w, "body", 422)
			return
		}
		if ordinal == 2 {
			actual, e := os.ReadFile(toolInput.Path)
			if e != nil || string(actual) != toolInput.Content || !bytes.Contains(body, []byte(`"tool_use_id":"call_cli_write_once"`)) {
				t.Error("second request precedes real Write or result", e, string(actual), string(body))
				http.Error(w, "missing effect", 422)
				return
			}
		}
		response := fixture.Responses[ordinal-1]
		for key, value := range response.Headers {
			w.Header().Set(key, value)
		}
		for _, frame := range response.SSE {
			var compact bytes.Buffer
			if err := json.Compact(&compact, frame.Data); err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.Event, compact.Bytes())
		}
	}))
	defer server.Close()
	n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	args, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	input := strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"Create the requested evidence file.\"}}\n")
	p, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &out, Error: &errOut})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	code, err := p.Run(context.Background())
	if err != nil || code != 0 {
		t.Fatal(code, err, errOut.String())
	}
	if posts.Load() != 2 {
		t.Fatal("actual requests", posts.Load())
	}
	var result struct {
		Type  string `json:"type"`
		Turns int    `json:"num_turns"`
		Text  string `json:"result"`
	}
	for _, line := range bytes.Split(out.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var value struct {
			Type  string `json:"type"`
			Turns int    `json:"num_turns"`
			Text  string `json:"result"`
		}
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value.Type == "result" {
			result = value
		}
	}
	if result.Text != "FILE_CREATED" || result.Turns != 2 {
		t.Fatal("real query yielded user count", result)
	}
}
