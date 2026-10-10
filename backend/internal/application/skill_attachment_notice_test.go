package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"testing"
)

func TestSkillQueryAttachmentsReachOnlyTheirActualTerminalTurn(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	events := []tui.Event{}
	s := &Session{process: &clibootstrap.MainPrint{Owner: owner}, turnID: "native-turn", emit: func(e tui.Event) error { events = append(events, e); return nil }}
	rows := []string{`{"type":"dynamic_skill","skillNames":["one"],"displayPath":".claude/skills"}`, `{"type":"dynamic_skill","skillNames":["one","two"],"displayPath":"sub/.claude/skills"}`, `{"type":"skill_listing","skillCount":1,"isInitial":true}`, `{"type":"skill_listing","skillCount":2,"isInitial":false}`, `{"type":"command_permissions"}`}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, payload := range rows {
			message := queryloop.Message{Record: obj(raw(map[string]any{"type": "attachment", "attachment": obj([]byte(payload))}))}
			if err := s.nativeQueryYield(scope, &queryloop.Yield{Message: &message}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Loaded 1 skill from .claude/skills", "Loaded 2 skills from sub/.claude/skills", "2 skills available"}
	if len(events) != len(want) {
		t.Fatal("hidden initial/command attachment became visible", events)
	}
	for i, event := range events {
		if event.Kind != tui.Notice || event.Text != want[i] || event.TurnID != "native-turn" {
			t.Fatal("actual attachment display changed", event)
		}
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { return s.nativeQueryYield(scope, nil) }); err != cooperative.ErrRestricted {
		t.Fatal("foreign query notice admitted", err)
	}
}
