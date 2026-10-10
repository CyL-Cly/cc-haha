package providerproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponsesRealHTTPDownstreamCancelClosesBodyAndSettlesOnceWithReentrantCallbacks(t *testing.T) {
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, responsesEvent("response.content_part.added", map[string]any{"part": map[string]any{"type": "output_text", "text": ""}}))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	response, err := http.Get(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	events := []string{}
	record := func(value string) { mu.Lock(); events = append(events, value); mu.Unlock() }
	var reader *ResponsesReader
	reader, err = NewResponsesReader(context.Background(), response.Body, "actual-model", ResponsesReaderOptions{ResponsesStreamOptions: ResponsesStreamOptions{OnCancel: func(reason any) {
		if reason != "actual-stop" {
			t.Error(reason)
		}
		record("cancel")
		_ = reader.Close()
	}, OnTerminal: func(string) { record("terminal") }, OnSettled: func() { record("settled"); _ = reader.Close() }}, CancelUpstream: func(reason any) error {
		if reason != "actual-stop" {
			t.Error(reason)
		}
		record("body-close")
		return response.Body.Close()
	}})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := reader.NextFrame()
	if err != nil || !strings.Contains(string(frame), "message_start") {
		t.Fatal(err, string(frame))
	}
	done := make(chan error, 1)
	go func() { done <- reader.CloseWithReason("actual-stop") }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant cancel callback deadlocked")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("actual upstream body survived downstream cancellation")
	}
	if _, err = reader.NextFrame(); err != io.EOF {
		t.Fatal(err)
	}
	_ = reader.CloseWithReason("second-stop")
	mu.Lock()
	actual := append([]string(nil), events...)
	mu.Unlock()
	if !reflect.DeepEqual(actual, []string{"cancel", "body-close", "settled"}) {
		t.Fatal(actual)
	}
}

func TestResponsesRealHTTPTerminalClosesInfiniteUpstreamAndPreservesEveryCompleteFrame(t *testing.T) {
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		input := responsesDone([]any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "physical terminal 你好"}}}})
		for _, b := range []byte(input) {
			_, _ = w.Write([]byte{b})
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	response, err := http.Get(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	reader, err := NewResponsesReader(context.Background(), response.Body, "actual-model", ResponsesReaderOptions{ResponsesStreamOptions: ResponsesStreamOptions{OnTerminal: func(event string) { events = append(events, "terminal:"+event) }, OnCancel: func(any) { events = append(events, "cancel") }, OnSettled: func() { events = append(events, "settled") }}, CancelUpstream: func(reason any) error {
		if reason != responsesTerminalCancelReason {
			t.Error(reason)
		}
		events = append(events, "body-close")
		return response.Body.Close()
	}})
	if err != nil {
		t.Fatal(err)
	}
	frames := []string{}
	for {
		frame, err := reader.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, string(frame))
	}
	_ = reader.Close()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal did not close physical upstream")
	}
	if len(frames) != 6 || !strings.Contains(frames[len(frames)-1], "message_stop") {
		t.Fatal(frames)
	}
	if !reflect.DeepEqual(events, []string{"terminal:response.completed", "body-close", "settled"}) {
		t.Fatal(events)
	}
}

func TestResponsesContextCancellationDrainsPhysicalPendingRead(t *testing.T) {
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	response, err := http.Get(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := NewResponsesReader(ctx, response.Body, "actual-model", ResponsesReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := reader.NextFrame(); finished <- err }()
	cancel()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel retained upstream")
	}
	select {
	case err = <-finished:
		if err != io.EOF {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel retained transformer read")
	}
}
