package sdkhost

import (
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/processpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// RecordPolicyKind is supplied by the actual process host, not by model
// output. These finite profiles cover an unhandled native open EACCES only.
// They do not install signal handlers or implement general Server diagnostics.
type RecordPolicyKind string

const (
	NativeSDKRecordPolicy     RecordPolicyKind = "native"
	CLIHelperRecordPolicy     RecordPolicyKind = "cli-handlers"
	ServerHandlerRecordPolicy RecordPolicyKind = "server-handlers"
)

type RecordFailure struct {
	Original error  `json:"-"`
	Name     string `json:"name"`
	Message  string `json:"message"`
	Code     string `json:"code"`
	Syscall  string `json:"syscall"`
	Path     string `json:"path"`
	Errno    int    `json:"errno"`
	// Source's Bun stack/excerpt and Server persisted sanitizer remain distinct
	// unfinished contracts. No Source-rendered stack is accepted as input.
	StackImplemented bool `json:"stackImplemented"`
}

type RecordPolicyEvent struct {
	Kind     string        `json:"kind"`
	Severity string        `json:"severity"`
	Failure  RecordFailure `json:"failure"`
}

type RecordPolicyOptions struct {
	Owner       *cooperative.Owner
	Kind        RecordPolicyKind
	WriteStderr func([]byte) error
	Diagnostic  func(RecordPolicyEvent) error
	// Authority is borrowed from the same process/Owner host when supplied.
	Authority *processpolicy.Authority
}

type ProcessRecordPolicy struct {
	options   RecordPolicyOptions
	authority *processpolicy.Authority
}

func NewProcessRecordPolicy(options RecordPolicyOptions) (*ProcessRecordPolicy, error) {
	if options.Owner == nil || options.WriteStderr == nil || options.Diagnostic == nil {
		return nil, errors.New("process Record policy requires canonical owner and actual sinks")
	}
	switch options.Kind {
	case NativeSDKRecordPolicy, CLIHelperRecordPolicy, ServerHandlerRecordPolicy:
	default:
		return nil, cap("process Record host policy " + string(options.Kind))
	}
	kind := processpolicy.Native
	if options.Kind == CLIHelperRecordPolicy {
		kind = processpolicy.CLIHelper
	}
	if options.Kind == ServerHandlerRecordPolicy {
		kind = processpolicy.ServerHelper
	}
	authority := options.Authority
	if authority != nil && !authority.Owns(options.Owner, kind) {
		return nil, cooperative.ErrRestricted
	}
	if authority == nil {
		var err error
		authority, err = processpolicy.New(processpolicy.Options{Owner: options.Owner, Kind: kind, WriteStderr: options.WriteStderr, Diagnostic: func(event processpolicy.Event) error {
			failure, err := projectRecordFailure(event.Fault.Original)
			if err != nil {
				return err
			}
			return options.Diagnostic(RecordPolicyEvent{Kind: event.Kind, Severity: event.Severity, Failure: failure})
		}})
		if err != nil {
			return nil, err
		}
	}
	return &ProcessRecordPolicy{options: options, authority: authority}, nil
}

func projectRecordFailure(reason error) (RecordFailure, error) {
	// The exact error itself is retained for identity/Unwrap. Only the actual
	// native shape in Source process proofs is admitted; other errors are caps.
	native, ok := reason.(*os.PathError)
	if !ok || native.Op != "open" || !errors.Is(native.Err, syscall.EACCES) {
		return RecordFailure{}, cap("unhandled Record error shape")
	}
	return RecordFailure{Original: reason, Name: "Error", Message: strings.TrimPrefix(storage.JavaScriptErrorText(reason), "Error: "), Code: "EACCES", Syscall: "open", Path: native.Path, Errno: -13}, nil
}

// Unhandled runs only for an original unawaited Record rejection. It never
// returns that reason to ASK or forces a persistence drain. Native SDK policy
// changes process status; CLI/Server unhandled handlers only emit diagnostics.
func (policy *ProcessRecordPolicy) Unhandled(scope *cooperative.Scope, reason error) error {
	if policy == nil || scope == nil || !scope.BelongsTo(policy.options.Owner) {
		return cooperative.ErrRestricted
	}
	switch policy.options.Kind {
	case NativeSDKRecordPolicy, CLIHelperRecordPolicy, ServerHandlerRecordPolicy:
	default:
		return cap("changed process Record policy")
	}
	failure, err := projectRecordFailure(reason)
	if err != nil {
		return err
	}
	return policy.authority.RecordFault(scope, processpolicy.Fault{Original: reason, Name: failure.Name, Message: failure.Message})
}

// ExitCode is an owner snapshot, not an await-all-Record promise or a decision
// about model abort, uncaught exceptions, or diagnostic write failures.
func (policy *ProcessRecordPolicy) ExitCode(scope *cooperative.Scope, ordinary int) (int, error) {
	if policy == nil || scope == nil || !scope.BelongsTo(policy.options.Owner) {
		return 0, cooperative.ErrRestricted
	}
	return policy.authority.ExitCode(scope, ordinary)
}

// RejectionAuthority exposes a borrowed authority, never a cloned fatal ledger.
func (policy *ProcessRecordPolicy) RejectionAuthority() *processpolicy.Authority {
	if policy == nil {
		return nil
	}
	return policy.authority
}
