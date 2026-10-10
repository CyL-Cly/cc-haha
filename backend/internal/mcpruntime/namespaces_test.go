package mcpruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestActualNamespacesReuseConnectionsAndRootOwnsDrain(t *testing.T) {
	var mode, initializes, lists atomic.Int32
	peer := loopback(t, &mode, &initializes, &lists, nil, nil)
	root := manager(t, nil)
	candidate := manager(t, nil)
	view, e := root.ShareNamespace(candidate)
	if e != nil || view != candidate {
		t.Fatal(e)
	}
	duplicate := manager(t, nil)
	duplicate.options.CWD = candidate.options.CWD
	same, e := root.ShareNamespace(duplicate)
	if e != nil || same != view {
		t.Fatal("same cwd did not reuse actual namespace", e)
	}
	config := parse(t, `{"type":"http","url":"`+peer.URL+`"}`)
	parent, e := root.Connect(context.Background(), "shared", config)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	peers := make([]Connection, 2)
	for i := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var e error
			peers[i], e = view.Connect(context.Background(), "shared", config)
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if peers[0].Client != peers[1].Client || peers[0].Client == parent.Client || initializes.Load() != 2 || lists.Load() != 2 {
		t.Fatal("actual cwd/name/config namespace identity lost", initializes.Load(), lists.Load())
	}
	inherited, e := view.RefreshConnection(context.Background(), parent)
	if e != nil || inherited.Client != parent.Client || initializes.Load() != 2 {
		t.Fatal("inherited parent reconnected under child cwd", e)
	}
	if e = view.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	retained, e := same.Connect(context.Background(), "shared", config)
	if e != nil || retained.Client != peers[0].Client {
		t.Fatal("worker prematurely closed named peer", e)
	}
	if e = view.CloseConnection(context.Background(), peers[0]); e != nil {
		t.Fatal(e)
	}
	replacement, e := view.Connect(context.Background(), "shared", config)
	if e != nil || replacement.Client == peers[0].Client {
		t.Fatal(e)
	}
	if e = view.CloseConnection(context.Background(), peers[0]); e != nil {
		t.Fatal(e)
	}
	current, e := view.RefreshConnection(context.Background(), replacement)
	if e != nil || current.Client != replacement.Client {
		t.Fatal("old inline cleanup retired replacement", e)
	}
	foreign := manager(t, nil)
	if _, e = foreign.RefreshConnection(context.Background(), parent); e == nil {
		t.Fatal("foreign process graph accepted actual peer")
	}
	if e = root.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, runtime := range []*Service{root, view} {
		if _, e = runtime.Connect(context.Background(), "shared", config); e == nil {
			t.Fatal("root close did not drain child namespace")
		}
	}
}
