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

func emitPublicDreamReply(w http.ResponseWriter, id, text, path string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(name string, value any) {
		raw, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
	}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 0}}})
	stop := "end_turn"
	block := map[string]any{"type": "text", "text": ""}
	delta := map[string]any{"type": "text_delta", "text": text}
	if path != "" {
		stop = "tool_use"
		block = map[string]any{"type": "tool_use", "id": "public-dream-write", "name": "Write", "input": map[string]any{}}
		raw, _ := json.Marshal(map[string]string{"file_path": path, "content": "PUBLIC_DREAM_MEMORY_PHYSICAL"})
		delta = map[string]any{"type": "input_json_delta", "partial_json": string(raw)}
	}
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
	emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta})
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 5}})
	emit("message_stop", map[string]string{"type": "message_stop"})
}

func TestActualPublicGoCLIAutoDreamDialogPhysicalWriteKillNextTurnAndDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTY is not Windows OS evidence")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-dream-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	capturePublicGoCoverage(t, build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for _, profile := range []string{"complete", "kill", "disabled"} {
		t.Run(profile, func(t *testing.T) {
			home, _ := startupFlagHome(t, root, profile, false)
			settings, _ := json.Marshal(map[string]any{"autoDreamEnabled": profile != "disabled", "autoMemoryEnabled": true, "disableAllHooks": true, "enabledPlugins": map[string]bool{}})
			if err := os.WriteFile(filepath.Join(home, ".claude/settings.json"), settings, 0600); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(home, ".claude/projects", projectpath.Sanitize(home))
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			originals := map[string][]byte{}
			for i := 0; i < 5; i++ {
				sid := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)
				path := filepath.Join(project, sid+".jsonl")
				body := []byte(fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"uuid\":%q,\"parentUuid\":null,\"message\":{\"role\":\"user\",\"content\":\"private durable project conventions\"}}\n", sid, "private-prior-"+sid))
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				originals[path] = body
			}
			memory := filepath.Join(home, "memory")
			file := filepath.Join(memory, "public-memory.md")
			var mu sync.Mutex
			roots, dreams := 0, 0
			cancelled := make(chan struct{}, 1)
			requests := [][]byte{}
			peer := httptest.NewServer(nativeCLIModelHTTPHandler(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				dream := bytes.Contains(body, []byte("# Dream: Memory Consolidation"))
				mu.Lock()
				requests = append(requests, append([]byte{}, body...))
				if dream {
					dreams++
				} else {
					roots++
				}
				index := roots
				dreamIndex := dreams
				mu.Unlock()
				var request struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				}
				if json.Unmarshal(body, &request) != nil || len(request.Tools) < 19 {
					t.Error("public Dream lost full default catalog")
				}
				if !dream {
					label := "PUBLIC_PARENT_READY"
					if index > 1 {
						label = "PUBLIC_PARENT_AFTER"
					}
					emitPublicDreamReply(w, fmt.Sprint("parent-", index), label, "")
					return
				}
				if dreamIndex == 1 {
					if err := os.WriteFile(filepath.Join(home, "dream-http-opened"), []byte("actual model request body received"), 0600); err != nil {
						t.Error(err)
					}
					for {
						if _, err := os.Stat(filepath.Join(home, "release-dream")); err == nil {
							break
						}
						select {
						case <-r.Context().Done():
							cancelled <- struct{}{}
							return
						case <-ctx.Done():
							return
						case <-time.After(5 * time.Millisecond):
						}
					}
					emitPublicDreamReply(w, "dream-write", "", file)
					return
				}
				if !bytes.Contains(body, []byte("public-dream-write")) || bytes.Contains(body, []byte(`"is_error":true`)) {
					t.Error("actual Write result missing/error in next Dream request")
				}
				emitPublicDreamReply(w, "dream-done", "PUBLIC_DREAM_CONSOLIDATED", "")
			}))
			defer peer.Close()
			python := exec.CommandContext(ctx, "/usr/bin/python3", "testdata/dream-dialog.py", binary, home, profile)
			python.Env = append(resumeErrorEnv(home, root, peer.URL), "TERM=xterm-256color", "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE="+memory, "CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL=1", "CLAUDE_CODE_TMPDIR="+filepath.Join(home, "tmp"))
			capturePublicGoCoverage(t, python)
			out, err := python.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(out))
			}
			if !bytes.Contains(out, []byte(`"restored": true`)) {
				t.Fatal("public terminal restoration not proved", string(out))
			}
			mu.Lock()
			rootCount, dreamCount := roots, dreams
			all := append([][]byte{}, requests...)
			mu.Unlock()
			wantDreams := map[string]int{"complete": 2, "kill": 1, "disabled": 0}[profile]
			if rootCount != 2 || dreamCount != wantDreams {
				t.Fatal("public actual producer counts", rootCount, dreamCount)
			}
			if profile == "complete" {
				body, err := os.ReadFile(file)
				if err != nil || string(body) != "PUBLIC_DREAM_MEMORY_PHYSICAL" {
					t.Fatal("public physical memory Write", err, string(body))
				}
				lock, err := os.ReadFile(filepath.Join(memory, ".consolidate-lock"))
				if err != nil || len(lock) != 0 {
					t.Fatal("public consolidation lease not completed", err)
				}
			} else {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("killed/disabled Dream wrote memory", err)
				}
				if _, err := os.Stat(filepath.Join(memory, ".consolidate-lock")); !os.IsNotExist(err) {
					t.Fatal("kill/disabled lease not rolled back", err)
				}
			}
			if profile == "kill" {
				select {
				case <-cancelled:
				case <-ctx.Done():
					t.Fatal("actual task x did not cancel physical HTTP")
				}
			}
			for path, original := range originals {
				if body, err := os.ReadFile(path); err != nil || !bytes.Equal(body, original) {
					t.Fatal("Dream mutated prior session", path, err)
				}
			}
			for _, request := range all {
				if bytes.Contains(request, []byte("AFTER_DREAM")) && (bytes.Contains(request, []byte("PUBLIC_DREAM_CONSOLIDATED")) || bytes.Contains(request, []byte("# Dream: Memory Consolidation")) || bytes.Contains(request, []byte(`"subtype":"memory_saved"`))) {
					t.Fatal("background fork leaked into next parent model input")
				}
			}
			current, _ := filepath.Glob(filepath.Join(project, "*.jsonl"))
			for _, path := range current {
				if originals[path] != nil {
					continue
				}
				body, _ := os.ReadFile(path)
				if bytes.Contains(body, []byte("# Dream: Memory Consolidation")) || bytes.Contains(body, []byte("public-dream-write")) {
					t.Fatal("skipTranscript fork leaked into physical root transcript")
				}
				if profile == "complete" && !strings.Contains(string(body), `"subtype":"memory_saved"`) {
					t.Fatal("public TUI memory notification was not persisted")
				}
			}
		})
	}
}
