package analytics

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestMetadataPrefixCapturesBeforeAsyncEnrichmentAndKeepsLateSession(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	model, sid := "before-model", "before-session"
	trace := []string{}
	service := testService(t, owner, "http://127.0.0.1:1", func(o *Options) {
		o.BeginRecord = func(scope *cooperative.Scope, event Event) (RecordEnrichment, error) {
			trace = append(trace, "prefix:"+model)
			captured := model
			return func(next *cooperative.Scope) (protocol.JSONObject, error) {
				if next == scope || !next.BelongsTo(owner) {
					return protocol.JSONObject{}, cooperative.ErrRestricted
				}
				trace = append(trace, "enrich:"+sid)
				return protocol.NewJSONObject(field("model", captured), field("session", sid)), nil
			}, nil
		}
		o.Record = func(*cooperative.Scope, Event) (protocol.JSONObject, error) {
			return protocol.JSONObject{}, errors.New("unexpected finite record fallback")
		}
	})
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := service.Log(scope, Event{Name: "epoch", Metadata: protocol.NewJSONObject()}); err != nil {
			return err
		}
		if len(trace) != 1 || trace[0] != "prefix:before-model" {
			return fmt.Errorf("prefix did not run on log stack: %v", trace)
		}
		model, sid = "after-model", "after-session"
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		if len(service.queue) != 1 {
			return fmt.Errorf("enrichment queue: %v", service.queue)
		}
		var value protocol.JSONObject
		if err := value.UnmarshalJSON(service.queue[0]); err != nil {
			return err
		}
		if value.String("model") != "before-model" || value.String("session") != "after-session" {
			return fmt.Errorf("capture/enrichment epochs changed: %s", service.queue[0])
		}
		service.queue = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataPrefixHonorsPrivacySamplingFailuresAndOwnerLifetime(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	count := 0
	failure := errors.New("actual synchronous metadata rejection")
	service := testService(t, owner, "http://127.0.0.1:1", func(o *Options) {
		o.BeginRecord = func(*cooperative.Scope, Event) (RecordEnrichment, error) { count++; return nil, failure }
		o.Config = func(_ *cooperative.Scope, name string) protocol.JSONObject {
			if name == "tengu_event_sampling_config" {
				return protocol.NewJSONObject(field("drop", map[string]any{"sample_rate": 0}))
			}
			return protocol.NewJSONObject()
		}
	})
	var expired *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		if err := service.Log(scope, Event{Name: "drop", Metadata: protocol.NewJSONObject()}); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("dropped event consumed metadata")
		}
		if err := service.Log(scope, Event{Name: "reject", Metadata: protocol.NewJSONObject()}); err != nil {
			return err
		}
		if count != 1 || len(service.queue) != 0 {
			return errors.New("failed prefix escaped or emitted")
		}
		return service.Shutdown(scope)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Log(expired, Event{}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired prefix", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := service.Log(scope, Event{}); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign prefix: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("invalid lifetime consumed prefix", count)
	}
	privacy := testService(t, owner, "http://127.0.0.1:1", func(o *Options) {
		o.Environment = func(*cooperative.Scope) map[string]string { return map[string]string{"DISABLE_TELEMETRY": "1"} }
		o.BeginRecord = func(*cooperative.Scope, Event) (RecordEnrichment, error) { count++; return nil, failure }
	})
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error { return privacy.Log(scope, Event{Name: "private"}) }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("privacy consumed metadata", count)
	}
}
