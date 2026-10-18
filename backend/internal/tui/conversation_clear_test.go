package tui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestConversationClearPreservesLaterDraftQueueAndBackgroundConsumers(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("next-human draft")
	s.PastedContents = map[int]PastedContent{7: {Content: "actual private paste"}}
	s.history = []HistoryEntry{{Display: "previous readline"}, {Display: "/clear"}}
	s.Queue = []Input{{TurnID: "next-turn", Text: "queued-human", Mode: "prompt"}}
	s.ActiveTurn = "clear-turn"
	s.Preferences = TerminalPreferences{Theme: "light", EditorMode: "vim", PRStatusFooterEnabled: true}
	s.SessionName, s.AgentColor = "previous-name", "pink"
	s.Entries = []Entry{{Kind: "assistant", Text: "old answer"}, {Kind: "tool", ToolID: "old", Running: true}}
	s.LocalPanel = &localPanelState{props: LocalPanel{ID: "previous", Kind: "config"}}
	s.BackgroundTasks = presentationObject(json.RawMessage(`{"worker":{"status":"running","isBackgrounded":true}}`))
	s.TaskItems = []protocol.JSONObject{presentationObject(json.RawMessage(`{"id":"actual-task","status":"pending"}`))}
	queue, history, preferences := append([]Input(nil), s.Queue...), append([]HistoryEntry(nil), s.history...), s.Preferences
	s.Apply(Event{Kind: ConversationCleared, Result: json.RawMessage(`{"sessionId":"new-sid","conversationId":"new-cid","history":[{"type":"system","subtype":"hook_started","uuid":"startup-hook","content":"actual startup hook","level":"info"}]}`)})
	if len(s.Entries) != 1 || s.Entries[0].Text != "actual startup hook" || s.Entries[0].TurnID != "startup-hook" || s.SessionName != "" || s.AgentColor != "" || s.LocalPanel != nil {
		t.Fatalf("clear projection did not reset only the previous conversation: %+v", s)
	}
	if s.Editor.Text() != "next-human draft" || s.PastedContents[7].Content != "actual private paste" || !reflect.DeepEqual(s.Queue, queue) || !reflect.DeepEqual(s.history, history) || s.Preferences != preferences || s.ActiveTurn != "clear-turn" {
		t.Fatalf("clear consumed later input or live settings: %+v", s)
	}
	if len(s.TaskItems) != 1 || s.TaskItems[0].String("id") != "actual-task" || len(s.BackgroundTasks.Get("worker")) == 0 {
		t.Fatal("clear removed actual task consumers")
	}
}

func TestClearNativeLocalAliasesDispatchLocallyAndQueueWhileBusy(t *testing.T) {
	for _, name := range []string{"clear", "reset", "new"} {
		t.Run(name, func(t *testing.T) {
			s := NewState([]Completion{{Name: name, Kind: "local"}}, 80, 24)
			s.Editor.Set("/" + name)
			actions := s.HandleKey(Key{Name: "enter"}, time.Now())
			if len(actions) != 1 || actions[0].kind != actionInvoke || actions[0].name != "command:panel:"+name+"\x00" || s.ActiveTurn != "" {
				t.Fatal("native clear was sent to the model", actions, s.ActiveTurn)
			}
			s.ActiveTurn = "actual-model"
			s.Editor.Set("/" + name)
			if actions := s.HandleKey(Key{Name: "enter"}, time.Now()); len(actions) != 0 || len(s.Queue) != 1 || s.Queue[0].Text != "/"+name {
				t.Fatal("busy clear bypassed the original queued command boundary", actions, s.Queue)
			}
		})
	}
}
