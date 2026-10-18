//go:build darwin

package pluginregistry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPluginProducerReceiptKeepsEnteredIOCancelWarmAndClearIdentity(t *testing.T) {
	f := newCacheFixture(t)
	f.options.Prepare = func(_ context.Context, _ protocol.JSONObject, _, _ string, _ protocol.JSONObject) (Prepared, error) {
		return Prepared{Path: filepath.Join(f.market, "plugins", "sample")}, nil
	}
	loader, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.home, ".claude", "plugins", "known_marketplaces.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	producer := loader.CachedProducer()
	loaded := make(chan error, 1)
	go func() { _, err := producer.Load(ctx); loaded <- err }()
	type opened struct {
		file *os.File
		err  error
	}
	connected := make(chan opened, 1)
	go func() { file, err := os.OpenFile(path, os.O_WRONLY, 0600); connected <- opened{file, err} }()
	var writer *os.File
	select {
	case value := <-connected:
		if value.err != nil {
			t.Fatal(value.err)
		}
		writer = value.file
	case <-ctx.Done():
		t.Fatal("actual plugin FIFO not entered", ctx.Err())
	}
	defer writer.Close()
	if _, _, ready := producer.Ready(); ready || loader.CachedProducer() != producer {
		t.Fatal("entered native load lost pending identity")
	}
	waiter, stop := context.WithCancel(ctx)
	stop()
	canceled := make(chan error, 1)
	go func() { _, err := loader.LoadCached(waiter); canceled <- err }()
	var waiterError error
	select {
	case waiterError = <-canceled:
	case <-ctx.Done():
		t.Fatal("second waiter cancellation waited for original physical producer", ctx.Err())
	}
	// Always release the real FIFO before asserting the cancellation result.
	_, writeError := writer.Write(original)
	closeError := writer.Close()
	if writeError != nil || closeError != nil {
		t.Fatal(writeError, closeError)
	}
	select {
	case err = <-loaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(waiterError, context.Canceled) {
		t.Fatal("single waiter changed original producer", waiterError)
	}
	if _, err, ready := producer.Ready(); !ready || err != nil || loader.CachedProducer() != producer {
		t.Fatal("ready actual cache receipt changed", err, ready)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	full := loader.FullProducer()
	if _, err = full.Load(ctx); err != nil {
		t.Fatal(err)
	}
	warm := loader.CachedProducer()
	if warm == producer || warm == full || loader.FullProducer() != full {
		t.Fatal("full load must install distinct cached-only resolved operation")
	}
	if value, err, ready := warm.Ready(); !ready || err != nil || len(value.Enabled) != 1 {
		t.Fatal("actual full result did not warm cached consumer", value, err, ready)
	}
	loader.Clear()
	if loader.CachedProducer() == warm || loader.FullProducer() == full {
		t.Fatal("explicit clear did not retire cache identities")
	}
	if _, err = producer.Load(ctx); err != nil {
		t.Fatal("clear changed an already admitted original operation", err)
	}
}
