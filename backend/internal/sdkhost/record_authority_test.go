package sdkhost

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
	"os"
	"syscall"
	"testing"
)

func TestSharedProcessAuthorityRecordAndPermissionHaveOneExitLedger(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	var out bytes.Buffer
	runtime, _ := processpolicy.New(processpolicy.Options{Owner: owner, Kind: processpolicy.Native, WriteStderr: func(b []byte) error { _, e := out.Write(b); return e }})
	options := RecordPolicyOptions{Owner: owner, Kind: NativeSDKRecordPolicy, Authority: runtime, WriteStderr: func([]byte) error { t.Fatal("borrowed authority used old stderr"); return nil }, Diagnostic: func(RecordPolicyEvent) error { t.Fatal("borrowed authority used old diagnostic"); return nil }}
	policy, e := NewProcessRecordPolicy(options)
	if e != nil {
		t.Fatal(e)
	}
	if policy.RejectionAuthority() != runtime {
		t.Fatal("authority copied")
	}
	options.Owner = other
	if _, e = NewProcessRecordPolicy(options); e == nil {
		t.Fatal("foreign process authority")
	}
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original := errors.New("original permission reason")
		p, _ := ownerasync.NewOwnerFuture[int](owner)
		h, _ := ownerasync.NewOwnerFuture[int](owner)
		if e := runtime.Dispatch(scope, ownerasync.IgnoredRejection{Promise: p.Identity(), Holder: h.Identity(), Site: "success", Original: original}); e != nil {
			return e
		}
		code, _ := policy.ExitCode(scope, 0)
		if code != 1 {
			t.Fatal("Record adapter has a second fatal ledger")
		}
		cause := &os.PathError{Op: "open", Path: "private", Err: syscall.EACCES}
		if e := policy.Unhandled(scope, cause); e != nil {
			return e
		}
		if out.String() != "Error: original permission reason\nError: EACCES: permission denied, open 'private'\n" {
			t.Fatalf("old EACCES format changed: %q", out.String())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var nilPolicy *ProcessRecordPolicy
	if nilPolicy.RejectionAuthority() != nil {
		t.Fatal("nil authority")
	}
}

func TestSharedRecordExitLedgerExternallyObserved(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	authority, _ := processpolicy.New(processpolicy.Options{Owner: owner, Kind: processpolicy.Native, WriteStderr: func([]byte) error { return nil }})
	policy, err := NewProcessRecordPolicy(RecordPolicyOptions{Owner: owner, Kind: NativeSDKRecordPolicy, Authority: authority, WriteStderr: func([]byte) error { return nil }, Diagnostic: func(RecordPolicyEvent) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	code := 0
	cause := errors.New("same actual process permission reason")
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, _ := ownerasync.NewOwnerFuture[int](owner)
		h, _ := ownerasync.NewOwnerFuture[int](owner)
		if err := authority.Dispatch(scope, ownerasync.IgnoredRejection{Promise: p.Identity(), Holder: h.Identity(), Site: "success", Original: cause}); err != nil {
			return err
		}
		var err error
		code, err = policy.ExitCode(scope, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("record adapter detached from actual process fatal ledger: %d", code)
	}
}
