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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceLargeReadValidationAndNativeOwnerCounter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		content, extension, response string
		status                       int
	}
	rows := []row{
		{strings.Repeat("a", 25000), "txt", `{"input_tokens":90000}`, 0},
		{strings.Repeat("b", 25002), "txt", `{"input_tokens":25000}`, 0},
		{strings.Repeat("c", 25002), "txt", `{"input_tokens":25000.25}`, 0},
		{strings.Repeat("d", 28000), "txt", `{"input_tokens":null}`, 0},
		{strings.Repeat("e", 28000), "txt", `{"input_tokens":"99000"}`, 0},
		{strings.Repeat("f", 28000), "txt", `{"input_tokens":-3}`, 0},
		{strings.Repeat("g", 28000), "txt", `{"unrecognized":10}`, 0},
		{strings.Repeat("h", 100004), "txt", "", 404},
		{strings.Repeat("i", 12501), "json", `{"input_tokens":42}`, 0},
		{strings.Repeat("j", 50002), "jsonl", "", 404},
		{strings.Repeat("😀", 14000), "jsonc", `{"input_tokens":25001}`, 0},
		{strings.Repeat("k", 28000), "txt", `{broken`, 0},
		{strings.Repeat("l", 28000), "txt", `{"input_tokens":1e400}`, 0},
		{strings.Repeat("m", 28000), "txt", `{"input_tokens":-1e400}`, 0},
		{strings.Repeat("<>&\u2028\u2029😀", 4000), "txt", `{"input_tokens":37}`, 0},
	}
	type observed struct {
		Path, Beta string
		Body       json.RawMessage
	}
	var mu sync.Mutex
	observations := []observed{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		observations = append(observations, observed{r.URL.RequestURI(), r.Header.Get("anthropic-beta"), append(json.RawMessage(nil), body...)})
		mu.Unlock()
		var payload struct{ Messages []struct{ Content string } }
		if json.Unmarshal(body, &payload) != nil || len(payload.Messages) != 1 {
			t.Error("unexpected Source count body")
			http.Error(w, "bad", 400)
			return
		}
		for _, row := range rows {
			if payload.Messages[0].Content == row.content {
				if row.status != 0 {
					http.Error(w, `{"type":"error","error":{"type":"not_found_error","message":"count unsupported"}}`, row.status)
				} else {
					w.Header().Set("content-type", "application/json")
					io.WriteString(w, row.response)
				}
				return
			}
		}
		t.Error("unowned count input")
		http.Error(w, "unknown", 400)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	goResults := []map[string]any{}
	for i, row := range rows {
		path := filepath.Join(m.options.Native.CWD, "native-count-"+string(rune('a'+i))+"."+row.extension)
		if err := os.WriteFile(path, []byte(row.content), 0600); err != nil {
			t.Fatal(err)
		}
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			_, err := m.nativeTools.Call(ctx, "Read", protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(path)}), scope, false)
			return err
		})
		var message any
		if err != nil {
			message = err.Error()
		}
		goResults = append(goResults, map[string]any{"error": message})
	}
	mu.Lock()
	goObservations := append([]observed{}, observations...)
	observations = nil
	mu.Unlock()
	allocation := t.TempDir()
	home := filepath.Join(allocation, "source-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-read-token-count.fixture.ts")
	inputRows := []map[string]any{}
	for _, row := range rows {
		inputRows = append(inputRows, map[string]any{"content": row.content, "extension": row.extension})
	}
	input, _ := json.Marshal(map[string]any{"home": home, "url": peer.URL, "session": m.Controller.SessionID(), "device": "offline-count-device", "model": "fixture-model", "env": map[string]string{"ANTHROPIC_API_KEY": "offline-background-agent", "CLAUDE_CODE_SIMPLE": "1", "ENABLE_TOOL_SEARCH": "false"}, "rows": inputRows})
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_READ_TOKEN_COUNT_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var sourceResults []map[string]any
	if err := json.Unmarshal(output, &sourceResults); err != nil {
		t.Fatal(err, string(output))
	}
	if !reflect.DeepEqual(goResults, sourceResults) {
		t.Fatalf("actual Source Read validation differs: Go=%+v Source=%+v", goResults, sourceResults)
	}
	mu.Lock()
	sourceObservations := append([]observed{}, observations...)
	mu.Unlock()
	if !reflect.DeepEqual(goObservations, sourceObservations) {
		t.Fatal("actual raw Read count request body/path/betas differ", len(goObservations), len(sourceObservations))
	}
	if len(goObservations) != len(rows)-1 {
		t.Fatal("Source Read threshold or failure sampling changed", len(goObservations))
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
