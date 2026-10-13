package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Exercise the ordinary stdin parser, admission callback and model consumer.
// A control response is the barrier proving the preceding queued input was
// admitted. Neither a duplicate now packet nor that ordinary input may abort
// the first physical HTTP stream; only the novel now packet may do so.
func TestActualSDKStdinPriorityAdmissionPreservesQueueAndCheckpointBeforeEOF(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-sdk-priority"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	path := filepath.Join(f.cwd, "actual-stdin-now-write.txt")
	const marker = "ACTUAL_STDIN_PRIORITY_WRITE_ONCE"
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"stdin-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture-model\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ACTUAL_STDIN_PRIORITY_PARTIAL\"}}\n\n")
			w.(http.Flusher).Flush()
			close(started)
			select {
			case <-r.Context().Done():
				close(cancelled)
			case <-release:
			}
		case 2:
			// print.ts drains consecutive prompt commands into one ASK. Their
			// priority order must survive that actual batch, not create a turn
			// for each packet or silently discard the ordinary queued input.
			if !bytes.Contains(body, []byte(`ACTUAL_STDIN_NOW\nACTUAL_STDIN_QUEUED`)) {
				t.Error("Source ordered prompt batch was lost", string(body))
			}
			emitNativeTaskTool(w, "stdin-now-write", "Write", map[string]any{"file_path": path, "content": marker})
		case 3:
			if !bytes.Contains(body, []byte("stdin-now-write")) || !bytes.Contains(body, []byte("ACTUAL_STDIN_PRIORITY_PARTIAL")) {
				t.Error("actual Write or partial stream was lost", string(body))
			}
			emitPlanFixture(w, 3, "", "ACTUAL_STDIN_BATCH_DONE")
		default:
			t.Error("duplicate stdin message repeated a physical side effect")
			http.Error(w, "extra request", http.StatusBadRequest)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	frames := make(chan protocol.JSONObject, 128)
	var output, diagnostics bytes.Buffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", AllowedTools: []string{"Write"}, Replay: true, MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &output, Error: &diagnostics, OnOutput: func(data []byte) error {
		output.Write(data)
		var frame protocol.JSONObject
		if frame.UnmarshalJSON(data) == nil {
			frames <- frame
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	finished, joined := make(chan error, 1), make(chan struct{})
	go func() { defer close(joined); _, err := m.Run(ctx); finished <- err }()
	defer func() {
		cancel()
		_ = writer.Close()
		_ = input.Close()
		select {
		case <-release:
		default:
			close(release)
		}
		select {
		case <-joined:
		case <-time.After(30 * time.Second):
			t.Error("stdin runtime did not join")
		}
	}()
	write := func(value any) {
		t.Helper()
		if _, err := writer.Write(append(rawMain(value), '\n')); err != nil {
			t.Fatal("actual stdin write", err)
		}
	}
	await := func(predicate func(protocol.JSONObject) bool) {
		t.Helper()
		for {
			select {
			case frame := <-frames:
				if predicate(frame) {
					return
				}
			case err := <-finished:
				t.Fatal("runtime ended before expected frame", err, diagnostics.String(), output.String())
			case <-ctx.Done():
				t.Fatal("actual stdin consumer did not advance", ctx.Err())
			}
		}
	}
	control := func(id, subtype string) {
		write(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": subtype}})
		await(func(frame protocol.JSONObject) bool {
			response, _ := frame.Object("response")
			if frame.String("type") == "control_response" && response.String("request_id") == id {
				if response.String("subtype") != "success" {
					t.Fatal("control failed", string(rawMain(frame)))
				}
				return true
			}
			return false
		})
	}
	control("stdin-initialize", "initialize")
	first := map[string]any{"type": "user", "uuid": "8c49badb-1fad-4207-a2c5-f9facb3b3425", "message": map[string]any{"role": "user", "content": "ACTUAL_STDIN_FIRST"}}
	write(first)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("real stream did not start", ctx.Err())
	}
	first["priority"] = "now"
	write(first)
	await(func(frame protocol.JSONObject) bool {
		return frame.String("type") == "user" && string(frame.Get("isReplay")) == "true" && frame.String("uuid") == first["uuid"]
	})
	write(map[string]any{"type": "user", "uuid": "8971c064-edc4-4a0c-84a8-eebebf49a651", "message": map[string]any{"role": "user", "content": "ACTUAL_STDIN_QUEUED"}})
	control("queued-admission-barrier", "get_settings")
	select {
	case <-cancelled:
		t.Fatal("duplicate now or default queued input cancelled the active stream")
	default:
	}
	write(map[string]any{"type": "user", "uuid": "fe1c5dcf-5d4c-4f20-b099-6caf7d710b20", "priority": "now", "message": map[string]any{"role": "user", "content": "ACTUAL_STDIN_NOW"}})
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("novel now failed to cancel actual active HTTP", ctx.Err())
	}
	await(func(frame protocol.JSONObject) bool {
		return frame.String("type") == "result" && bytes.Contains(rawMain(frame), []byte("ACTUAL_STDIN_BATCH_DONE"))
	})
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err, diagnostics.String())
		}
	case <-ctx.Done():
		t.Fatal("EOF did not drain actual queued work", ctx.Err())
	}
	if requests.Load() != 3 {
		t.Fatal("unexpected physical request cardinality", requests.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != marker {
		t.Fatal("physical admitted Write absent", string(data), err)
	}
	paths, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", m.Controller.SessionID()+".jsonl"))
	if len(paths) != 1 {
		t.Fatal("canonical transcript missing", paths)
	}
	history, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Contains(history, []byte("file-history-snapshot")) || bytes.Count(history, []byte(`"tool_use_id":"stdin-now-write"`)) != 1 {
		t.Fatal("checkpoint producer or once-only tool record missing", err, string(history))
	}
	if bytes.Contains(history, []byte("[Request interrupted by user]")) {
		t.Fatal("priority interrupt became ordinary Stop context", string(history))
	}
	select {
	case <-m.Owner.Done():
	default:
		t.Fatal("EOF returned before actual owner close")
	}
}
