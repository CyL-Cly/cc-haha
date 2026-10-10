package whatsappcandidate

import (
	"context"
	"go.mau.fi/libsignal/session"
	"testing"
	"time"
)

func TestActualClientCachedSessionGateCoversCommitAndCanceledSibling(t *testing.T) {
	device, n, e := OpenNodeDevice(t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx := session.WithNodeSessionTransactions(context.Background(), n.AcquireSessions)
	_, cached, e := device.WithCachedSessions(ctx, []string{})
	if e != nil || cached != ctx {
		t.Fatal("empty prefetch changed context", e)
	}
	_, release, e := n.AcquireSessions(ctx, []string{"111:0"})
	if e != nil {
		t.Fatal(e)
	}
	blocked, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, _, e := device.WithCachedSessions(blocked, []string{"111:0"}); result <- e }()
	cancel()
	select {
	case e := <-result:
		if e != context.Canceled {
			t.Fatal("canceled sibling", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked cache acquisition did not cancel")
	}
	release()
	_, cached, e = device.WithCachedSessions(ctx, []string{"111:0"})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		_, release, e := n.AcquireSessions(ctx, []string{"111:0"})
		if e != nil {
			t.Error(e)
			return
		}
		release()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("same-address escaped before cache commit")
	default:
	}
	if e = device.PutCachedSessions(cached); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("committed session gate not released")
	}
	if _, release, e = n.AcquireSessions(ctx, []string{"111:0"}); e != nil {
		t.Fatal(e)
	} else {
		release()
	}
}
