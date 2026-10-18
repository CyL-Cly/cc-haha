package mcpruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthenticationCacheActualFileWriteChainClearAndFreshBatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cache.json")
	if err := os.WriteFile(path, []byte(`{"legacy":{"timestamp":1791244800000,"future":"keep"},"auth":{"timestamp":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var writes, reads atomic.Int32
	cache, err := NewAuthenticationCache(AuthenticationCacheOptions{Path: path, ReadFile: func(path string) ([]byte, error) { reads.Add(1); return os.ReadFile(path) }, WriteFile: func(path string, data []byte) error {
		if writes.Add(1) == 1 {
			close(started)
			<-release
		}
		return os.WriteFile(path, data, 0600)
	}, MkdirAll: func(path string) error { return os.MkdirAll(path, 0700) }, Remove: os.Remove, NowMillis: func() float64 { return 1791244800000 }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = cache.Join(context.Background())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache.Mark("auth")
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual first file write never started")
	}
	target := "auth"
	cleared := cache.queueClear(&target)
	cache.Mark("other")
	select {
	case <-cleared:
		t.Fatal("targeted clear settled before older actual 401 write")
	default:
	}
	close(release)
	if err := cache.Join(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil || actual["auth"] != nil || actual["other"] == nil || string(actual["legacy"]) != `{"timestamp":1791244800000,"future":"keep"}` {
		t.Fatal("actual serialized writes lost unrelated failure or recreated cleared failure", string(data))
	}
	if err := cache.BeginBatch(ctx); err != nil {
		t.Fatal(err)
	}
	beforeReads := reads.Load()
	for _, name := range []string{"legacy", "other"} {
		if yes, err := cache.Cached(ctx, name); err != nil || !yes {
			t.Fatal("fresh batch did not read real latest file", name, yes, err)
		}
	}
	if reads.Load() != beforeReads+1 {
		t.Fatal("one batch did not share original read promise", reads.Load(), beforeReads)
	}
	if err := os.WriteFile(path, []byte(`{"other":{"timestamp":1791244800000}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if yes, err := cache.Cached(ctx, "legacy"); err != nil || !yes {
		t.Fatal("same batch unexpectedly lost its already-read memo", yes, err)
	}
	if err := cache.BeginBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if yes, err := cache.Cached(ctx, "legacy"); err != nil || yes {
		t.Fatal("new batch froze another process's stale failure", yes, err)
	}
	if err := cache.Clear(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("clear all did not await actual unlink", err)
	}
}
