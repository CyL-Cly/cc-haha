package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func jsonSchemaCLIResponse(w http.ResponseWriter, id, tool string, input any, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	content := map[string]any{"type": "text", "text": ""}
	delta := map[string]any{"type": "text_delta", "text": text}
	stop := "end_turn"
	if tool != "" {
		content = map[string]any{"type": "tool_use", "id": id, "name": tool, "input": map[string]any{}}
		data, _ := json.Marshal(input)
		delta = map[string]any{"type": "input_json_delta", "partial_json": string(data)}
		stop = "tool_use"
	}
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": content},
		{"type": "content_block_delta", "index": 0, "delta": delta},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}},
		{"type": "message_stop"},
	}
	for _, event := range events {
		data, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
}

type schemaCLIStreamWriter struct {
	all     bytes.Buffer
	pending []byte
	results chan struct{}
}

func (w *schemaCLIStreamWriter) Write(data []byte) (int, error) {
	w.all.Write(data)
	w.pending = append(w.pending, data...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := w.pending[:i]
		var v protocol.JSONObject
		if v.UnmarshalJSON(line) == nil && v.String("type") == "result" {
			w.results <- struct{}{}
		}
		w.pending = w.pending[i+1:]
	}
	return len(data), nil
}

// Send a new SDK request only after the preceding actual result is observed.
// Source may coalesce/interrupt input arriving during an existing ASK.
func runThreeSchemaInputs(cmd *exec.Cmd, stdout, stderr *bytes.Buffer) error {
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	stream := &schemaCLIStreamWriter{results: make(chan struct{}, 3)}
	cmd.Stdout = stream
	cmd.Stderr = stderr
	if e = cmd.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	lines := strings.Split(strings.TrimSuffix(jsonSchemaThreeInputs(), "\n"), "\n")
	for _, line := range lines {
		if _, e = io.WriteString(in, line+"\n"); e != nil {
			in.Close()
			<-done
			stdout.Write(stream.all.Bytes())
			return e
		}
		select {
		case <-stream.results:
		case e = <-done:
			stdout.Write(stream.all.Bytes())
			return fmt.Errorf("SDK process exited before request result: %w", e)
		}
	}
	in.Close()
	e = <-done
	stdout.Write(stream.all.Bytes())
	return e
}

func jsonSchemaThreeInputs() string {
	var input strings.Builder
	for i := 1; i <= 3; i++ {
		input.WriteString(fmt.Sprintf("{\"type\":\"user\",\"uuid\":\"10000000-0000-4000-8000-00000000000%d\",\"message\":{\"role\":\"user\",\"content\":\"schema turn %d\"}}\n", i, i))
	}
	return input.String()
}
func assertThreeSchemaResults(t *testing.T, output []byte, calls, code int) {
	t.Helper()
	results := []protocol.JSONObject{}
	for _, line := range bytes.Split(output, []byte("\n")) {
		var r protocol.JSONObject
		if r.UnmarshalJSON(line) == nil && r.String("type") == "result" {
			results = append(results, r)
		}
	}
	if code != 1 || calls != 5 || len(results) != 3 {
		t.Fatalf("three schema queries code%d calls%d results%d output %.3000s", code, calls, len(results), output)
	}
	first, ok := results[0].Object("structured_output")
	if results[0].String("subtype") != "success" || !ok || first.String("answer") != "PHYSICAL_SCHEMA_VALUE" || results[1].String("subtype") != "success" || results[1].Get("structured_output") != nil || results[2].String("subtype") != "error_max_structured_output_retries" || results[2].Get("structured_output") != nil || !bytes.Contains(results[2].Get("errors"), []byte("after 2 attempts")) {
		t.Fatalf("per ASK capture/count leaked %.4000s", output)
	}
}

