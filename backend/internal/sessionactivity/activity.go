// Package sessionactivity ports the callback-null process activity profile.
// Counters and reasons are independent Source module state. The same existing
// Owner, diagnostic Process, and process cleanup Registry are borrowed.
// Remote heartbeat/idle timer execution remains an explicit unmigrated profile.
package sessionactivity

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"strings"
	"unicode"
)

type Reason string

const APICall Reason = "api_call"
const ToolExec Reason = "tool_exec"

type entry struct {
	reason Reason
	count  int
}
type Tracker struct {
	owner             *cooperative.Owner
	registry          *runtimecleanup.Registry
	process           *diagnostics.Process
	refs              int
	reasons           []entry
	oldest            *float64
	cleanupRegistered bool
	callback          func(*cooperative.Scope) error
}

// CapabilityError identifies a real transport boundary that the current
// unique Owner cannot yet supply. Its existing ArmTimer is one-shot, not the
// Source native repeating interval. No timer or synthetic callback is created.
type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "session activity capability unavailable: " + e.Boundary
}
func (e *CapabilityError) ErrorName() string { return "CapabilityError" }

func (tracker *Tracker) checkOwner(scope *cooperative.Scope) error {
	if tracker == nil || !scope.BelongsTo(tracker.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func (tracker *Tracker) RegisterCallback(scope *cooperative.Scope, callback func(*cooperative.Scope) error) error {
	if err := tracker.checkOwner(scope); err != nil {
		return err
	}
	if callback == nil {
		return cooperative.ErrRestricted
	}
	tracker.callback = callback
	if tracker.refs > 0 {
		return tracker.requireHeartbeat()
	}
	return nil
}
func (tracker *Tracker) UnregisterCallback(scope *cooperative.Scope) error {
	if err := tracker.checkOwner(scope); err != nil {
		return err
	}
	tracker.callback = nil
	return nil
}
func (tracker *Tracker) TrackingActive(scope *cooperative.Scope) (bool, error) {
	if err := tracker.checkOwner(scope); err != nil {
		return false, err
	}
	return tracker.callback != nil, nil
}
func keepaliveTruthy(value string) bool {
	value = strings.ToLower(strings.TrimFunc(value, func(r rune) bool {
		return r == 9 || r == 10 || r == 11 || r == 12 || r == 13 || r == 0xfeff || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Zs, r)
	}))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
func (tracker *Tracker) SendSignal(scope *cooperative.Scope) error {
	if err := tracker.checkOwner(scope); err != nil {
		return err
	}
	if keepaliveTruthy(tracker.process.Environment("CLAUDE_CODE_REMOTE_SEND_KEEPALIVES")) && tracker.callback != nil {
		return tracker.callback(scope)
	}
	return nil
}
func (tracker *Tracker) requireHeartbeat() error {
	return &CapabilityError{Boundary: "same-Owner repeating native 30s heartbeat interval"}
}

func NewCallbackNull(owner *cooperative.Owner, registry *runtimecleanup.Registry, process *diagnostics.Process) (*Tracker, error) {
	if !registry.Owns(owner) || process == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Tracker{owner: owner, registry: registry, process: process}, nil
}
func (tracker *Tracker) Owns(owner *cooperative.Owner) bool {
	return tracker != nil && owner != nil && tracker.owner == owner
}
func (tracker *Tracker) UsesDiagnostics(process *diagnostics.Process) bool {
	return tracker != nil && tracker.process == process
}
func (tracker *Tracker) UsesRegistry(registry *runtimecleanup.Registry) bool {
	return tracker != nil && tracker.registry == registry
}
func (tracker *Tracker) check(scope *cooperative.Scope, reason Reason) error {
	if tracker == nil || !scope.BelongsTo(tracker.owner) || (reason != APICall && reason != ToolExec) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

// Start commits refs/reasons before the first clock. An original clock error
// leaves those changes in place and precedes cleanup registration. A reason
// deleted then started again goes to the end of the ordered Map.
func (tracker *Tracker) Start(scope *cooperative.Scope, reason Reason) error {
	if err := tracker.check(scope, reason); err != nil {
		return err
	}
	tracker.refs++
	found := false
	for i := range tracker.reasons {
		if tracker.reasons[i].reason == reason {
			tracker.reasons[i].count++
			found = true
			break
		}
	}
	if !found {
		tracker.reasons = append(tracker.reasons, entry{reason, 1})
	}
	if tracker.refs == 1 {
		now, err := tracker.process.ReadNowMillis()
		if err != nil {
			return err
		}
		tracker.oldest = &now
		if tracker.callback != nil {
			return tracker.requireHeartbeat()
		}
	}
	if !tracker.cleanupRegistered {
		tracker.cleanupRegistered = true
		_, err := tracker.registry.Register(scope, runtimecleanup.NewCallback(tracker.startCleanup))
		return err
	}
	return nil
}

// Stop intentionally does not require a matching reason. Source decrements
// refs independently of its ordered reason Map, even after failed creation.
func (tracker *Tracker) Stop(scope *cooperative.Scope, reason Reason) error {
	if err := tracker.check(scope, reason); err != nil {
		return err
	}
	if tracker.refs > 0 {
		tracker.refs--
	}
	for i := range tracker.reasons {
		if tracker.reasons[i].reason != reason {
			continue
		}
		tracker.reasons[i].count--
		if tracker.reasons[i].count <= 0 {
			tracker.reasons = append(tracker.reasons[:i], tracker.reasons[i+1:]...)
		}
		break
	}
	return nil
}

// The registered callback is async. Its body reads live process state when
// Registry.Start invokes it. Date/serialization/clock errors reject its Promise
// and cannot synchronously throw out of Array.map before later callbacks start.
func (tracker *Tracker) startCleanup(scope *cooperative.Scope) (*runtimecleanup.Future, error) {
	if !scope.BelongsTo(tracker.owner) {
		return nil, cooperative.ErrRestricted
	}
	future, err := ownerasync.NewOwnerFuture[struct{}](tracker.owner)
	if err != nil {
		return nil, err
	}
	failure := tracker.logState()
	if failure != nil {
		err = future.Reject(scope, failure)
	} else {
		err = future.Resolve(scope, struct{}{})
	}
	if err != nil {
		_ = future.RejectAdministrative(err)
	}
	return future, nil
}
func (tracker *Tracker) logState() error {
	active := protocol.NewJSONObject()
	for _, reason := range tracker.reasons {
		active.Set(string(reason.reason), diagnostics.Number(float64(reason.count)))
	}
	elapsed := json.RawMessage("null")
	if tracker.refs > 0 && tracker.oldest != nil {
		now, err := tracker.process.ReadNowMillis()
		if err != nil {
			return err
		}
		elapsed = diagnostics.Number(now - *tracker.oldest)
	}
	encoded, err := active.MarshalJSON()
	if err != nil {
		return err
	}
	data := protocol.NewJSONObject(protocol.JSONField{Name: "refcount", Value: diagnostics.Number(float64(tracker.refs))}, protocol.JSONField{Name: "active", Value: encoded}, protocol.JSONField{Name: "oldest_activity_ms", Value: elapsed})
	return tracker.process.Log("info", "session_activity_at_shutdown", &data)
}
