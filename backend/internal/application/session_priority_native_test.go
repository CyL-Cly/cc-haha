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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSessionSDKNowAdmissionDeduplicatesBeforeInterruptAndColdCheckpointResume(t *testing.T) {
	firstStarted, firstCancelled, releaseFirst := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	var path string
	const marker = "ACTUAL_PRIORITY_SDK_WRITE_ONCE"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"priority-first\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture-model\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ACTUAL_PRIORITY_PARTIAL\"}}\n\n")
			w.(http.Flusher).Flush()
			close(firstStarted)
			select {
			case <-r.Context().Done():
				close(firstCancelled)
			case <-releaseFirst:
			}
		case 2:
			if !bytes.Contains(body, []byte("NOW_PRIVATE_WRITE")) {
				t.Error("now input did not become next actual ASK", string(body))
			}
			response(w, "priority-write", "Write", map[string]any{"file_path": path, "content": marker}, "")
		case 3:
			if !bytes.Contains(body, []byte("call-priority-write")) || !bytes.Contains(body, []byte("ACTUAL_PRIORITY_PARTIAL")) {
				t.Error("actual Write or admitted partial history missing", string(body))
			}
			response(w, "priority-final", "", nil, "ACTUAL_PRIORITY_NOW_DONE")
		case 4:
			response(w, "priority-cold-read", "Read", map[string]any{"file_path": path}, "")
		case 5:
			if !bytes.Contains(body, []byte(marker)) || !bytes.Contains(body, []byte("ACTUAL_PRIORITY_NOW_DONE")) {
				t.Error("cold SDK resume lost physical Write/history", string(body))
			}
			response(w, "priority-cold-final", "", nil, "ACTUAL_PRIORITY_COLD_DONE")
		default:
			t.Error("duplicate caused an extra physical ASK")
			http.Error(w, "extra request", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = nil
	o.Arguments.Replay = true
	o.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	o.Native.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	path = filepath.Join(o.WorkDir, "priority-write.txt")
	var replay atomic.Int32
	o.OnSDK = func(data json.RawMessage) error {
		row := obj(data)
		if row.String("type") == "user" && string(row.Get("isReplay")) == "true" {
			replay.Add(1)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
		_ = s.Close(context.Background())
	})
	input := func(id, text, priority string) protocol.JSONObject {
		row := obj(raw(map[string]any{"type": "user", "uuid": id, "message": map[string]any{"role": "user", "content": text}}))
		if priority != "" {
			row.Set("priority", raw(priority))
		}
		return row
	}
	first := input("667cd5ee-8a6b-4bf2-b92d-3612589ef4d1", "FIRST_PRIVATE_STREAM", "")
	done := make(chan error, 1)
	go func() { done <- s.SubmitSDK(ctx, first) }()
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	duplicate := first.Clone()
	duplicate.Set("priority", raw("now"))
	if err = s.SubmitSDK(ctx, duplicate); err != nil {
		t.Fatal("duplicate now admission", err)
	}
	if replay.Load() != 1 {
		t.Fatal("real duplicate replay ACK absent", replay.Load())
	}
	select {
	case <-firstCancelled:
		t.Fatal("duplicate now interrupted admitted work")
	default:
	}
	if err = s.SubmitSDK(ctx, input("8a89050d-66ce-4163-a63a-7826ddf202ac", "NOW_PRIVATE_WRITE", "now")); err != nil {
		t.Fatal("novel now", err)
	}
	select {
	case <-firstCancelled:
	case <-ctx.Done():
		t.Fatal("now did not cancel physical stream", ctx.Err())
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal("first ASK did not close normally", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if requests.Load() != 3 {
		t.Fatal("unexpected physical request count", requests.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != marker {
		t.Fatal("real Write missing", string(data), err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	transcripts, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", o.Arguments.SessionID+".jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatal("canonical transcript absent", transcripts, err)
	}
	history, err := os.ReadFile(transcripts[0])
	if err != nil || !bytes.Contains(history, []byte("file-history-snapshot")) {
		t.Fatal("real enabled SDK checkpoint absent", err, string(history))
	}
	if bytes.Contains(history, []byte("[Request interrupted by user]")) {
		t.Fatal("submit interrupt persisted Stop-only interruption", string(history))
	}
	o.Arguments.Resume, o.Arguments.SessionID = o.Arguments.SessionID, ""
	resumed, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal("cold native resume", err)
	}
	defer resumed.Close(context.Background())
	if err = resumed.SubmitSDK(ctx, input("b92f6842-c922-4ac2-9bf3-0cc208787783", "Read previous real file and continue", "")); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 {
		t.Fatal("cold resume did not perform actual Read", requests.Load())
	}
	if err = resumed.Close(ctx); err != nil {
		t.Fatal(err)
	}
	finalHistory, err := os.ReadFile(transcripts[0])
	if err != nil || strings.Count(string(finalHistory), `"tool_use_id":"call-priority-write"`) != 1 {
		t.Fatal("duplicate replay executed Write twice or failed persistence", err, string(finalHistory))
	}
}
