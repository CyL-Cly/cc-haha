package providerlogging

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"reflect"
	"testing"
)

func TestActualLocalQueryEventOwnUndefinedAndInputPriority(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	q, _ := New(owner)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		temp := float64(0)
		assistant := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: data("assistant")}, protocol.JSONField{Name: "requestId", Value: data("original-previous")})
		input := requestcontext.Context{Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", TemperatureOverride: &temp}, Thinking: requestcontext.Thinking{Type: "disabled"}, Host: requestcontext.Host{SDKBetas: []string{"original-beta"}, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_MODEL": "original-env-model", "ANTHROPIC_SMALL_FAST_MODEL": "original-small"}}, History: []protocol.JSONObject{assistant}}
		values, e := FromOriginal(input, []protocol.JSONObject{assistant})
		if e != nil {
			return e
		}
		if e = q.LogQuery(scope, values, nil); e != nil {
			return e
		}
		input.Host.Env["ANTHROPIC_MODEL"] = "after-original-capture"
		events, _ := q.OriginalEvents(scope)
		m := events[0].Metadata
		if m.Get("permissionMode") != nil || m.Get("buildAgeMins") != nil {
			t.Fatal("undefined became null")
		}
		keys := []string{}
		for _, property := range m.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			keys = append(keys, name)
		}
		want := []string{"model", "messagesLength", "temperature", "provider", "buildAgeMins", "betas", "permissionMode", "querySource", "thinkingType", "effortValue", "fastMode", "previousRequestId", "baseUrl", "envModel", "envSmallFastModel"}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("Source field order %v", keys)
		}
		if m.String("envModel") != "original-env-model" || string(m.Get("temperature")) != "0" {
			t.Fatal("input priority/immutable capture")
		}
		events[0].Metadata.Set("observer", data(true))
		again, _ := q.OriginalEvents(scope)
		if again[0].Metadata.Get("observer") != nil {
			t.Fatal("metadata observer mutated queue")
		}
		bytes, _ := q.Events(scope)
		raw, _ := json.Marshal(bytes)
		if string(raw) == "[]" || string(raw) == "null" {
			t.Fatal("no real event queue")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestQueryLoggerCapabilityAndOwnerGuards(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	q, _ := New(owner)
	if _, e := New(nil); e == nil {
		t.Fatal("nil logger owner")
	}
	if q.Owns(other) {
		t.Fatal("foreign owner")
	}
	if _, e := FromOriginal(requestcontext.Context{Options: requestcontext.Options{Model: "fixture[1m]"}}, nil); e == nil {
		t.Fatal("unproved beta silently accepted")
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := q.LogQuery(scope, QueryInput{Environment: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}}, nil); e == nil {
			t.Fatal("cloud provider silently firstParty")
		}
		if e := q.LogQuery(scope, QueryInput{Environment: map[string]string{"CLAUDE_CODE_USE_VERTEX": "false"}}, data("default")); e != nil {
			return e
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := q.LogQuery(scope, QueryInput{}, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign log")
		}
		if _, e := q.Events(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign snapshot")
		}
		if _, e := q.OriginalEvents(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign own properties")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestQueuedSinkActualReceiptAndAttachIdempotence(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	q, _ := New(owner)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		receipt, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		got := []string{}
		if err := q.LogQuery(scope, QueryInput{Model: "before"}, nil); err != nil {
			return err
		}
		if err := q.AttachSink(scope, func(next *cooperative.Scope, e Event) error {
			got = append(got, e.Metadata.String("model"))
			return receipt.Resolve(next, struct{}{})
		}); err != nil {
			return err
		}
		if len(got) != 0 {
			t.Fatal("drained inline")
		}
		if err := q.AttachSink(scope, func(*cooperative.Scope, Event) error { t.Fatal("replaced sink"); return nil }); err != nil {
			return err
		}
		if _, err := receipt.Await(scope); err != nil {
			return err
		}
		if err := q.LogQuery(scope, QueryInput{Model: "after"}, nil); err != nil {
			return err
		}
		if !reflect.DeepEqual(got, []string{"before", "after"}) {
			t.Fatal(got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestSinkThrowKeepsOriginalReasonAndOwnerGuard(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	q, _ := New(owner)
	cause := errors.New("original sink")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := q.AttachSink(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		if err := q.AttachSink(scope, func(*cooperative.Scope, Event) error { return cause }); err != nil {
			return err
		}
		if err := q.LogQuery(scope, QueryInput{}, nil); err != cause {
			t.Fatal("sink cause lost", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
