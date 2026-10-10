package sdkhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Go-only deterministic counterexample: original model input admission and
// public message_delta delimit clock changes. No Source observation is input.
func TestSDK744SuccessDurationDoesNotUsePreflightOrigin(t *testing.T) {
	for _, varying := range []bool{false, true} {
		name := "constant-control"
		if varying {
			name = "model-start-excludes-preflight"
		}
		t.Run(name, func(t *testing.T) {
			clock := float64(1000)
			turn := 0
			stdout, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) {
				o.NowMillis = func() float64 { return clock }
				observe := o.ObserveModelInput
				o.ObserveModelInput = func(input requestretry.Input) {
					turn++
					if varying {
						clock = float64(turn * 2000)
					}
					observe(input)
				}
				enqueue := o.Output.Enqueue
				o.Output.Enqueue = func(data []byte) error {
					event := object(data)
					if varying && event.String("type") == "stream_event" && object(event.Get("event")).String("type") == "message_delta" {
						clock = float64(turn*2000 + 30)
					}
					return enqueue(data)
				}
			}})
			if result["runError"] != nil {
				t.Fatal(result["runError"])
			}
			var last protocol.JSONObject
			for _, line := range splitAccountingLines(stdout) {
				v := object(line)
				if v.String("type") == "result" {
					last = v
				}
			}
			var got float64
			if e := json.Unmarshal(last.Get("duration_api_ms"), &got); e != nil {
				t.Fatal(e)
			}
			want := float64(0)
			if varying {
				want = 60
			}
			if got != want {
				t.Fatalf("provider successful duration includes unrelated preflight: got %v want %v", got, want)
			}
		})
	}
}

func TestSuccessDurationControllerDefaultPermissionIsOriginalState(t *testing.T) {
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		return o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			future, e := o.Controller.SuccessPermissionOnOwner(scope)
			if e != nil {
				return e
			}
			value, e := future.Await(scope)
			if e != nil {
				return e
			}
			if value.String("mode") != "default" {
				t.Error("Source getDefaultAppState finite initial mode not retained")
			}
			other := cooperative.New()
			defer other.Close()
			if e = other.Run(context.Background(), func(foreign *cooperative.Scope) error {
				_, e := o.Controller.SuccessPermissionOnOwner(foreign)
				if !errors.Is(e, cooperative.ErrRestricted) {
					t.Error("foreign state read admitted")
				}
				return nil
			}); e != nil {
				return e
			}
			if _, e = o.Controller.SuccessPermissionOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
				t.Error("nil state read admitted")
			}
			return nil
		})
	}})
	if result["runError"] != nil {
		t.Fatal(result["runError"])
	}
}
