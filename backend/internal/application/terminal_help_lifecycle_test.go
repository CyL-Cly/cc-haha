package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalHelpActualMountedContextReplacementAndGlobalCloseRetireOnlyItsCompletion(t *testing.T) {
	for _, fullscreen := range []bool{false, true} {
		for _, exit := range []string{"context", "replacement", "global-close"} {
			mode := "scrollback/"
			if fullscreen {
				mode = "fullscreen/"
			}
			t.Run(mode+exit, func(t *testing.T) {
				o := nativeSessionOptions(t, "http://127.0.0.1:1")
				o.Interactive = true
				if fullscreen {
					o.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
					o.Native.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
				}
				s, err := NewSession(context.Background(), o)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				opened := make(chan tui.Event, 2)
				emit := func(event tui.Event) error {
					if event.Kind == tui.LocalPanelOpened {
						opened <- event
					}
					return nil
				}
				done := make(chan error, 1)
				go func() { _, err := s.InvokeTerminalCommand(ctx, "help", "retired help args", emit); done <- err }()
				var first tui.Event
				select {
				case first = <-opened:
				case <-time.After(3 * time.Second):
					t.Fatal("actual help mount absent")
				}
				select {
				case err := <-done:
					t.Fatal("Source Help promise settled before onDone", err)
				default:
				}
				var want error
				switch exit {
				case "context":
					cancel()
					want = context.Canceled
				case "replacement":
					if handled, err := s.InvokeTerminalCommand(context.Background(), "skills", "", emit); !handled || err != nil {
						t.Fatal(handled, err)
					}
					second := <-opened
					if second.LocalPanel.ID == first.LocalPanel.ID || second.LocalPanel.Kind != "skills" {
						t.Fatal("replacement did not own new mount", second)
					}
					if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: first.LocalPanel.ID, Action: "help-close"}, emit); err == nil {
						t.Fatal("retired Help callback admitted")
					}
					want = cooperative.ErrClosed
				case "global-close":
					closed := make(chan error, 1)
					go func() { closed <- s.Close(context.Background()) }()
					select {
					case err := <-closed:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("global close did not join active help")
					}
					want = cooperative.ErrClosed
				}
				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatal("owned help completion cause", err, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("Help context/unmount completion leaked")
				}
			})
		}
	}
}
