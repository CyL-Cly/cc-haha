package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestChildAgentInheritsCapturedTurnModelAfterNextModelSelection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		at := calls.Add(1)
		want := "OldTurnModel"
		if at == 4 {
			want = "NextTurnModel"
		}
		if got := obj(body).String("model"); got != want {
			t.Errorf("request %d model=%q want=%q", at, got, want)
		}
		switch at {
		case 1:
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			response(w, "snapshot-parent", "Agent", map[string]any{"prompt": "Use the parent's current turn model.", "description": "captured model", "subagent_type": "general-purpose"}, "")
		case 2:
			response(w, "snapshot-child", "", nil, "CHILD FINISHED")
		case 3:
			response(w, "snapshot-parent-done", "", nil, "PARENT FINISHED")
		case 4:
			response(w, "snapshot-next-turn", "", nil, "NEXT TURN FINISHED")
		default:
			t.Error("extra model request")
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer provider.Close()
	defer releaseOnce.Do(func() { close(release) })
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Model = "OldTurnModel"
	o.Arguments.Tools = []string{"Read,Agent"}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Submit(ctx, tui.Input{Text: "Run a child Agent."}, nil) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		return s.process.Host.SetInferenceModel(scope, "NextTurnModel", nil, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = s.Submit(ctx, tui.Input{Text: "Use the newly selected model."}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("actual child/parent/next-turn requests=%d", calls.Load())
	}
}
