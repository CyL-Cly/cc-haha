package sessioncontroller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

func TestInboxExtractionDefersConsumptionToCanonicalConsumer(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint("native=", native), func(t *testing.T) {
			c, _, owner := boundController(t)
			pending, e := NewPendingWriter(c.writer, owner)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = pending.Close(context.Background()) })
			receipts := []protocol.JSONObject{}
			e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := c.ActivateOwned(scope, activation()); e != nil {
					return e
				}
				if e := c.BindSessionInboxOnOwner(scope, func(receipt protocol.JSONObject) error { receipts = append(receipts, receipt); return nil }); e != nil {
					return e
				}
				input := object([]byte(`{"subtype":"enqueue_session_message","message_id":"deferred","sender_session_id":"peer","text":"progress"}`))
				if _, e := c.AcceptSessionMessageOnOwner(scope, input, false); e != nil {
					return e
				}
				uuid := sessioninbox.MessageUUID(quote("deferred"))
				if native {
					other, e := c.TakeNativeQueuedAttachmentsOnOwner(scope, "foreign-agent")
					if e != nil {
						return e
					}
					if len(other) != 0 || len(c.queue) != 1 {
						return errors.New("root inbox leaked into an Agent queue")
					}
				}
				var taken []protocol.JSONObject
				var e error
				if native {
					taken, e = c.TakeNativeQueuedAttachmentsOnOwner(scope, "")
				} else {
					taken, e = c.TakeSessionInboxAttachmentsOnOwner(scope)
				}
				if e != nil {
					return e
				}
				if len(taken) != 1 || taken[0].String("source_uuid") != uuid || len(c.queue) != 0 || len(receipts) != 0 {
					return errors.New("queue extraction acknowledged instead of yielding the pending attachment")
				}
				if pending, e := c.SessionMessagePendingOnOwner(scope, uuid); e != nil || !pending {
					return fmt.Errorf("pending extraction: %v/%v", pending, e)
				}
				if duplicate, e := c.AcceptSessionMessageOnOwner(scope, input, false); e != nil || duplicate.String("status") != "queued" || string(duplicate.Get("duplicate")) != "true" {
					return errors.New("extracted delivery lost original pending duplicate status")
				}
				if e := c.ConsumeSessionMessageOnOwner(scope, uuid); e != nil {
					return e
				}
				if e := c.ConsumeSessionMessageOnOwner(scope, uuid); e != nil {
					return e
				}
				if len(receipts) != 1 || receipts[0].String("status") != "consumed" {
					return errors.New("canonical consumer must acknowledge once")
				}
				if pending, e := c.SessionMessagePendingOnOwner(scope, uuid); e != nil || pending {
					return fmt.Errorf("consumed pending: %v/%v", pending, e)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestInboxCanonicalConsumerScopeAndAbsentInbox(t *testing.T) {
	c, _, owner := boundController(t)
	for _, receiver := range []*Controller{nil, {}} {
		if _, e := receiver.SessionMessagePendingOnOwner(nil, "id"); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
		if e := receiver.ConsumeSessionMessageOnOwner(nil, "id"); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
		if _, e := receiver.ReplayUserMessagesOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal(e)
		}
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := c.SessionMessagePendingOnOwner(scope, "id"); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign pending lookup")
		}
		if e := c.ConsumeSessionMessageOnOwner(scope, "id"); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign consumption")
		}
		if _, e := c.ReplayUserMessagesOnOwner(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign replay option")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if pending, e := c.SessionMessagePendingOnOwner(scope, "id"); e != nil || pending {
			return errors.New("unregistered source UUID became pending")
		}
		if e := c.ConsumeSessionMessageOnOwner(scope, "id"); e != nil {
			return e
		}
		for _, replay := range []bool{false, true} {
			c.options.ReplayUserMessages = replay
			if actual, e := c.ReplayUserMessagesOnOwner(scope); e != nil || actual != replay {
				return errors.New("replay configuration did not follow the actual Controller")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
