package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

func harnessResultProjection(t *testing.T, output []byte, stream bool) []map[string]any {
	t.Helper()
	rows := [][]byte{output}
	if len(bytes.TrimSpace(output)) > 0 && bytes.TrimSpace(output)[0] == '[' {
		var array []json.RawMessage
		if err := json.Unmarshal(output, &array); err != nil {
			t.Fatal(err, string(output))
		}
		rows = nil
		for _, raw := range array {
			rows = append(rows, raw)
		}
	}
	if stream {
		rows = bytes.Split(bytes.TrimSpace(output), []byte{'\n'})
	}
	var results []map[string]any
	for _, row := range rows {
		var value map[string]any
		if err := json.Unmarshal(row, &value); err != nil {
			t.Fatal(err, string(output))
		}
		if value["type"] != "result" {
			continue
		}
		for _, field := range []string{"duration_ms", "duration_api_ms", "uuid"} {
			delete(value, field)
		}
		results = append(results, value)
	}
	return results
}

func TestActualSourceAndPublicCLIBudgetFallbackWorkload(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "actual-harness-policy-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-resume-errors.fixture.ts")
	for _, row := range []struct {
		name, behavior, model, fallback, budget, workload string
		twoASKs                                           bool
	}{
		{name: "first text charges after final yield", model: "claude-sonnet-4-6", budget: "0.000001"},
		{name: "physical write then budget stop", behavior: "budget-tool", model: "claude-sonnet-4-6", budget: "0.000001"},
		{name: "denied real Edit budget reports permission denial", behavior: "budget-denied", model: "claude-sonnet-4-6", budget: "0.000001"},
		{name: "denied real Edit success result and next ASK reset", behavior: "budget-denied", model: "claude-sonnet-4-6", twoASKs: true},
		{name: "exact decimal price boundary", behavior: "budget-tool", model: "claude-sonnet-4-6", budget: "0.000018"},
		{name: "second ASK cumulative budget", model: "claude-sonnet-4-6", budget: "0.000001", twoASKs: true},
		{name: "actual 529 model fallback and next ASK reset", behavior: "fallback", model: "claude-opus-4-5-20251101", fallback: "claude-sonnet-4-6", workload: "cron", twoASKs: true},
		{name: "fallback tool continuation keeps selected model", behavior: "fallback-tool", model: "claude-opus-4-5-20251101", fallback: "claude-sonnet-4-6", workload: "cron"},
		{name: "failed stream then fallback clears partial history", behavior: "fallback-stream", model: "claude-opus-4-5-20251101", fallback: "claude-sonnet-4-6", workload: "cron", twoASKs: true},
		{name: "default fallback resolves provider default", behavior: "fallback", model: "claude-opus-4-5-20251101", fallback: "default"},
		{name: "401 does not switch model", behavior: "authentication-error", model: "claude-opus-4-5-20251101", fallback: "claude-sonnet-4-6"},
	} {
		t.Run(row.name, func(t *testing.T) {
			sourceHome := resumeErrorHome(t, root, "source-"+strings.ReplaceAll(row.name, " ", "-"))
			goHome := resumeErrorHome(t, root, "go-"+strings.ReplaceAll(row.name, " ", "-"))
			args := func(home string) []string {
				a := []string{"--bare", "--print", "--model", row.model, "--thinking", "disabled", "--no-computer-use", "--session-id", resumeErrorInitialSID, "--system-prompt", "Private original Harness policy", "--output-format", "json", "--tools", ""}
				if row.behavior == "budget-tool" || row.behavior == "fallback-tool" || row.behavior == "budget-denied" {
					a[len(a)-1] = "Edit"
					if row.behavior != "budget-denied" {
						a = append(a, "--dangerously-skip-permissions")
					}
					a = append(a, "--verbose")
				}
				if row.budget != "" {
					a = append(a, "--max-budget-usd", row.budget)
				}
				if row.fallback != "" {
					a = append(a, "--fallback-model", row.fallback)
				}
				if row.workload != "" {
					a = append(a, "--workload", row.workload)
				}
				if row.twoASKs {
					a = append(a, "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
					return a
				}
				return append(a, "Actual policy instruction")
			}
			var mu sync.Mutex
			var requests []json.RawMessage
			var agents []string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, append(json.RawMessage(nil), body...))
				agents = append(agents, r.Header.Get("User-Agent"))
				count := len(requests)
				mu.Unlock()
				if (row.behavior == "fallback" || row.behavior == "fallback-tool") && count <= 3 || row.behavior == "fallback-stream" && count >= 2 && count <= 4 {
					w.Header().Set("content-type", "application/json")
					w.Header().Set("retry-after-ms", "1")
					w.WriteHeader(529)
					fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Private scripted overload"}}`)
					return
				}
				if row.behavior == "fallback-stream" && count == 1 {
					w.Header().Set("content-type", "text/event-stream")
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"failed-partial-stream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-4-5-20251101\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"FAILED_PARTIAL_STREAM\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
					return
				}
				if row.behavior == "authentication-error" {
					w.Header().Set("content-type", "application/json")
					w.WriteHeader(401)
					fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"Private scripted authentication failure"}}`)
					return
				}
				write := (row.behavior == "budget-tool" || row.behavior == "budget-denied") && count == 1 || row.behavior == "fallback-tool" && count == 4
				if write {
					jsonSchemaCLIResponse(w, "actual-budget-write-call", "Edit", map[string]string{"file_path": filepath.Join(goHome, "budget-artifact.txt"), "old_string": "", "new_string": "ACTUAL_NATIVE_WRITE_BEFORE_LIMIT\n"}, "")
					return
				}
				jsonSchemaCLIResponse(w, "normal-resume-response", "", nil, "PUBLIC_RESUME_NORMAL_OK")
			}))
			defer peer.Close()
			input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "args": args(sourceHome), "success": true, "behavior": row.behavior, "writePath": filepath.Join(sourceHome, "budget-artifact.txt")})
			source := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			source.Dir = sourceHome
			source.Env = append(resumeErrorEnv(sourceHome, root, peer.URL), "PATH=/usr/bin:/bin", "CLAUDE_CODE_MAX_RETRIES=2", "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-fallback", "CC_HAHA_RESUME_ERROR_FIXTURE=1", "CC_HAHA_RESUME_ERROR_INPUT="+string(input))
			actual := exec.CommandContext(ctx, binary, args(goHome)...)
			actual.Dir = goHome
			actual.Env = append(resumeErrorEnv(goHome, root, peer.URL), "CLAUDE_CODE_MAX_RETRIES=2", "ANTHROPIC_DEFAULT_SONNET_MODEL=fixture-fallback")
			sequentialASKs := row.twoASKs && row.behavior == "budget-denied"
			if row.twoASKs && !sequentialASKs {
				stdin := `{"type":"user","message":{"role":"user","content":"FIRST_ASK"}}` + "\n" + `{"type":"user","message":{"role":"user","content":"NEXT_ASK"}}` + "\n"
				source.Stdin = strings.NewReader(stdin)
				actual.Stdin = strings.NewReader(stdin)
			}
			run := func(cmd *exec.Cmd) (int, []byte, []byte) {
				if sequentialASKs {
					return runHarnessSequentialASKs(t, cmd)
				}
				return resumeErrorRun(t, cmd, false)
			}
			sc, so, se := run(source)
			receipt, err := os.ReadFile(filepath.Join(sourceHome, "source-resume-error-receipt.json"))
			if err != nil {
				t.Fatal(err, string(so), string(se))
			}
			var original struct {
				Requests        []json.RawMessage
				WorkloadHeaders []string
				Capabilities    struct{ Blocked []string }
			}
			if err := json.Unmarshal(receipt, &original); err != nil || len(original.Capabilities.Blocked) != 0 {
				t.Fatal(err, string(receipt))
			}
			gc, goOutput, goError := run(actual)
			if sc != gc || len(se) != 0 || len(goError) != 0 {
				t.Fatalf("actual process outcome source%d Go%d Sourceerr%s Goerr%s Sourceout%s Goout%s", sc, gc, se, goError, so, goOutput)
			}
			want, got := harnessResultProjection(t, so, row.twoASKs), harnessResultProjection(t, goOutput, row.twoASKs)
			if row.behavior == "budget-denied" {
				for _, result := range want {
					denials, _ := result["permission_denials"].([]any)
					for _, raw := range denials {
						denial, _ := raw.(map[string]any)
						input, _ := denial["tool_input"].(map[string]any)
						if path, ok := input["file_path"].(string); ok {
							input["file_path"] = strings.Replace(path, sourceHome, goHome, 1)
						}
					}
				}
				for _, home := range []string{sourceHome, goHome} {
					if _, err := os.Stat(filepath.Join(home, "budget-artifact.txt")); !os.IsNotExist(err) {
						t.Fatal("denied actual Edit changed private filesystem", home, err)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("actual Source results %s\nGo results %s", mustJSON(want), mustJSON(got))
			}
			mu.Lock()
			actualRequests := append([]json.RawMessage(nil), requests...)
			actualAgents := append([]string(nil), agents...)
			mu.Unlock()
			if len(actualRequests) != len(original.Requests) {
				t.Fatalf("request count Source%d Go%d Sourceout%s Goout%s", len(original.Requests), len(actualRequests), so, goOutput)
			}
			for i, request := range actualRequests {
				var a, b struct {
					Model        string
					Messages     json.RawMessage
					OutputConfig json.RawMessage
				}
				_ = json.Unmarshal(request, &a)
				_ = json.Unmarshal(original.Requests[i], &b)
				if a.Model != b.Model {
					t.Error("physical request model", i, a.Model, b.Model)
				}
				if bytes.Contains(a.Messages, []byte("Switched to ")) || bytes.Contains(b.Messages, []byte("Switched to ")) {
					t.Error("informational fallback warning leaked into API context")
				}
				if bytes.Contains(a.Messages, []byte("FAILED_PARTIAL_STREAM")) || bytes.Contains(b.Messages, []byte("FAILED_PARTIAL_STREAM")) {
					t.Error("failed partial stream leaked into subsequent API context", i)
				}
				if strings.Contains(actualAgents[i], ", workload/cron") != strings.Contains(original.WorkloadHeaders[i], ", workload/cron") {
					t.Error("workload was not preserved across physical requests", i, actualAgents[i], original.WorkloadHeaders[i])
				}
			}
			if row.behavior == "budget-tool" || row.behavior == "fallback-tool" {
				for _, home := range []string{sourceHome, goHome} {
					data, err := os.ReadFile(filepath.Join(home, "budget-artifact.txt"))
					if err != nil || string(data) != "ACTUAL_NATIVE_WRITE_BEFORE_LIMIT\n" {
						t.Fatal("actual tool side effect", home, err, string(data), "Source output", string(so), "Go output", string(goOutput), "Source requests", mustJSON(original.Requests), "Go requests", mustJSON(actualRequests))
					}
				}
			}
		})
	}
}

func mustJSON(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }

// A second ASK starts after the first public result. Sending both inputs at
// once instead exercises queued-input consumption during the first tool turn.
func runHarnessSequentialASKs(t *testing.T, cmd *exec.Cmd) (int, []byte, []byte) {
	t.Helper()
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output := &authStatusProcessWriter{result: make(chan struct{}, 1)}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = output, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for _, text := range []string{"FIRST_ASK", "NEXT_ASK"} {
		line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q}}`+"\n", text)
		if _, err := io.WriteString(input, line); err != nil {
			t.Fatal(err)
		}
		select {
		case <-output.result:
		case err := <-done:
			t.Fatalf("public CLI exited before %s result: %v", text, err)
		case <-time.After(12 * time.Second):
			_ = cmd.Process.Kill()
			_ = input.Close()
			err := <-done
			output.mu.Lock()
			defer output.mu.Unlock()
			t.Fatalf("public CLI %s result timed out: %v output%s stderr%s", text, err, output.all.Bytes(), stderr.Bytes())
		}
	}
	_ = input.Close()
	err = <-done
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	return code, bytes.Clone(output.all.Bytes()), bytes.Clone(stderr.Bytes())
}
