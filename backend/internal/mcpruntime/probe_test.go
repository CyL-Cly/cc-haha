package mcpruntime

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestStatusProbeSkipsToolDiscoveryAndCannotCloseReplacement(t *testing.T) {
	var mode, initializations, lists atomic.Int32
	peer := loopback(t, &mode, &initializations, &lists, nil, nil)
	defer peer.Close()
	s := manager(t, nil)
	config := parse(t, `{"type":"http","url":"`+peer.URL+`","scope":"user"}`)
	old, err := s.ConnectTransport(context.Background(), "owned", config)
	if err != nil || old.Status != "connected" {
		t.Fatal(old.Status, err)
	}
	if lists.Load() != 0 {
		t.Fatal("status probe fetched model-facing tools")
	}
	if err = s.Disconnect(context.Background(), "owned", config); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Connect(context.Background(), "owned", config)
	if err != nil || fresh.Status != "connected" {
		t.Fatal(fresh.Status, err)
	}
	if err = s.CloseProbe(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	retained, err := s.Connect(context.Background(), "owned", config)
	if err != nil || retained.Client != fresh.Client || initializations.Load() != 2 || lists.Load() != 1 {
		t.Fatal("old probe retired fresh connection", err, initializations.Load(), lists.Load())
	}
	if err = s.CloseProbe(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConnectTransport(context.Background(), "owned", config); err != nil || initializations.Load() != 3 {
		t.Fatal("probe did not release its own attempt", err, initializations.Load())
	}
}
