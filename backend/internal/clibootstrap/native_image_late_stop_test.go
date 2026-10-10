package clibootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

func TestActualMainPrintImageLateStopKeepsCompletedFileAndClosesNextModelBody(t *testing.T) {
	ready, closed := make(chan struct{}), make(chan struct{})
	var models, images atomic.Int32
	var imagePath string
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		if strings.Contains(r.URL.Path, "/images/") {
			if r.Header.Get("Authorization") != "Bearer private-image-key" {
				t.Error("actual image credential changed")
			}
			images.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(nativeImagePNG))
			return
		}
		if r.Header.Get("X-Api-Key") != "private-image-harness-fake-key" {
			t.Error("actual model credential changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"late-stop-image","name":"ImageGen","input":{"prompt":"original combined image signal","count":1}}`)}))
			return
		}
		var request protocol.JSONObject
		if e = request.UnmarshalJSON(body); e != nil {
			t.Error(e)
			return
		}
		for _, message := range taskBlocks(request.Get("messages")) {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") != "tool_result" || block.String("tool_use_id") != "late-stop-image" {
					continue
				}
				var output protocol.JSONObject
				if e = output.UnmarshalJSON([]byte(block.String("content"))); e != nil {
					t.Error(e)
					return
				}
				result := taskBlocks(output.Get("images"))
				if len(result) != 1 {
					t.Error("actual image result missing", len(result))
					return
				}
				imagePath = result[0].String("path")
			}
		}
		if imagePath == "" {
			t.Error("actual next model POST lacks generated file")
			return
		}
		// The file/result are already complete; the real next model request is held.
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"held-next-model\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-flash\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func([]byte) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"generate image then stop next pending model request"}}`))
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("actual late stop phase not reached")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("actual next model socket did not close")
	}
	select {
	case e := <-done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual late stop did not settle")
	}
	bytes, e := os.ReadFile(imagePath)
	if e != nil || string(bytes) != string(nativeImagePNG) {
		t.Fatal("late stop changed completed file", e)
	}
	root := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "generated-images", m.Controller.SessionID())
	files, e := os.ReadDir(root)
	if e != nil || len(files) != 1 {
		t.Fatal("completed image count", len(files), e)
	}
	if images.Load() != 1 || models.Load() != 2 {
		t.Fatal("actual physical request counts", images.Load(), models.Load())
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}
