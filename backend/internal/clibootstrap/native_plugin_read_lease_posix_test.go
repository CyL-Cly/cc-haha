//go:build darwin

package clibootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestActualPluginReadLeaseAllowsCanceledWaiterAndJoinsCloseBeforeArchiveCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	p, err := NewNativeProcessSettings(f.n, "darwin", DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	if _, err = p.InitialSettings(); err != nil {
		t.Fatal(err)
	}
	archive, err := p.plugins.archive.SessionPath(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.config, "plugins", "known_marketplaces.json")
	known, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	p.plugins.loader.Clear()
	producer := p.plugins.loader.CachedProducer()
	loaded := make(chan error, 1)
	go func() { _, err := p.LoadPlugins(ctx, "", false); loaded <- err }()
	writer := queryCatalogFIFOConnectedWriter(t, ctx, path)
	defer writer.Close()
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if err = queryCatalogRestorePrivateFile(path, known); err != nil {
			t.Error(err)
		}
		if _, err = writer.Write(known); err != nil {
			t.Error(err)
		}
		_ = writer.Close()
	}
	defer release()
	shared := func() error {
		result := make(chan error, 1)
		go func() { _, failure := p.SharedGlobalConfig(); result <- failure }()
		select {
		case failure := <-result:
			return failure
		case <-ctx.Done():
			release()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			select {
			case <-result:
			case <-deadline.C:
				t.Error("shared settings probe did not join after physical release")
			}
			t.Fatal("held plugin read retained settings admission", ctx.Err())
			return ctx.Err()
		}
	}
	if err = shared(); err != nil {
		t.Fatal("held plugin read retained the settings mutex", err)
	}
	waiter, stop := context.WithCancel(ctx)
	stop()
	waited := make(chan error, 1)
	go func() { _, err := p.LoadPlugins(waiter, "", false); waited <- err }()
	select {
	case err = <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("single canceled waiter", err)
		}
	case <-ctx.Done():
		t.Fatal("canceled waiter blocked behind original IO", ctx.Err())
	}
	if _, _, ready := producer.Ready(); ready || p.plugins.loader.CachedProducer() != producer {
		t.Fatal("single waiter replaced/settled original physical producer")
	}
	closed := make(chan error, 1)
	go func() { closed <- p.Close(ctx) }()
	// Observe the existing API's actual closed rejection, never merely that
	// a goroutine was launched. No OS/Promise result is fabricated by the probe.
	for {
		if err = shared(); err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			t.Fatal("Close did not close admission", err)
		}
	}
	if _, err = os.Stat(archive); err != nil {
		t.Fatal("archive cleaned before admitted physical read completed", err)
	}
	select {
	case err = <-closed:
		t.Fatal("Close returned before real FIFO release", err)
	default:
	}
	release()
	select {
	case err = <-loaded:
		if err != nil {
			t.Fatal("admitted original plugin read", err)
		}
	case <-ctx.Done():
		t.Fatal("original read did not join", ctx.Err())
	}
	select {
	case err = <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("settings Close did not join", ctx.Err())
	}
	if _, err = os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("completed Close retained actual archive session", err)
	}
	if _, err = p.LoadPlugins(ctx, "", false); err == nil {
		t.Fatal("closed settings admitted new plugin IO")
	}
}

func TestNativePluginProducerLeaseRejectsForeignAndClosedSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	other := newNativePluginFixture(t)
	for _, fixture := range []nativePluginFixture{f, other} {
		fixture.write(t, filepath.Join(fixture.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	}
	p, err := NewNativeProcessSettings(f.n, "darwin", DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	op, err := NewNativeProcessSettings(other.n, "darwin", DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	defer op.Close(context.Background())
	if _, err = p.InitialSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err = op.InitialSettings(); err != nil {
		t.Fatal(err)
	}
	foreign := op.plugins.loader.CachedProducer()
	if _, err = p.loadNativePluginProducer(ctx, foreign); err == nil {
		t.Fatal("foreign producer borrowed actual archive read lease")
	}
	if _, err = p.loadNativePluginProducer(ctx, nil); err == nil {
		t.Fatal("nil producer borrowed actual archive read lease")
	}
	if err = p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = p.loadNativePluginProducer(ctx, p.plugins.loader.CachedProducer()); err == nil {
		t.Fatal("closed settings admitted retained producer")
	}
}
