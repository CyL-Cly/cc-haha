package mcpruntime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestActualPeerCloseClearsConnectionAndToolDiscoveryCaches(t *testing.T) {
	var mode, initializes, lists atomic.Int32
	peer := loopback(t, &mode, &initializes, &lists, nil, nil)
	s := manager(t, nil)
	config := parse(t, fmt.Sprintf(`{"type":"http","url":%q}`, peer.URL))
	first, err := s.Connect(context.Background(), "owned", config)
	if err != nil || first.Status != "connected" || len(first.Tools) != 1 {
		t.Fatal(first, err)
	}
	if err = first.Client.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := s.Connect(context.Background(), "owned", config)
	if err != nil || second.Status != "connected" || second.Client == first.Client || len(second.Tools) != 1 {
		t.Fatal("closed client was returned as ready", second, err)
	}
	if initializes.Load() != 2 || lists.Load() != 2 {
		t.Fatal("closed peer reused stale connection/tool metadata", initializes.Load(), lists.Load())
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Connect(context.Background(), "owned", config); !errors.Is(err, context.Canceled) {
		t.Fatal("closed lifetime accepted a new discovery", err)
	}
}
