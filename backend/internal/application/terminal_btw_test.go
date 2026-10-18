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

func TestTerminalBtwHeldMainEmptyUsageOwnedForkCancelReplaceAndNextHarness(t *testing.T) {
	for _, dismiss := range []string{"dismiss", "replacement"} {
		t.Run(dismiss, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			mainStarted, sideStarted, sideCancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseMain := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(releaseMain) })
			var mu sync.Mutex
			requests := [][]byte{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, body)
				n := len(requests)
				mu.Unlock()
				if bytes.Contains(body, []byte("This is a side question from the user")) {
					close(sideStarted)
					<-r.Context().Done()
					close(sideCancelled)
					return
				}
				if n == 1 {
					close(mainStarted)
					select {
					case <-releaseMain:
					case <-r.Context().Done():
						t.Error("BTW interrupted the parent HTTP")
						return
					}
				}
				response(w, "btw-parent", "", nil, "PARENT_ACTUAL_DONE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = true
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			events := make(chan tui.Event, 32)
			emit := func(e tui.Event) error {
				select {
				case events <- e:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			mainDone := make(chan error, 1)
			go func() { mainDone <- s.Submit(ctx, tui.Input{Mode: "prompt", Text: "HELD_PARENT_HUMAN"}, emit) }()
			select {
			case <-mainStarted:
			case <-ctx.Done():
				t.Fatal("Parent model did not start")
			}
			handled, err := s.InvokeTerminalCommand(ctx, "btw", " \t ", emit)
			if !handled || err != nil {
				t.Fatal("Actual busy Usage canonical admission", handled, err)
			}
			handled, err = s.InvokeTerminalCommand(ctx, "btw", "held side question", emit)
			if !handled || err != nil {
				t.Fatal(handled, err)
			}
			select {
			case <-sideStarted:
			case <-ctx.Done():
				t.Fatal("Actual independent side HTTP did not start")
			}
			var panel *tui.LocalPanel
			drain := true
			for drain {
				select {
				case event := <-events:
					if event.LocalPanel != nil {
						panel = event.LocalPanel
					}
				default:
					drain = false
				}
			}
			if panel == nil || panel.Kind != "btw" || !panel.KeepPromptInput {
				t.Fatal("Actual panel projection", panel)
			}
			release.Do(func() { close(releaseMain) })
			select {
			case err = <-mainDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("Parent ASK blocked on mounted side question")
			}
			s.mu.Lock()
			active := s.terminalPanel != nil && s.terminalPanel.props.ID == panel.ID
			s.mu.Unlock()
			if !active {
				t.Fatal("Parent completion unmounted independent BTW")
			}
			if dismiss == "dismiss" {
				err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "btw-close"}, emit)
			} else {
				_, err = s.InvokeTerminalCommand(ctx, "mobile", "", emit)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-sideCancelled:
			case <-ctx.Done():
				t.Fatal("Physical side HTTP was not cancelled/joined")
			}
			s.terminalBtw.mu.Lock()
			jobs := len(s.terminalBtw.jobs)
			s.terminalBtw.mu.Unlock()
			if jobs != 0 {
				t.Fatal("Unmount returned before app/native worker joined", jobs)
			}
			if dismiss == "replacement" {
				s.mu.Lock()
				p := s.terminalPanel
				s.mu.Unlock()
				if p == nil || p.props.Kind != "mobile" {
					t.Fatal("Replacement was reopened by old result")
				}
				if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: p.props.ID, Action: "mobile-close"}, emit); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Submit(ctx, tui.Input{Mode: "prompt", Text: "NEXT_PARENT_HUMAN"}, emit); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			captured := append([][]byte(nil), requests...)
			mu.Unlock()
			if len(captured) != 3 || !bytes.Contains(captured[1], []byte("HELD_PARENT_HUMAN")) || !bytes.Contains(captured[2], []byte("PARENT_ACTUAL_DONE")) || !bytes.Contains(captured[2], []byte("Usage: /btw")) || bytes.Contains(captured[2], []byte("held side question")) || bytes.Contains(captured[2], []byte("This is a side question")) {
				t.Fatalf("Same Harness prefix/canonical ephemeral isolation %s", raw(captured))
			}
			var global protocol.JSONObject
			receipt, e := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
				g, err := s.process.Global.Get()
				if err == nil {
					global = g.Clone()
				}
				return err
			})
			if e == nil {
				e = receipt.Wait()
			}
			if e != nil || string(global.Get("btwUseCount")) != "1" {
				t.Fatal("Actual mounted count", global, e)
			}
		})
	}
}

func TestTerminalBtwGlobalCloseCancelsJoinsActualSideAndMain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered := make(chan string, 2)
	closed := make(chan string, 2)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		name := "main"
		if bytes.Contains(body, []byte("This is a side question")) {
			name = "side"
		}
		entered <- name
		<-r.Context().Done()
		closed <- name
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	mainDone := make(chan error, 1)
	go func() {
		mainDone <- s.Submit(ctx, tui.Input{Mode: "prompt", Text: "HELD_CLOSE_HUMAN"}, func(tui.Event) error { return nil })
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Parent HTTP")
	}
	handled, err := s.InvokeTerminalCommand(ctx, "btw", "held close side", func(tui.Event) error { return nil })
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Side HTTP")
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatal("Global close returned without physical cancellation")
		}
	}
	select {
	case <-mainDone:
	case <-ctx.Done():
		t.Fatal("Global close did not join main")
	}
	s.terminalBtw.mu.Lock()
	jobs := len(s.terminalBtw.jobs)
	s.terminalBtw.mu.Unlock()
	if jobs != 0 {
		t.Fatal(jobs)
	}
}
