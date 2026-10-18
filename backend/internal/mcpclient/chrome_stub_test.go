package mcpclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFrozenChromeStubHasNoToolsAndTimesOutWithoutSpawningCommand(t *testing.T) {
	options := options(t.TempDir())
	options.NativeChromeStub, options.Name, options.ConnectTimeout = true, "claude-in-chrome", 5*time.Millisecond
	client, err := Connect(context.Background(), Config{Type: "stdio", Command: "/must/not/execute/private/legacy/runtime"}, options)
	var transport *TransportError
	if client != nil || !errors.As(err, &transport) || !strings.Contains(err.Error(), "connection timed out after 5ms") {
		t.Fatal("frozen in-process stub fabricated a working Chrome server", client, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Connect(ctx, Config{Type: "stdio", Command: "/must/not/execute"}, options)
	if err == nil {
		t.Fatal("stub handshake ignored cancellation")
	}
}
