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
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualPublicGoCLIDoctorDismissAndFollowingPhysicalReadPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTY is not native Windows evidence")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-doctor-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, dismiss := range []string{"enter", "escape"} {
		t.Run(dismiss, func(t *testing.T) {
			home, _ := startupFlagHome(t, root, dismiss, false)
			proof := filepath.Join(home, "proof.txt")
			if e = os.WriteFile(proof, []byte("DOCTOR_PHYSICAL_READ_MARKER"), 0600); e != nil {
				t.Fatal(e)
			}
			var model, channels atomic.Int32
			release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				channels.Add(1)
				fmt.Fprint(w, `[{"tag_name":"v3.0.1","assets":[{"name":"claude-haha-cli-manifest.json"}]}]`)
			}))
			defer release.Close()
			provider := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := model.Add(1)
				if n == 1 && !bytes.Contains(body, []byte("Read proof.txt with the actual Read")) {
					t.Error("Doctor or unrelated source opened model", string(body))
				}
				if n == 2 && (!bytes.Contains(body, []byte("DOCTOR_PHYSICAL_READ_MARKER")) || bytes.Contains(body, []byte(`"is_error":true`))) {
					t.Error("actual Read did not reach next provider request", string(body))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(kind string, value any) {
					data, _ := json.Marshal(value)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
				}
				emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprint("doctor-", n), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
				stop := "end_turn"
				block := map[string]any{"type": "text", "text": ""}
				delta := map[string]any{"type": "text_delta", "text": "DOCTOR_PUBLIC_NEXT_READ_OK"}
				if n == 1 {
					stop = "tool_use"
					block = map[string]any{"type": "tool_use", "id": "doctor-read", "name": "Read", "input": map[string]any{}}
					delta = map[string]any{"type": "input_json_delta", "partial_json": string(ideCLIJSON(map[string]any{"file_path": proof}))}
				}
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta})
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
				emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
				emit("message_stop", map[string]string{"type": "message_stop"})
			}))
			defer provider.Close()
			flags := ideCLIJSON([]string{"--bare", "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--tools", "Read", "--allowedTools", "Read", "--max-turns", "4"})
			python := exec.CommandContext(ctx, "/usr/bin/python3", "testdata/doctor.py", binary, home, dismiss, string(flags))
			python.Env = append(resumeErrorEnv(home, root, provider.URL), "CC_HAHA_CLI_RELEASE_API="+release.URL, "TERM=xterm-256color")
			capturePublicGoCoverage(t, python)
			out, e := python.CombinedOutput()
			if e != nil || !bytes.Contains(out, []byte(`"restored": true`)) {
				t.Fatal("production CLI Doctor PTY", e, string(out))
			}
			if model.Load() != 2 || channels.Load() != 2 {
				t.Fatal("actual Source no-ASK local command cardinality changed", model.Load(), channels.Load())
			}
			data, e := os.ReadFile(proof)
			if e != nil || string(data) != "DOCTOR_PHYSICAL_READ_MARKER" {
				t.Fatal("Doctor/Read mutated proof", e, string(data))
			}
		})
	}
}
