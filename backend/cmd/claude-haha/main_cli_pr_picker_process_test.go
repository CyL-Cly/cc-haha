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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestActualPublicGoCLIFromPRPickerResumesSameSIDToolsAndCancelsBeforeModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual Unix PTY is not Windows ConPTY evidence")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-pr-picker-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, row := range []struct {
		name, filter, wantSID string
		search, cancel        bool
	}{{"number", "32", cliContinueA, false, false}, {"url", "https://github.com/a/b/pull/43", cliContinueB, false, false}, {"all-search", "", cliContinueB, true, false}, {"cancel", "", "", false, true}} {
		t.Run(row.name, func(t *testing.T) {
			home, _ := startupFlagHome(t, root, row.name, false)
			_ = os.WriteFile(filepath.Join(home, "pr-read-input.txt"), []byte("PR_PICKER_PHYSICAL_READ_INPUT"), 0600)
			directory := filepath.Join(home, ".claude", "projects", projectpath.Sanitize(home))
			_ = os.MkdirAll(directory, 0700)
			originals := map[string][]byte{}
			for i, session := range []struct {
				sid, title, marker string
				pr                 int
			}{{cliContinueA, "AlphaTitle", "PR_32_OLD_TASK", 32}, {cliContinueB, "BetaTitle", "PR_43_OLD_TASK", 43}} {
				data := []byte(fmt.Sprintf("{\"type\":\"user\",\"uuid\":\"old-user-%s\",\"parentUuid\":null,\"sessionId\":\"%s\",\"cwd\":%q,\"timestamp\":\"2026-10-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"%s\"}}\n{\"type\":\"assistant\",\"uuid\":\"old-answer-%s\",\"parentUuid\":\"old-user-%s\",\"sessionId\":\"%s\",\"timestamp\":\"2026-10-01T00:00:01Z\",\"message\":{\"id\":\"old-message\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[{\"type\":\"text\",\"text\":\"old completed PR response\"}],\"stop_reason\":\"end_turn\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n{\"type\":\"custom-title\",\"sessionId\":\"%s\",\"customTitle\":\"%s\"}\n{\"type\":\"pr-link\",\"sessionId\":\"%s\",\"prNumber\":%d,\"prRepository\":\"a/b\",\"prUrl\":\"https://github.com/a/b/pull/%d\"}\n", session.sid, session.sid, home, session.marker, session.sid, session.sid, session.sid, session.sid, session.title, session.sid, session.pr, session.pr))
				path := filepath.Join(directory, session.sid+".jsonl")
				_ = os.WriteFile(path, data, 0600)
				stamp := time.Unix(1700000000+int64(i), 0)
				_ = os.Chtimes(path, stamp, stamp)
				originals[path] = data
			}
			var mu sync.Mutex
			requests := [][]byte{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, append([]byte(nil), data...))
				index := len(requests)
				mu.Unlock()
				if row.cancel {
					t.Error("cancel reached provider before selection")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(event string, value any) {
					raw, _ := json.Marshal(value)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
				}
				emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("pr-%d", index), "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
				stop := "end_turn"
				kind := "text"
				if index == 1 {
					kind = "tool_use"
					stop = "tool_use"
				}
				block := map[string]any{"type": kind}
				if index == 1 {
					block["id"] = "pr-real-write"
					block["name"] = "Read"
					block["input"] = map[string]any{}
				} else {
					block["text"] = ""
				}
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
				delta := map[string]string{"type": "text_delta", "text": "PR_PICKER_TOOL_AND_HISTORY_OK"}
				if index == 1 {
					input, _ := json.Marshal(map[string]string{"file_path": filepath.Join(home, "pr-read-input.txt")})
					delta = map[string]string{"type": "input_json_delta", "partial_json": string(input)}
				}
				emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta})
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
				emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
				emit("message_stop", map[string]string{"type": "message_stop"})
			}))
			defer peer.Close()
			flags := []string{"--bare", "--from-pr"}
			if row.filter != "" {
				flags = append(flags, row.filter)
			}
			flags = append(flags, "--model", "fixture-model", "--thinking", "disabled", "--no-computer-use", "--tools", "Read", "--allowedTools", "Read", "--max-turns", "4")
			encoded, _ := json.Marshal(flags)
			python := exec.CommandContext(ctx, "/usr/bin/python3", "testdata/pr-picker.py", binary, home, row.name, string(encoded))
			python.Env = append(resumeErrorEnv(home, root, peer.URL), "TERM=xterm-256color")
			out, e := python.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			if !bytes.Contains(out, []byte(`"restored": true`)) {
				t.Fatal("actual PTY restoration missing", string(out))
			}
			mu.Lock()
			calls := append([][]byte{}, requests...)
			mu.Unlock()
			if row.cancel {
				if len(calls) != 0 {
					t.Fatal("cancelled picker reached model")
				}
			} else {
				if len(calls) != 2 {
					t.Fatal("expected actual tool continuation", len(calls))
				}
				wantMarker := "PR_32_OLD_TASK"
				if row.wantSID == cliContinueB {
					wantMarker = "PR_43_OLD_TASK"
				}
				if !bytes.Contains(calls[0], []byte(wantMarker)) || !bytes.Contains(calls[1], []byte("pr-real-write")) {
					t.Fatal("restored Agent history/tool continuation missing", string(calls[0]), string(calls[1]))
				}
				for _, call := range calls {
					var body struct {
						Metadata struct {
							UserID string `json:"user_id"`
						}
					}
					_ = json.Unmarshal(call, &body)
					if !strings.Contains(body.Metadata.UserID, row.wantSID) {
						t.Fatal("picker created a fresh SID", body.Metadata.UserID)
					}
				}
				if !bytes.Contains(calls[1], []byte("PR_PICKER_PHYSICAL_READ_INPUT")) || bytes.Contains(calls[1], []byte(`"is_error":true`)) {
					t.Fatal("actual Read result missing or failed", string(calls[1]))
				}
			}
			for path, before := range originals {
				after, _ := os.ReadFile(path)
				if row.cancel || !strings.Contains(path, row.wantSID) {
					if !bytes.Equal(before, after) {
						t.Fatal("picker changed unselected transcript", path)
					}
				} else if bytes.Count(after, []byte(`"uuid":"old-user-`+row.wantSID+`"`)) != 1 {
					t.Fatal("old UUID duplicated at resume")
				}
			}
		})
	}
}
