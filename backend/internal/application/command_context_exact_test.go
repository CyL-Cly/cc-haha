package application

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestPublicCLIContextCommandUsesActualCounterThenSameCanonicalHarness(t *testing.T) {
	var counts, inferences atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/messages/count_tokens" {
			counts.Add(1)
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"input_tokens":37}`)
			return
		}
		inferences.Add(1)
		if !bytes.Contains(body, []byte("## Context Usage")) || !bytes.Contains(body, []byte("<local-command-stdout>")) {
			t.Error("next actual ASK lost canonical context-command output")
		}
		response(w, "actual-context-next", "", nil, "CONTEXT_SAME_HARNESS_NEXT_E83")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	command, _, err := ParseCommand([]string{"--print", "--model", "fixture-model", "--system-prompt", "Actual private context command", "--tools", "", "--thinking", "disabled", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	out := &usageControlOutput{frames: make(chan json.RawMessage, 256)}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		code, e := RunCommand(ctx, command, o, reader, out, nil)
		if e == nil && code != 0 {
			e = io.ErrUnexpectedEOF
		}
		done <- e
	}()
	send := func(text string) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": text}})
		if _, err := writer.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	waitResult := func() {
		t.Helper()
		for {
			select {
			case frame := <-out.frames:
				if obj(frame).String("type") == "result" {
					return
				}
			case err := <-done:
				t.Fatal("CLI stopped before result", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	send("/context")
	waitResult()
	if counts.Load() == 0 || inferences.Load() != 0 {
		t.Fatal("context command did not use count-only native transport", counts.Load(), inferences.Load())
	}
	out.mu.Lock()
	local := out.buffer.String()
	out.mu.Unlock()
	if !strings.Contains(local, "## Context Usage") || !strings.Contains(local, "Estimated usage by category") {
		t.Fatal("actual command output absent", local)
	}
	send("Continue after context inspection.")
	waitResult()
	if inferences.Load() != 1 {
		t.Fatal("context command created an additional ASK", inferences.Load())
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("CLI did not join EOF", ctx.Err())
	}
	files, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal("physical canonical transcript", files)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(disk, []byte("<command-name>/context")) || !bytes.Contains(disk, []byte("CONTEXT_SAME_HARNESS_NEXT_E83")) || bytes.Count(disk, []byte(`"type":"assistant"`)) != 1 {
		t.Fatal("context display changed canonical assistant history", string(disk))
	}
}
