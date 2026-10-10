package clibootstrap

import (
	"context"
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

func TestActualSharedHarnessImageCancellationClosesPhysicalBodyAndNextTurnRuns(t *testing.T) {
	opened, closed := make(chan struct{}), make(chan struct{})
	var models atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/images/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[`)
			w.(http.Flusher).Flush()
			close(opened)
			<-r.Context().Done()
			close(closed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"cancel-actual-image","name":"ImageGen","input":{"prompt":"cancel physical image generation"}}`)}))
		} else {
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"IMAGE_CANCEL_NEXT_TURN_OK"}`)}))
		}
	}))
	defer peer.Close()
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func([]byte) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"start the cancellable real image request"}}`))
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("actual image tool never opened physical response")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("actual image stream cancellation did not close socket")
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled image Harness outcome", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual image tool producer did not settle")
	}
	root := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "generated-images", m.Controller.SessionID())
	if entries, err := os.ReadDir(root); len(entries) != 0 || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled upstream persisted an image", entries, err)
	}
	next, nextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer nextCancel()
	if err := m.RunTurn(next, webObject(`{"type":"user","message":{"role":"user","content":"continue after cancelling the image"}}`)); err != nil {
		t.Fatal("next turn reused stale image owner", err)
	}
	if err := m.ShutdownSession(next); err != nil {
		t.Fatal("image session close did not join actual lifetimes", err)
	}
	if models.Load() != 2 {
		t.Fatal("real model loop was replaced or repeated", models.Load())
	}
}

func TestTwoActualImageToolsOverlapOnOneHarnessOwnerAndFinishSeparateFiles(t *testing.T) {
	var models, images atomic.Int32
	both := make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/images/") {
			if images.Add(1) == 2 {
				close(both)
			}
			select {
			case <-both:
			case <-time.After(3 * time.Second):
				t.Error("Source concurrency-safe image tools were serialized")
			}
			_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":"iVBORw0KGgoAAAAA"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"parallel-image-one","name":"ImageGen","input":{"prompt":"first distinct prompt"}}`), webObject(`{"type":"tool_use","id":"parallel-image-two","name":"ImageGen","input":{"prompt":"second distinct prompt"}}`)}))
		} else {
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"TWO_ACTUAL_IMAGE_FILES_OK"}`)}))
		}
	}))
	defer peer.Close()
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func([]byte) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"generate two distinct images concurrently"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	if models.Load() != 2 || images.Load() != 2 {
		t.Fatal("actual image producer cardinality", models.Load(), images.Load())
	}
	root := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", "generated-images", m.Controller.SessionID())
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatal("actual concurrent image files", len(entries), err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("actual image file permissions", info, err)
		}
	}
}
