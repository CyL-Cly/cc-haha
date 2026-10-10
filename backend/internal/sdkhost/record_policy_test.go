package sdkhost

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestProcessRecordPoliciesRetainNativeErrorAndDistinctExit(t *testing.T) {
	for _, kind := range []RecordPolicyKind{NativeSDKRecordPolicy, CLIHelperRecordPolicy, ServerHandlerRecordPolicy} {
		t.Run(string(kind), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			path := filepath.Join(t.TempDir(), "original-private.jsonl")
			if e := os.WriteFile(path, []byte("original\n"), 0400); e != nil {
				t.Fatal(e)
			}
			handle, reason := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if reason == nil {
				handle.Close()
				t.Fatal("native readonly fault not reproduced")
			}
			var out bytes.Buffer
			var events []RecordPolicyEvent
			policy, e := NewProcessRecordPolicy(RecordPolicyOptions{Owner: owner, Kind: kind, WriteStderr: func(b []byte) error { _, e := out.Write(b); return e }, Diagnostic: func(event RecordPolicyEvent) error { events = append(events, event); return nil }})
			if e != nil {
				t.Fatal(e)
			}
			e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
				if before, e := policy.ExitCode(s, 0); e != nil || before != 0 {
					return errors.New("pre-fault exit changed")
				}
				if e := policy.Unhandled(s, reason); e != nil {
					return e
				}
				result, e := policy.ExitCode(s, 0)
				want := 0
				if kind == NativeSDKRecordPolicy {
					want = 1
				}
				if e != nil || result != want {
					return errors.New("host exit policy collapsed")
				}
				result, e = policy.ExitCode(s, 7)
				if e != nil || kind != NativeSDKRecordPolicy && result != 7 {
					return errors.New("ordinary exit overwritten")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			if kind == NativeSDKRecordPolicy {
				if len(events) != 0 || out.Len() == 0 {
					t.Fatal("native policy claimed handler diagnostic")
				}
			} else {
				if len(events) != 1 || events[0].Failure.Original != reason || !errors.Is(events[0].Failure.Original, syscall.EACCES) || events[0].Failure.StackImplemented || events[0].Failure.Message != "EACCES: permission denied, open '"+path+"'" {
					t.Fatal(events)
				}
				if kind == CLIHelperRecordPolicy && out.Len() != 0 || kind == ServerHandlerRecordPolicy && out.Len() == 0 {
					t.Fatal("wrong process stderr policy")
				}
			}
			after, e := os.ReadFile(path)
			if e != nil || string(after) != "original\n" {
				t.Fatal("policy changed original native file")
			}
		})
	}
}

func TestProcessRecordPolicyGuardsDoNotApproximateErrors(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	good := RecordPolicyOptions{Owner: owner, Kind: CLIHelperRecordPolicy, WriteStderr: func([]byte) error { return nil }, Diagnostic: func(RecordPolicyEvent) error { return nil }}
	for _, input := range []RecordPolicyOptions{{}, {Owner: owner, Kind: CLIHelperRecordPolicy}, {Owner: owner, Kind: CLIHelperRecordPolicy, WriteStderr: good.WriteStderr}, {Owner: owner, Kind: "unknown", WriteStderr: good.WriteStderr, Diagnostic: good.Diagnostic}} {
		if _, e := NewProcessRecordPolicy(input); e == nil {
			t.Fatal("incomplete policy accepted")
		}
	}
	policy, e := NewProcessRecordPolicy(good)
	if e != nil {
		t.Fatal(e)
	}
	if e := policy.Unhandled(nil, errors.New("x")); e != cooperative.ErrRestricted {
		t.Fatal(e)
	}
	var nilPolicy *ProcessRecordPolicy
	if e := nilPolicy.Unhandled(nil, nil); e != cooperative.ErrRestricted {
		t.Fatal(e)
	}
	if _, e := nilPolicy.ExitCode(nil, 0); e != cooperative.ErrRestricted {
		t.Fatal(e)
	}
	e = foreign.Run(context.Background(), func(s *cooperative.Scope) error {
		if policy.Unhandled(s, nil) != cooperative.ErrRestricted {
			return errors.New("foreign error admission")
		}
		if _, e := policy.ExitCode(s, 0); e != cooperative.ErrRestricted {
			return errors.New("foreign exit admission")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		for _, input := range []error{nil, errors.New("unknown"), &os.PathError{Op: "read", Path: "private", Err: syscall.EACCES}, &os.PathError{Op: "open", Path: "private", Err: syscall.ENOENT}} {
			var capability *CapabilityError
			if !errors.As(policy.Unhandled(s, input), &capability) {
				return errors.New("unknown native shape silently approximated")
			}
		}
		policy.options.Kind = "mutated"
		var capability *CapabilityError
		reason := &os.PathError{Op: "open", Path: "private", Err: syscall.EACCES}
		if !errors.As(policy.Unhandled(s, reason), &capability) {
			return errors.New("unknown changed policy accepted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestProcessRecordPolicySinkFailuresPreserveIdentity(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	failure := errors.New("original sink error")
	reason := &os.PathError{Op: "open", Path: "private", Err: syscall.EACCES}
	for _, kind := range []RecordPolicyKind{NativeSDKRecordPolicy, CLIHelperRecordPolicy, ServerHandlerRecordPolicy} {
		for _, writeFails := range []bool{false, true} {
			policy, e := NewProcessRecordPolicy(RecordPolicyOptions{Owner: owner, Kind: kind, WriteStderr: func([]byte) error {
				if writeFails {
					return failure
				}
				return nil
			}, Diagnostic: func(RecordPolicyEvent) error { return failure }})
			if e != nil {
				t.Fatal(e)
			}
			e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
				e := policy.Unhandled(s, reason)
				if kind == NativeSDKRecordPolicy && !writeFails {
					if e != nil {
						return e
					}
				} else if e != failure {
					return errors.New("sink error identity replaced")
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestProcessRecordNativeRuntimeCapabilityGuard(t *testing.T) {
	if (*Host)(nil).WaitTranscriptRuntime(context.Background()) != cooperative.ErrRestricted {
		t.Fatal("nil host accepted")
	}
	if (&Host{}).WaitTranscriptRuntime(context.Background()) != cooperative.ErrRestricted {
		t.Fatal("nil pending service accepted")
	}
}
