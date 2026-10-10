package teamreader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTeamWatcherImmediateSinglePollCoalescesAndCloseJoinsAcceptedEvent(t *testing.T) {
	s := watcherReader(t)
	dir := filepath.Join(s.options.ConfigDir, "teams", "held")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"held","createdAt":1,"leadSessionId":"parent","leadAgentId":"team-lead@held","members":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var deliveries atomic.Int32
	w, e := NewWatcher(s, func(event protocol.JSONObject, lead string) {
		if deliveries.Add(1) == 1 {
			close(entered)
			<-release
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(parent)
	w.Start(parent)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("immediate original poll did not enter actual delivery")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if e = w.CheckNow(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("overlapping poll did not wait for shared scan", e)
	}
	stop()
	if deliveries.Load() != 1 {
		t.Fatal("duplicate accepted scan", deliveries.Load())
	}
	cancel()
	ctx, stop = context.WithTimeout(context.Background(), 30*time.Millisecond)
	if e = w.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("Close returned while actual delivery remained held", e)
	}
	stop()
	close(release)
	ctx, stop = context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if e = w.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if e = w.CheckNow(context.Background()); e == nil {
		t.Fatal("poll accepted after Close")
	}
	w.Start(context.Background())
	if deliveries.Load() != 1 {
		t.Fatal("closed interval was restarted")
	}
}
