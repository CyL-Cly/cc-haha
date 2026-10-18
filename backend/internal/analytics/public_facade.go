package analytics

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PublicSink is services/analytics/index.ts's attached process sink. The
// downstream logger/exporter remains a separate consumer and lifetime.
type PublicSink struct {
	Log      func(*cooperative.Scope, Event) error
	LogAsync func(*cooperative.Scope, Event) (*ownerasync.OwnerFuture[struct{}], error)
}

type publicQueuedEvent struct {
	event        Event
	asynchronous bool
}

// PublicFacade preserves the original pre-attachment queue, including object
// identity. JSONObject copies share their outer record until the actual sink
// consumes it; no worker accesses these same-owner values.
type PublicFacade struct {
	owner       *cooperative.Owner
	environment func(*cooperative.Scope) map[string]string
	sink        *PublicSink
	queue       []publicQueuedEvent
	closed      bool
}

func NewPublicFacade(owner *cooperative.Owner, environment func(*cooperative.Scope) map[string]string) (*PublicFacade, error) {
	if owner == nil || environment == nil {
		return nil, cooperative.ErrRestricted
	}
	return &PublicFacade{owner: owner, environment: environment}, nil
}

func (facade *PublicFacade) check(scope *cooperative.Scope) error {
	if facade == nil || !scope.BelongsTo(facade.owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	if facade.closed {
		return cooperative.ErrClosed
	}
	return nil
}

func (facade *PublicFacade) Log(scope *cooperative.Scope, event Event) error {
	if err := facade.check(scope); err != nil {
		return err
	}
	if facade.sink == nil {
		facade.queue = append(facade.queue, publicQueuedEvent{event: event})
		return nil
	}
	return facade.sink.Log(scope, event)
}

func (facade *PublicFacade) LogAsync(scope *cooperative.Scope, event Event) (*ownerasync.OwnerFuture[struct{}], error) {
	if err := facade.check(scope); err != nil {
		return nil, err
	}
	result, err := ownerasync.NewOwnerFuture[struct{}](facade.owner)
	if err != nil {
		return nil, err
	}
	if facade.sink == nil {
		facade.queue = append(facade.queue, publicQueuedEvent{event: event, asynchronous: true})
		return result, result.Resolve(scope, struct{}{})
	}
	pending, err := facade.sink.LogAsync(scope, event)
	if err != nil {
		return result, result.Reject(scope, err)
	}
	if pending == nil {
		return result, result.Reject(scope, cooperative.ErrRestricted)
	}
	_, err = pending.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return result.Reject(next, failure)
		}
		return result.Resolve(next, struct{}{})
	})
	return result, err
}

func (facade *PublicFacade) Attach(scope *cooperative.Scope, sink *PublicSink) error {
	if err := facade.check(scope); err != nil {
		return err
	}
	if sink == nil || sink.Log == nil || sink.LogAsync == nil {
		return cooperative.ErrRestricted
	}
	if facade.sink != nil {
		return nil
	}
	facade.sink = sink
	queued := facade.queue
	facade.queue = nil
	if len(queued) == 0 {
		return nil
	}
	if facade.environment(scope)["USER_TYPE"] == "ant" {
		if err := sink.Log(scope, Event{Name: "analytics_sink_attached", Metadata: protocol.NewJSONObject(field("queued_event_count", len(queued)))}); err != nil {
			return err
		}
	}
	_, err := facade.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		if facade.closed {
			return nil
		}
		for _, row := range queued {
			if row.asynchronous {
				if _, err := sink.LogAsync(next, row.event); err != nil {
					return err
				}
			} else if err := sink.Log(next, row.event); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (facade *PublicFacade) Close(scope *cooperative.Scope) error {
	if facade == nil || !scope.BelongsTo(facade.owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	facade.closed = true
	facade.queue = nil
	return nil
}
