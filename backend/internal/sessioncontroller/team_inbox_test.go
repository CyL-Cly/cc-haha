package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestLeaderInboxOwnerStatusIsolationAndLifecycle(t *testing.T) {
	c, _, owner := boundController(t)
	c.active = true
	message := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quote("worker")}, protocol.JSONField{Name: "text", Value: []byte(`"raw \ud800"`)}, protocol.JSONField{Name: "timestamp", Value: quote("t")}, protocol.JSONField{Name: "summary", Value: quote("exact summary")})
	var stale *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		if err := c.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{message}); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("standalone queue admitted", err)
		}
		if err := c.SetLeaderTeamOnOwner(scope, "actual-team"); err != nil {
			return err
		}
		if err := c.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{message}); err != nil {
			return err
		}
		pending, err := c.PendingTeamInboxOnOwner(scope)
		if err != nil || len(pending) != 1 || string(pending[0].Get("text")) != `"raw \ud800"` {
			t.Fatal("pending raw message", pending, err)
		}
		pending[0].Set("text", quote("foreign snapshot mutation"))
		again, _ := c.PendingTeamInboxOnOwner(scope)
		if string(again[0].Get("text")) != `"raw \ud800"` {
			t.Fatal("snapshot borrowed mutable inbox")
		}
		c.appState.ViewingAgentTaskID = "teammate-view"
		if viewed, err := c.PendingTeamInboxOnOwner(scope); err != nil || len(viewed) != 0 {
			t.Fatal("leader inbox leaked into viewed teammate", viewed, err)
		}
		c.appState.ViewingAgentTaskID = ""
		if err := c.ProcessTeamInboxOnOwner(scope, again); err != nil {
			return err
		}
		if len(c.appState.Inbox) != 1 || c.appState.Inbox[0].String("status") != "processed" {
			t.Fatal("midturn status missing")
		}
		if err := c.FinishTeamInboxOnOwner(scope, nil); err != nil {
			return err
		}
		if len(c.appState.Inbox) != 0 {
			t.Fatal("idle processed queue not cleared")
		}
		if err := c.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{message}); err != nil {
			return err
		}
		if err := c.SetLeaderTeamOnOwner(scope, ""); err != nil {
			return err
		}
		if len(c.appState.Inbox) != 0 {
			t.Fatal("team delete preserved stale messages")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.QueueTeamInboxOnOwner(stale, []protocol.JSONObject{message}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale owner admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		return c.QueueTeamInboxOnOwner(scope, []protocol.JSONObject{message})
	}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("foreign owner admitted", err)
	}
}
