package tui

import (
	"testing"
	"time"
)

func TestExitAndQuitSlashCommandsUseNormalQuitWithoutAgentSubmission(t *testing.T) {
	for _, input := range []string{"/exit", "/quit", " /exit ", "/exit ignored arguments", "/quit ignored arguments"} {
		t.Run(input, func(t *testing.T) {
			s := NewState(nil, 80, 24)
			s.Editor.Set(input)
			actions := s.HandleKey(Key{Name: "enter"}, time.Now())
			if len(actions) != 1 || actions[0].kind != actionQuit || !s.Closing {
				t.Fatalf("local immediate exit routed as Agent prompt: closing=%v actions=%v", s.Closing, actions)
			}
		})
	}
}

func TestExitNegativeSpellingsAndBashModeKeepOriginalInputRouting(t *testing.T) {
	for _, input := range []string{"exit", "quit", "/EXIT", "/Quit", "/exitx", "/exit\targuments", "/quitx"} {
		s := NewState(nil, 80, 24)
		s.Editor.Set(input)
		actions := s.HandleKey(Key{Name: "enter"}, time.Now())
		if len(actions) != 1 || actions[0].kind != actionSubmit || s.Closing {
			t.Fatalf("non-exit input unexpectedly quit: %q %v", input, actions)
		}
	}
	s := NewState(nil, 80, 24)
	s.Mode = "bash"
	s.Editor.Set("/exit")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionSubmit || actions[0].input.Mode != "bash" || s.Closing {
		t.Fatal("Bash input interpreted as slash command", actions)
	}
}

func TestImmediateExitCancelsActiveTurnClearsQueueAndDoesNotStartAnother(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.ActiveTurn = "actual-active"
	s.Queue = []Input{{TurnID: "queued", Text: "queued request"}}
	s.Editor.Set("/exit")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 2 || actions[0].kind != actionCancel || actions[1].kind != actionQuit || !s.Closing || len(s.Queue) != 0 {
		t.Fatal("immediate exit was queued or skipped cancellation", actions)
	}
	if next := s.HandleKey(Key{Name: "enter"}, time.Now()); len(next) != 0 {
		t.Fatal("closing terminal admitted a new turn", next)
	}
}
