package sessionstate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestStateSourceListenerMetadataAndEventOrder(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	trace := []string{}
	enabled := false
	state, e := New(owner, Options{EmitEvents: func() bool { return enabled }, OnState: func(_ *cooperative.Scope, s string, _ *protocol.JSONObject) error {
		trace = append(trace, "state:"+s)
		return nil
	}, OnMetadata: func(_ *cooperative.Scope, v protocol.JSONObject) error {
		b, e := v.MarshalJSON()
		trace = append(trace, "metadata:"+string(b))
		return e
	}, EnqueueSDK: func(_ *cooperative.Scope, v protocol.JSONObject) error {
		b, e := v.MarshalJSON()
		trace = append(trace, "sdk:"+string(b))
		return e
	}})
	if e != nil {
		t.Fatal(e)
	}
	e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		if v, e := state.Current(s); v != "idle" || e != nil {
			return errors.New("initial state")
		}
		details := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: []byte(`"Read"`)})
		if e := state.Change(s, "requires_action", &details); e != nil {
			return e
		}
		enabled = true
		if e := state.Change(s, "running", nil); e != nil {
			return e
		}
		return state.Change(s, "idle", nil)
	})
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"state:requires_action", `metadata:{"pending_action":{"tool_name":"Read"}}`, "state:running", `metadata:{"pending_action":null}`, `sdk:{"type":"system","subtype":"session_state_changed","state":"running"}`, "state:idle", `metadata:{"task_summary":null}`, `sdk:{"type":"system","subtype":"session_state_changed","state":"idle"}`}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("listener trace\n%v\n%v", want, trace)
	}
}
func TestStateListenerFailurePreservesOriginalCommittedPrefix(t *testing.T) {
	for _, boundary := range []string{"state", "pending-metadata", "idle-metadata", "sdk"} {
		t.Run(boundary, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			original := errors.New("original listener error")
			trace := []string{}
			state, e := New(owner, Options{EmitEvents: func() bool { return true }, OnState: func(_ *cooperative.Scope, s string, _ *protocol.JSONObject) error {
				trace = append(trace, "state")
				if boundary == "state" {
					return original
				}
				return nil
			}, OnMetadata: func(_ *cooperative.Scope, v protocol.JSONObject) error {
				trace = append(trace, "metadata")
				if boundary == "pending-metadata" || boundary == "idle-metadata" {
					return original
				}
				return nil
			}, EnqueueSDK: func(*cooperative.Scope, protocol.JSONObject) error { trace = append(trace, "sdk"); return original }})
			if e != nil {
				t.Fatal(e)
			}
			e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
				next := "requires_action"
				d := protocol.NewJSONObject()
				if boundary == "idle-metadata" {
					next = "idle"
				}
				if e := state.Change(s, next, &d); e != original {
					return errors.New("error identity replaced")
				}
				got, e := state.Current(s)
				if e != nil {
					return e
				}
				if got != next {
					return errors.New("current state reverted after listener error")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			switch boundary {
			case "state":
				if len(trace) != 1 || state.pendingAction {
					t.Fatal(trace, state.pendingAction)
				}
			case "pending-metadata":
				if len(trace) != 2 || !state.pendingAction {
					t.Fatal(trace, state.pendingAction)
				}
			case "idle-metadata":
				if len(trace) != 2 {
					t.Fatal(trace)
				}
			case "sdk":
				if len(trace) != 3 || !state.pendingAction {
					t.Fatal(trace)
				}
			}
		})
	}
}
func TestStateNullListenersOwnershipAndMissingEventQueue(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	if _, e := New(nil, Options{EmitEvents: func() bool { return false }}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := New(owner, Options{}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	emit := false
	state, e := New(owner, Options{EmitEvents: func() bool { return emit }})
	if e != nil {
		t.Fatal(e)
	}
	if state.Owns(other) {
		t.Fatal("foreign state")
	}
	if e := other.Run(context.Background(), func(s *cooperative.Scope) error {
		if _, e := state.Current(s); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign getter")
		}
		if e := state.Change(s, "running", nil); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign mutation")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		if e := state.Change(s, "unknown", nil); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("unknown status")
		}
		if e := state.Change(s, "running", nil); e != nil {
			return e
		}
		emit = true
		if e := state.Change(s, "idle", nil); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("missing event queue admitted")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
