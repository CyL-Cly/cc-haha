package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalBuddyActualGlobalRawActionsCardCanonicalAndNextSharedHarness(t *testing.T) {
	var mu sync.Mutex
	requests := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		response(w, "buddy-model", "", nil, "BUDDY_ACTUAL_MODEL_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	seed := func() {
		t.Helper()
		receipt, err := s.process.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(scope *cooperative.Scope) error {
			return s.process.Global.Save(func(global *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := global.Clone()
				next.Set("companion", []byte(`{"name":"\ud800","personality":"Old soul\ud801","hatchedAt":0,"futureSoul":{"keep":true}}`))
				next.Set("futureBuddy", []byte(`{"keep":true}`))
				return &next, nil
			})
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	seed()
	var panel *tui.LocalPanel
	events := []tui.Event{}
	emit := func(event tui.Event) error {
		events = append(events, event)
		if event.LocalPanel != nil {
			copy := terminalPanelProjection(*event.LocalPanel)
			panel = &copy
		}
		return nil
	}
	invoke := func(args string) {
		t.Helper()
		handled, err := s.InvokeTerminalCommand(context.Background(), "buddy", args, emit)
		if !handled || err != nil {
			t.Fatalf("Actual Source /buddy consumer missing: handled=%v err=%v", handled, err)
		}
	}
	invoke("info")
	if panel == nil || !bytes.Contains(panel.Presentation, []byte(`\ud800`)) {
		t.Fatal("Captured raw companion card", panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "buddy-close"}, emit); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{"hatch", "pet", "mute", "unmute"} {
		invoke(args)
	}
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	if count != 0 {
		t.Fatal("Buddy local mount/effects used a model", count)
	}
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "BUDDY_FIRST_HUMAN"}, emit); err != nil {
		t.Fatal(err)
	}
	invoke("pet")
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "BUDDY_SECOND_HUMAN"}, emit); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	captured := append([]json.RawMessage(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatal(len(captured))
	}
	for i, body := range captured {
		if !bytes.Contains(body, []byte(`\ud800`)) || !bytes.Contains(body, []byte("# Companion")) || !bytes.Contains(body, []byte("You pet")) || !bytes.Contains(body, []byte("<command-name>/buddy")) {
			t.Fatalf("Same Harness retained raw effects and intro%d %s", i, body)
		}
		if bytes.Count(body, []byte("# Companion")) != 1 {
			t.Fatal("Intro repeated instead of raw-name history dedup", i, string(body))
		}
	}
	invoke("release")
	invoke("pet")
	invoke("hatch")
	invoke("info")
	if panel == nil || bytes.Contains(panel.Presentation, []byte(`\ud800`)) {
		t.Fatal("Actual release/hatch did not replace old card", panel)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "buddy-close"}, emit); err != nil {
		t.Fatal(err)
	}
	globalData, err := os.ReadFile(s.process.Global.Path())
	if err != nil || !bytes.Contains(globalData, []byte(`"futureBuddy"`)) {
		t.Fatal("Unknown global state lost", err, string(globalData))
	}
	var global protocol.JSONObject
	_ = global.UnmarshalJSON(globalData)
	stored, _ := global.Object("companion")
	if stored.String("name") == "" || len(stored.Get("appearanceSeed")) == 0 {
		t.Fatal("Real Source hatch soul missing", string(globalData))
	}
}
