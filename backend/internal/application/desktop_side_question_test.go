package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSameHostSideQuestionKeepsParentCanonicalAndThinkingPrefix(t *testing.T) {
	var mu sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, e := io.ReadAll(r.Body)
		if e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		requests = append(requests, append([]byte(nil), data...))
		number := len(requests)
		mu.Unlock()
		if bytes.Contains(data, []byte("This is a side question from the user")) {
			response(w, "side-answer", "", nil, " SIDE ANSWER ")
			return
		}
		response(w, "parent-answer", "", nil, "PARENT ANSWER")
		if number > 3 {
			t.Error("unexpected additional model call", number)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	delete(o.Environment, "CLAUDE_CODE_SIMPLE")
	o.Arguments.Tools = nil
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, e := NewSession(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	if e = s.Submit(ctx, tui.Input{TurnID: "parent-first", Text: "parent question"}, nil); e != nil {
		t.Fatal(e)
	}
	history := func() ([]protocol.JSONObject, error) {
		var rows []protocol.JSONObject
		receipt, e := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error { rows = s.process.Controller.History(); return nil })
		if e == nil {
			e = receipt.Wait()
		}
		return rows, e
	}
	before, e := history()
	if e != nil {
		t.Fatal(e)
	}
	question := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("11111111-1111-4111-8111-111111111111")}, protocol.JSONField{Name: "question", Value: raw("side question")}, protocol.JSONField{Name: "history", Value: raw([]any{})})
	answer, e := s.process.SideQuestionControl(ctx, question)
	if e != nil {
		t.Fatal(e)
	}
	if answer.String("response") != "SIDE ANSWER" {
		t.Fatal("side response", answer)
	}
	after, e := history()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(raw(before), raw(after)) {
		t.Fatal("side question changed parent canonical")
	}
	if e = s.Submit(ctx, tui.Input{TurnID: "parent-next", Text: "parent continues"}, nil); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatal("physical call count", len(requests))
	}
	if !bytes.Contains(requests[1], []byte("PARENT ANSWER")) || bytes.Contains(requests[2], []byte("SIDE ANSWER")) {
		t.Fatal("cache-safe inherited context or ephemeral isolation failed")
	}
}

// Both model sockets are real. Completing the main socket must complete its
// actual ASK while the side socket remains pending, in either admission order.
func TestActualSameHostSideQuestionDoesNotHoldMainCompletion(t *testing.T) {
	for _, order := range []string{"main-first", "side-first"} {
		t.Run(order, func(t *testing.T) {
			mainEntered, sideEntered := make(chan struct{}), make(chan struct{})
			releaseMain, releaseSide := make(chan struct{}), make(chan struct{})
			var sideOnce, mainOnce sync.Once
			defer sideOnce.Do(func() { close(releaseSide) })
			defer mainOnce.Do(func() { close(releaseMain) })
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				if bytes.Contains(data, []byte("This is a side question from the user")) {
					close(sideEntered)
					select {
					case <-releaseSide:
					case <-r.Context().Done():
						return
					}
					response(w, "side", "", nil, "independent side")
					return
				}
				if bytes.Contains(data, []byte("held main")) {
					close(mainEntered)
					select {
					case <-releaseMain:
					case <-r.Context().Done():
						return
					}
				}
				response(w, "main", "", nil, "main answer")
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			delete(o.Environment, "CLAUDE_CODE_SIMPLE")
			o.Arguments.Tools = nil
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			s, e := NewSession(ctx, o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			if e = s.Submit(ctx, tui.Input{TurnID: "seed", Text: "seed"}, nil); e != nil {
				t.Fatal(e)
			}
			mainDone, sideDone := make(chan error, 1), make(chan error, 1)
			startMain := func() {
				go func() { mainDone <- s.Submit(ctx, tui.Input{TurnID: "main", Text: "held main"}, nil) }()
				select {
				case <-mainEntered:
				case e := <-mainDone:
					t.Fatal("main start", e)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			startSide := func() {
				go func() {
					_, e := s.process.SideQuestionControl(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("independent")}, protocol.JSONField{Name: "question", Value: raw("side question")}, protocol.JSONField{Name: "history", Value: raw([]any{})}))
					sideDone <- e
				}()
				select {
				case <-sideEntered:
				case e := <-sideDone:
					t.Fatal("side start", e)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if order == "main-first" {
				startMain()
				startSide()
			} else {
				startSide()
				startMain()
			}
			mainOnce.Do(func() { close(releaseMain) })
			select {
			case e := <-mainDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(2 * time.Second):
				t.Error("completed main ASK is held behind the unrelated side model socket")
			}
			sideOnce.Do(func() { close(releaseSide) })
			select {
			case e := <-sideDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
