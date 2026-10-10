package providerproxy

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatNativeReaderHoldsSuccessAndClosesOnLateUpstreamError(t *testing.T) {
	for _, tail := range []string{"data:{broken}\n\n", "data:{\"error\":\"upstream failed\"}\n\n"} {
		input := chatChunk(`{"content":"actual"}`, "", "") + chatChunk(`{}`, `"stop"`, "") + tail
		closed, settled := new(atomic.Int32), new(atomic.Int32)
		r, err := NewChatReader(context.Background(), io.NopCloser(strings.NewReader(input)), "fixture", ChatReaderOptions{CancelUpstream: func(reason any) error {
			if reason != nil {
				t.Error(reason)
			}
			closed.Add(1)
			return nil
		}, OnSettled: func() { settled.Add(1) }})
		if err != nil {
			t.Fatal(err)
		}
		result, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
		if !strings.Contains(string(result), "content_block_delta") || !strings.Contains(string(result), "event: error") || strings.Contains(string(result), "message_delta") || strings.Contains(string(result), "message_stop") {
			t.Fatalf("late failure committed a success %s", result)
		}
		if closed.Load() != 1 || settled.Load() != 1 {
			t.Fatal(closed.Load(), settled.Load())
		}
	}
}
func TestChatNativeReaderContextCancelDrainsBlockedTransportWithoutTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read, write := io.Pipe()
	defer write.Close()
	closed, settled := new(atomic.Int32), new(atomic.Int32)
	var cancelReason any
	settleDone := make(chan struct{})
	r, err := NewChatReader(ctx, read, "fixture", ChatReaderOptions{CancelUpstream: func(reason any) error { cancelReason = reason; closed.Add(1); return read.Close() }, OnSettled: func() { settled.Add(1); close(settleDone) }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.NextFrame(); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation left a blocked transport read")
	}
	select {
	case <-settleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled transport did not settle")
	}
	reason, ok := cancelReason.(error)
	if !ok || !errors.Is(reason, context.Canceled) || closed.Load() != 1 || settled.Load() != 1 {
		t.Fatal(cancelReason, closed.Load(), settled.Load())
	}
	_ = r.Close()
}