func TestActualSourceJSONSchemaPublicCLIResultAndErrorBranches(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	schema := `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
	for _, profile := range []string{"success", "enforcement", "sdk-three-turns", "retry-limit", "valid-at-limit", "invalid-schema", "falsy-schema", "invalid-json"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
				t.Fatal(e)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-json-schema-cli.fixture.ts")
			value := schema
			if profile == "falsy-schema" {
				value = "false"
			}
			if profile == "invalid-schema" {
				value = `{"type":42}`
			}
			if profile == "invalid-json" {
				value = `{"broken"`
			}
			input, _ := json.Marshal(map[string]any{"homeDir": home, "profile": profile, "schema": value})
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_JSON_SCHEMA_CLI_FIXTURE=1", "CC_HAHA_JSON_SCHEMA_CLI_INPUT=" + string(input), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_MAX_RETRIES=0", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			var e error
			if profile == "sdk-three-turns" {
				e = runThreeSchemaInputs(cmd, &stdout, &stderr)
			} else {
				e = cmd.Run()
			}
			var exit *exec.ExitError
			if e != nil && !errors.As(e, &exit) {
				t.Fatal(e, stderr.String())
			}
			raw, e := os.ReadFile(filepath.Join(home, "original-schema-cli-receipt.json"))
			if e != nil {
				t.Fatal(e, stdout.String(), stderr.String())
			}
			var receipt struct {
				Code         int
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if e = json.Unmarshal(raw, &receipt); e != nil || len(receipt.Capabilities.Blocked) > 0 {
				t.Fatal(e, string(raw), stderr.String())
			}
			if profile == "invalid-json" {
				if receipt.Code != 1 || len(receipt.Requests) != 0 || stdout.Len() != 0 || stderr.Len() == 0 {
					t.Fatal("original malformed schema", string(raw), stdout.String(), stderr.String())
				}
				return
			}
			if profile == "sdk-three-turns" {
				assertThreeSchemaResults(t, stdout.Bytes(), len(receipt.Requests), receipt.Code)
				return
			}
			var result protocol.JSONObject
			if e = result.UnmarshalJSON(stdout.Bytes()); e != nil {
				t.Fatal(e, string(raw), stdout.String(), stderr.String())
			}
			if profile == "retry-limit" || profile == "valid-at-limit" {
				wantCalls := 2
				if profile == "valid-at-limit" {
					wantCalls = 1
				}
				if receipt.Code != 1 || len(receipt.Requests) != wantCalls || result.String("subtype") != "error_max_structured_output_retries" || result.Get("structured_output") != nil || string(result.Get("is_error")) != "true" {
					t.Fatal("original retry limit", string(raw), stdout.String(), stderr.String())
				}
				return
			}
			wantCalls := 3
			wantText := "PHYSICAL_SCHEMA_DONE"
			if profile == "invalid-schema" || profile == "falsy-schema" {
				wantCalls = 1
				wantText = "SCHEMA_DISABLED"
			}
			if receipt.Code != 0 || len(receipt.Requests) != wantCalls || result.String("subtype") != "success" || result.String("result") != wantText {
				t.Fatal("original success", string(raw), stdout.String(), stderr.String())
			}
			if profile == "success" {
				data, ok := result.Object("structured_output")
				if !ok || data.String("answer") != "PHYSICAL_SCHEMA_VALUE" {
					t.Fatal("original structured result", stdout.String())
				}
			}
		})
	}
}

// This invokes the actual public CLI parser and normal print MainPrint path;
// no test supplies a synthetic tool, schema binding or projected result.
func TestPublicJSONSchemaCLIActualPrivateSharedHarnessResultAndRetries(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-schema-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	schema := `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`
	for _, profile := range []string{"success", "enforcement", "sdk-three-turns", "retry-limit", "valid-at-limit", "invalid-schema", "falsy-schema", "invalid-json"} {
		t.Run(profile, func(t *testing.T) {
			private := filepath.Join(root, profile)
			home := filepath.Join(private, "home")
			config := filepath.Join(home, ".claude")
			work := filepath.Join(private, "work")
			tmp := filepath.Join(private, "tmp")
			for _, p := range []string{config, work, tmp} {
				if e := os.MkdirAll(p, 0700); e != nil {
					t.Fatal(e)
				}
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := calls.Add(1)
				var request struct{ Tools []protocol.JSONObject }
				_ = json.Unmarshal(body, &request)
				found := false
				for _, tool := range request.Tools {
					if tool.String("name") == "StructuredOutput" {
						found = true
						if string(tool.Get("input_schema")) != schema {
							var actual, want any
							_ = json.Unmarshal(tool.Get("input_schema"), &actual)
							_ = json.Unmarshal([]byte(schema), &want)
							a, _ := json.Marshal(actual)
							b, _ := json.Marshal(want)
							if string(a) != string(b) {
								t.Errorf("original schema lost %s", tool.Get("input_schema"))
							}
						}
					}
				}
				if profile == "invalid-schema" || profile == "falsy-schema" {
					if found {
						t.Error("invalid schema installed StructuredOutput")
					}
					jsonSchemaCLIResponse(w, "invalid-schema-text", "", nil, "SCHEMA_DISABLED")
					return
				}
				if !found {
					t.Errorf("public --tools Read filtered out schema-only StructuredOutput: %.1400s", body)
				}
				if profile == "valid-at-limit" {
					jsonSchemaCLIResponse(w, "schema-limit-valid", "StructuredOutput", map[string]any{"answer": "PHYSICAL_SCHEMA_VALUE"}, "")
					return
				}
				if profile == "sdk-three-turns" {
					if n == 1 {
						jsonSchemaCLIResponse(w, "schema-sdk-valid", "StructuredOutput", map[string]any{"answer": "PHYSICAL_SCHEMA_VALUE"}, "")
					} else if n <= 3 {
						jsonSchemaCLIResponse(w, fmt.Sprint("schema-sdk-text", n), "", nil, "PHYSICAL_SCHEMA_DONE")
					} else {
						jsonSchemaCLIResponse(w, fmt.Sprint("schema-sdk-invalid", n), "StructuredOutput", map[string]any{"answer": 42}, "")
					}
					return
				}
				if profile == "enforcement" && n == 1 {
					jsonSchemaCLIResponse(w, "schema-ignored", "", nil, "Ignored structured instruction")
					return
				}
				if profile == "enforcement" && n == 2 {
					if !bytes.Contains(body, []byte("Stop hook feedback:\\nYou MUST call the StructuredOutput tool")) {
						t.Errorf("original Stop feedback absent %.1800s", body)
					}
					jsonSchemaCLIResponse(w, "schema-enforced-valid", "StructuredOutput", map[string]any{"answer": "PHYSICAL_SCHEMA_VALUE"}, "")
					return
				}
				if n == 1 || profile == "retry-limit" {
					jsonSchemaCLIResponse(w, fmt.Sprint("schema-", n), "StructuredOutput", map[string]any{"answer": 42}, "")
					return
				}
				if n == 2 {
					if !bytes.Contains(body, []byte("Output does not match required schema")) {
						t.Errorf("original validation error absent %.1800s", body)
					}
					jsonSchemaCLIResponse(w, "schema-valid", "StructuredOutput", map[string]any{"answer": "PHYSICAL_SCHEMA_VALUE"}, "")
					return
				}
				jsonSchemaCLIResponse(w, "schema-final", "", nil, "PHYSICAL_SCHEMA_DONE")
			}))
			defer peer.Close()
			env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_API_KEY=private-schema-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0"}
			value := schema
			if profile == "valid-at-limit" {
				env = append(env, "MAX_STRUCTURED_OUTPUT_RETRIES=1")
			}
			if profile == "retry-limit" || profile == "sdk-three-turns" {
				env = append(env, "MAX_STRUCTURED_OUTPUT_RETRIES=2")
			}
			if profile == "falsy-schema" {
				value = "false"
			}
			if profile == "invalid-schema" {
				value = `{"type":42}`
			}
			if profile == "invalid-json" {
				value = `{"broken"`
			}
			args := []string{"--print", "--no-computer-use", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Actual schema CLI.", "--tools", "Read", "--json-schema", value, "--output-format", "json", "Return the requested schema"}
			if profile == "sdk-three-turns" {
				args = args[:len(args)-1]
				for i := range args {
					if args[i] == "json" {
						args[i] = "stream-json"
					}
				}
				args = append(args, "--verbose", "--input-format", "stream-json")
			}
			if profile != "enforcement" {
				args = append([]string{"--bare"}, args...)
			}
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir = work
			cmd.Env = env
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			code := 0
			var runError error
			if profile == "sdk-three-turns" {
				runError = runThreeSchemaInputs(cmd, &out, &stderr)
			} else {
				runError = cmd.Run()
			}
			if e := runError; e != nil {
				var exit *exec.ExitError
				if !errors.As(e, &exit) {
					t.Fatal(e)
				}
				code = exit.ExitCode()
			}
			if profile == "sdk-three-turns" {
				assertThreeSchemaResults(t, out.Bytes(), int(calls.Load()), code)
				return
			}
			var result protocol.JSONObject
			if profile == "invalid-json" {
				if code != 1 || calls.Load() != 0 || out.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("actual malformed JSON code=%d calls=%d out=%s stderr=%s", code, calls.Load(), out.String(), stderr.String())
				}
				return
			}
			if e := result.UnmarshalJSON(out.Bytes()); e != nil {
				t.Fatalf("actual CLI result code=%d calls=%d %v stdout=%s stderr=%s", code, calls.Load(), e, out.String(), stderr.String())
			}
			if profile == "retry-limit" || profile == "valid-at-limit" {
				wantCalls := int32(2)
				wantError := "after 2 attempts"
				if profile == "valid-at-limit" {
					wantCalls = 1
					wantError = "after 1 attempts"
				}
				if code != 1 || calls.Load() != wantCalls || result.String("subtype") != "error_max_structured_output_retries" || string(result.Get("is_error")) != "true" || result.Get("structured_output") != nil || !bytes.Contains(result.Get("errors"), []byte(wantError)) {
					t.Fatalf("actual retry terminal code=%d calls=%d %s stderr=%s", code, calls.Load(), out.String(), stderr.String())
				}
				return
			}
			wantCalls := int32(3)
			wantText := "PHYSICAL_SCHEMA_DONE"
			if profile == "invalid-schema" || profile == "falsy-schema" {
				wantCalls = 1
				wantText = "SCHEMA_DISABLED"
			}
			if code != 0 || calls.Load() != wantCalls || result.String("subtype") != "success" || result.String("result") != wantText {
				t.Fatalf("actual success code=%d calls=%d %s stderr=%s", code, calls.Load(), out.String(), stderr.String())
			}
			if profile == "success" || profile == "enforcement" {
				structured, ok := result.Object("structured_output")
				if !ok || structured.String("answer") != "PHYSICAL_SCHEMA_VALUE" {
					t.Fatal("actual result structured_output missing", out.String())
				}
			} else if result.Get("structured_output") != nil {
				t.Fatal("invalid schema fabricated output", out.String())
			}
		})
	}
}
