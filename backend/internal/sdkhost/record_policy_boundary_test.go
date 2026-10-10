package sdkhost_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestProcessRecordActualReadOnlyDoesNotChangeASKFinally(t *testing.T) {
	for _, kind := range []sdkhost.RecordPolicyKind{sdkhost.NativeSDKRecordPolicy, sdkhost.CLIHelperRecordPolicy, sdkhost.ServerHandlerRecordPolicy} {
		t.Run(string(kind), func(t *testing.T) {
			var policy *sdkhost.ProcessRecordPolicy
			var observed error
			var policyFailure error
			var events []sdkhost.RecordPolicyEvent
			code := 0
			changed := false
			var stderr strings.Builder
			stdout, result := originalBoundary(t, nativeControls{Configure: func(o *sdkhost.Options) {
				var e error
				policy, e = sdkhost.NewProcessRecordPolicy(sdkhost.RecordPolicyOptions{Owner: o.Owner, Kind: kind, WriteStderr: func(b []byte) error { _, e := stderr.Write(b); return e }, Diagnostic: func(event sdkhost.RecordPolicyEvent) error { events = append(events, event); return nil }})
				must(e)
				o.UnhandledRecord = func(s *cooperative.Scope, reason error) {
					observed = reason
					policyFailure = policy.Unhandled(s, reason)
				}
				original := o.Output.Enqueue
				o.Output.Enqueue = func(data []byte) error {
					if !changed && object(data).String("type") == "assistant" {
						changed = true
						project := projectpath.Sanitize(o.CWD)
						must(os.Chmod(filepath.Join(o.CWD, ".claude", "projects", project, o.Controller.SessionID()+".jsonl"), 0400))
					}
					return original(data)
				}
			}, Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
				if e := o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, context.Background(), inputs[0]) }); e != nil {
					return e
				}
				if e := h.WaitTranscriptRuntime(context.Background()); e != nil {
					return e
				}
				return o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { var e error; code, e = policy.ExitCode(s, h.ExitCode()); return e })
			}})
			var native *os.PathError
			if !changed || !errors.As(observed, &native) || !errors.Is(observed, syscall.EACCES) || policyFailure != nil {
				t.Fatal(changed, observed, policyFailure)
			}
			if result["runError"] != nil {
				t.Fatal("unhandled Record was made synchronous ASK error", result["runError"])
			}
			want := 0
			if kind == sdkhost.NativeSDKRecordPolicy {
				want = 1
			}
			if code != want {
				t.Fatal("host status collapse", code)
			}
			if !strings.Contains(string(stdout), `"subtype":"success"`) || !strings.Contains(strings.Join(result["phases"].([]string), "|"), "checkpoint:settled") || !strings.Contains(strings.Join(result["phases"].([]string), "|"), "ask:finally") || len(result["cache"].([]protocol.JSONObject)) != 0 {
				t.Fatal("real result/checkpoint/cache/finally changed")
			}
			if kind != sdkhost.NativeSDKRecordPolicy && (len(events) != 1 || events[0].Failure.Original != observed) {
				t.Fatal("original error replaced by diagnostic text")
			}
		})
	}
}
