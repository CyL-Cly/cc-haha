// This disposable native fixture interprets the frozen original helper input.
// It calls the single migrated Activity/Registry/Process implementations; no
// Source result, prepared state, phase schedule, or expected output is an input.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
)

type descriptor struct{ Name, Kind, Error string }
type operation struct {
	Call, Reason, Name, Caller string
	Value                      json.RawMessage
}
type program struct {
	ID                                        string `json:"id"`
	Enabled                                   bool
	InitialNow, NowStep, DateMillis           float64
	DateEffects, NowEffects, SerializeEffects []string
	KeepaliveEnv                              *string
	Storage                                   string
	Callbacks                                 []descriptor
	Operations                                []operation
	ExternalControlUTF8                       string
	Errors                                    map[string]string
}
type envelope struct {
	Profile program
	Guard   string
}
type row = map[string]any

type capabilityError struct{ Boundary string }

func (e *capabilityError) Error() string {
	return "native helper fixture capability unavailable: " + e.Boundary
}
func (e *capabilityError) ErrorName() string { return "CapabilityError" }

type observer struct {
	trace, fsTrace, identities []row
	ids                        map[any]string
	next                       int
	home                       string
	unsupported                []string
}

func (o *observer) mark(operation string, data row) {
	r := row{"ordinal": len(o.trace) + 1, "operation": operation}
	for k, v := range data {
		r[k] = v
	}
	o.trace = append(o.trace, r)
}
func (o *observer) identity(value any, declared, kind string) string {
	if value == nil {
		return ""
	}
	if v, ok := o.ids[value]; ok {
		return v
	}
	if declared == "" {
		o.next++
		declared = fmt.Sprintf("observed-object-%d", o.next)
	}
	o.ids[value] = declared
	o.identities = append(o.identities, row{"identity": declared, "kind": kind, "actualGoType": fmt.Sprintf("%T", value)})
	return declared
}
func (o *observer) shape(e error) row {
	if e == nil {
		return nil
	}
	name := "Error"
	if n, ok := e.(interface{ ErrorName() string }); ok {
		name = n.ErrorName()
	}
	r := row{"identity": o.identity(e, "", name), "name": name, "message": e.Error(), "actualGoType": fmt.Sprintf("%T", e)}
	var path *os.PathError
	if errors.As(e, &path) {
		r["path"] = path.Path
		r["syscall"] = path.Op
		var errno syscall.Errno
		if errors.As(path.Err, &errno) {
			r["errno"] = int(errno)
			r["code"] = errno.Error()
		}
	}
	return r
}
func (o *observer) unsupportedError(boundary string) error {
	o.unsupported = append(o.unsupported, boundary)
	return &capabilityError{boundary}
}
func (o *observer) check(path string) error {
	rel, e := filepath.Rel(o.home, path)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return &capabilityError{"outside owned HOME filesystem"}
	}
	for p := path; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&fs.ModeSymlink != 0 {
			return &capabilityError{"symbolic-link filesystem target"}
		}
		if e != nil && !errors.Is(e, fs.ErrNotExist) {
			return e
		}
		if p == o.home {
			break
		}
	}
	return nil
}

// All production logger operations enter this explicit private native boundary.
// Native metadata/error differences remain in raw evidence, never replaced by
// Source values. There is no provider or subprocess capability in this fixture.
type privateFiles struct {
	o      *observer
	native diagnostics.OSFileSystem
}

func (f privateFiles) invoke(op, path string, data row, work func() (any, error)) (any, error) {
	r := row{"operation": op, "path": path}
	for k, v := range data {
		r[k] = v
	}
	f.o.fsTrace = append(f.o.fsTrace, r)
	f.o.mark("originalFS.invoke", row{"operation": op, "ordinalWithinFS": len(f.o.fsTrace)})
	if e := f.o.check(path); e != nil {
		r["error"] = f.o.shape(e)
		return nil, e
	}
	value, e := work()
	if e != nil {
		r["error"] = f.o.shape(e)
	} else {
		switch v := value.(type) {
		case nil:
			r["result"] = row{"kind": "undefined"}
		case bool:
			r["result"] = row{"kind": "boolean", "value": v}
		case []string:
			r["result"] = row{"kind": "array", "value": v}
		case fs.FileInfo:
			r["result"] = row{"kind": "stat", "size": v.Size(), "mode": v.Mode().Perm(), "isFile": v.Mode().IsRegular(), "isDirectory": v.IsDir(), "isSymbolicLink": v.Mode()&fs.ModeSymlink != 0}
		}
	}
	return value, e
}
func (f privateFiles) MkdirAll(p string, m fs.FileMode) error {
	_, e := f.invoke("mkdir", p, row{"mode": m}, func() (any, error) { return nil, f.native.MkdirAll(p, m) })
	return e
}
func (f privateFiles) Lstat(p string) (fs.FileInfo, error) {
	v, e := f.invoke("lstat", p, nil, func() (any, error) { return f.native.Lstat(p) })
	if e != nil {
		return nil, e
	}
	return v.(fs.FileInfo), nil
}
func (f privateFiles) Stat(p string) (fs.FileInfo, error) {
	v, e := f.invoke("stat", p, nil, func() (any, error) { return f.native.Stat(p) })
	if e != nil {
		return nil, e
	}
	return v.(fs.FileInfo), nil
}
func (f privateFiles) Exists(p string) bool {
	v, e := f.invoke("exists", p, nil, func() (any, error) { return f.native.Exists(p), nil })
	return e == nil && v.(bool)
}
func (f privateFiles) Chmod(p string, m fs.FileMode) error {
	_, e := f.invoke("chmod", p, row{"mode": m}, func() (any, error) { return nil, f.native.Chmod(p, m) })
	return e
}
func (f privateFiles) Append(p string, b []byte, m fs.FileMode) error {
	_, e := f.invoke("append", p, row{"mode": m, "bytes": len(b), "originalLineUTF8": string(b)}, func() (any, error) { return nil, f.native.Append(p, b, m) })
	return e
}
func (f privateFiles) Rename(p, q string) error {
	_, e := f.invoke("rename", p, row{"target": q}, func() (any, error) {
		if e := f.o.check(q); e != nil {
			return nil, e
		}
		return nil, f.native.Rename(p, q)
	})
	return e
}
func (f privateFiles) ReadDir(p string) ([]string, error) {
	v, e := f.invoke("readDir", p, nil, func() (any, error) { return f.native.ReadDir(p) })
	if e != nil {
		return nil, e
	}
	return v.([]string), nil
}
func (f privateFiles) Remove(p string) error {
	_, e := f.invoke("remove", p, nil, func() (any, error) { return nil, f.native.Remove(p) })
	return e
}
func (f privateFiles) guardedRead(p string) ([]byte, error) {
	if e := f.o.check(p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}

func nativeGuard(o *observer, guard string) error {
	files := privateFiles{o: o}
	switch guard {
	case "outside-read":
		_, e := files.guardedRead(filepath.Join(filepath.Dir(o.home), "external-sentinel"))
		return e
	case "external-symlink":
		_, e := files.Lstat(filepath.Join(o.home, "guard-link"))
		return e
	case "offorigin":
		return &capabilityError{"network transport not provided to Activity/Registry helper"}
	case "native-spawn":
		return &capabilityError{"process transport not provided to Activity/Registry helper"}
	default:
		return &capabilityError{"unknown guard"}
	}
}
func settle(s *cooperative.Scope, f *runtimecleanup.Future, e error) error {
	if e != nil {
		return f.Reject(s, e)
	}
	return f.Resolve(s, struct{}{})
}
func main() {
	home := os.Getenv("HOME")
	physical, e := filepath.EvalSymlinks(home)
	if e != nil || physical != home || os.Getenv("TMPDIR") != filepath.Join(home, "tmp") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("TZ") != "UTC" {
		fmt.Fprintln(os.Stderr, "physical owned fixture paths required")
		os.Exit(3)
	}
	allocation := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if allocation == "" || filepath.Dir(home) != allocation || !strings.HasPrefix(filepath.Base(allocation), "cc-haha-activity-go-replay-") {
		fmt.Fprintln(os.Stderr, "owned allocation required")
		os.Exit(3)
	}
	inputBytes, e := os.ReadFile(filepath.Join(home, ".fixture/input.json"))
	if e != nil {
		panic(e)
	}
	var input envelope
	if e = json.Unmarshal(inputBytes, &input); e != nil {
		panic(e)
	}
	var originalEnvelope struct{ Profile json.RawMessage }
	if e = json.Unmarshal(inputBytes, &originalEnvelope); e != nil {
		panic(e)
	}
	o := &observer{home: home, ids: map[any]string{}, trace: []row{}, fsTrace: []row{}, identities: []row{}, unsupported: []string{}}
	result := row{"profile": input.Profile.ID, "guard": input.Guard, "actualPID": os.Getpid(), "home": home, "productionEntered": false, "bodyEnded": false, "failure": nil, "trace": o.trace, "fsTrace": o.fsTrace, "identities": o.identities, "outcome": row{"kind": "not-observed"}, "inputOriginal": originalEnvelope.Profile, "nativeExitPolicy": "Go main returns naturally; no native JS event-loop lifetime claim", "sourceOutputsAsInputs": false}
	exit := 0
	if input.Guard != "" {
		e = nativeGuard(o, input.Guard)
		if e == nil {
			e = errors.New("guard admitted unavailable capability")
		}
		result["failure"] = o.shape(e)
		exit = 2
	} else {
		result["productionEntered"] = true
		owner := cooperative.New()
		registry, e := runtimecleanup.New(owner)
		if e != nil {
			panic(e)
		}
		originalErrors := map[string]error{}
		for _, name := range []string{"now", "serialize", "first", "second"} {
			if message, ok := input.Profile.Errors[name]; ok {
				err := errors.New(message)
				originalErrors[name] = err
				o.identity(err, "original-error-"+name, "Error")
			}
		}
		currentNow := input.Profile.InitialNow
		nowOrdinal, dateOrdinal, serializeOrdinal := 0, 0, 0
		effect := func(xs []string, n int, fallback string) string {
			if n < len(xs) {
				return xs[n]
			}
			return fallback
		}
		readNow := func() (float64, error) {
			x := effect(input.Profile.NowEffects, nowOrdinal, "native")
			nowOrdinal++
			o.mark("Date.now.invoke", row{"primitiveOrdinal": nowOrdinal, "originalEffect": x, "value": currentNow})
			if x != "native" {
				err := originalErrors[x]
				o.mark("Date.now.throw", row{"error": o.shape(err)})
				return 0, err
			}
			v := currentNow
			currentNow += input.Profile.NowStep
			return v, nil
		}
		process, e := diagnostics.NewProcess(diagnostics.Options{Environment: func(name string) string {
			if name == "CLAUDE_CODE_REMOTE_SEND_KEEPALIVES" && input.Profile.KeepaliveEnv != nil {
				return *input.Profile.KeepaliveEnv
			}
			if name == "CLAUDE_CODE_DIAGNOSTICS_FILE" && input.Profile.Enabled {
				return filepath.Join(home, "diagnostics/events")
			}
			return ""
		}, NowMillis: func() int64 { return int64(currentNow) }, ReadNowMillis: readNow, Now: func() time.Time {
			x := effect(input.Profile.DateEffects, dateOrdinal, "valid")
			dateOrdinal++
			v := any(input.Profile.DateMillis)
			millis := int64(input.Profile.DateMillis)
			if x == "invalid" {
				v = "NaN"
				millis = 8640000000000001
			}
			o.mark("new Date", row{"primitiveOrdinal": dateOrdinal, "originalEffect": x, "value": v})
			return time.UnixMilli(millis)
		}, Stringify: func(v protocol.JSONObject) ([]byte, error) {
			x := effect(input.Profile.SerializeEffects, serializeOrdinal, "native")
			serializeOrdinal++
			o.mark("JSON.stringify.invoke", row{"primitiveOrdinal": serializeOrdinal, "originalEffect": x})
			if x != "native" {
				err := originalErrors[x]
				o.mark("JSON.stringify.throw", row{"error": o.shape(err)})
				return nil, err
			}
			b, err := diagnostics.StringifyEntry(v)
			if err != nil {
				o.mark("JSON.stringify.throw", row{"error": o.shape(err)})
			} else {
				o.mark("JSON.stringify.return", row{"resultBytes": len(b)})
			}
			return b, err
		}, PID: os.Getpid(), Platform: "darwin", Files: privateFiles{o: o}})
		if e != nil {
			panic(e)
		}
		tracker, e := sessionactivity.NewCallbackNull(owner, registry, process)
		if e != nil {
			panic(e)
		}
		if input.Profile.Storage == "file-parent" {
			// Original writeFileSync setup omits mode: use the native default
			// creation mode with this actual process's inherited umask.
			if e = os.WriteFile(filepath.Join(home, "diagnostics"), []byte("original private non-directory storage input\n"), 0666); e != nil {
				panic(e)
			}
		}
		externalValue := "initial-original-value"
		released := false
		pending := map[string]*runtimecleanup.Future{}
		var releaseFuture *runtimecleanup.Future
		var releaseFailure error
		callbacks := map[string]*runtimecleanup.Callback{}
		unregisters := map[string]runtimecleanup.Unregister{}
		snapshots := []row{}
		observedFutures := []*runtimecleanup.Future{}
		outcome := row{"kind": "not-observed"}
		var ioDone chan struct{}
		e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
			for _, d := range input.Profile.Callbacks {
				if strings.HasPrefix(d.Kind, "pending") || d.Kind == "never" {
					f, err := ownerasync.NewOwnerFuture[struct{}](owner)
					if err != nil {
						return err
					}
					pending[d.Name] = f
					o.identity(f, "original-external-promise-"+d.Name, "Promise")
					if d.Kind != "never" {
						releaseFuture = f
						if d.Kind == "pendingReject" {
							releaseFailure = originalErrors[d.Error]
						}
					}
				}
			}
			if input.Profile.ExternalControlUTF8 != "" {
				if releaseFuture == nil {
					return &capabilityError{"external control without original pending promise"}
				}
				ioDone = make(chan struct{})
				// A physical stdin reader performs IO only. Its original callback is queued
				// on this same Owner; it does not run Activity/Registry business off-owner.
				go func() {
					defer close(ioDone)
					b, err := io.ReadAll(bufio.NewReader(os.Stdin))
					_, _ = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
						if err != nil {
							return settle(next, releaseFuture, err)
						}
						if string(b) != input.Profile.ExternalControlUTF8 {
							return settle(next, releaseFuture, &capabilityError{"original external stdin transcript differs"})
						}
						released = true
						o.mark("external.stdin.release", row{"originalUTF8": string(b)})
						return settle(next, releaseFuture, releaseFailure)
					})
				}()
			}
			for _, rawDescriptor := range input.Profile.Callbacks {
				d := rawDescriptor
				cb := runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) {
					o.mark("original.cleanupCallback.invoke", row{"name": d.Name, "kind": d.Kind})
					if d.Kind == "syncThrow" {
						failure := originalErrors[d.Error]
						o.mark("original.cleanupCallback.throw", row{"name": d.Name, "error": o.shape(failure)})
						return nil, failure
					}
					wrapped, err := ownerasync.NewOwnerFuture[struct{}](owner)
					if err != nil {
						return nil, err
					}
					if d.Kind == "asyncThrow" {
						return wrapped, settle(next, wrapped, originalErrors[d.Error])
					}
					if d.Kind == "liveGetter" {
						o.mark("original.liveGetter", row{"value": externalValue})
						o.mark("original.cleanupCallback.liveRead", row{"value": externalValue})
					}
					if external := pending[d.Name]; external != nil {
						b, _ := json.Marshal(row{"originalCallbackReady": d.Name})
						_, _ = os.Stdout.Write(append(b, '\n'))
						_, err = external.Then(next, func(later *cooperative.Scope, _ struct{}, failure error) error {
							if failure != nil {
								return settle(later, wrapped, failure)
							}
							o.mark("original.cleanupCallback.afterAwait", row{"name": d.Name, "value": "original external fulfillment"})
							return settle(later, wrapped, nil)
						})
						if err != nil {
							return nil, err
						}
						return wrapped, nil
					}
					return wrapped, settle(next, wrapped, nil)
				})
				callbacks[d.Name] = cb
				o.identity(cb, "original-cleanup-callback-"+d.Name, "function")
			}
			for _, op := range input.Profile.Operations {
				original := row{"call": op.Call}
				if op.Reason != "" {
					original["reason"] = op.Reason
				}
				if op.Name != "" {
					original["name"] = op.Name
				}
				if op.Caller != "" {
					original["caller"] = op.Caller
				}
				if op.Value != nil {
					var value any
					_ = json.Unmarshal(op.Value, &value)
					original["value"] = value
				}
				o.mark("caller.operation.begin", row{"original": original})
				var failure error
				switch op.Call {
				case "start":
					failure = tracker.Start(s, sessionactivity.Reason(op.Reason))
				case "stop":
					failure = tracker.Stop(s, sessionactivity.Reason(op.Reason))
				case "setNow":
					_ = json.Unmarshal(op.Value, &currentNow)
					o.mark("external.clock.set", row{"value": currentNow})
				case "setExternalValue":
					_ = json.Unmarshal(op.Value, &externalValue)
					o.mark("external.value.set", row{"value": externalValue})
				case "register":
					unregisters[op.Name], failure = registry.Register(s, callbacks[op.Name])
				case "unregister":
					var value bool
					value, failure = unregisters[op.Name](s)
					if failure == nil {
						o.mark("original.unregister.return", row{"name": op.Name, "value": value})
					}
				case "registerActivityCallback":
					failure = tracker.RegisterCallback(s, func(*cooperative.Scope) error { o.mark("original.activityCallback.invoke", nil); return nil })
				case "unregisterActivityCallback":
					failure = tracker.UnregisterCallback(s)
				case "sendSignal":
					failure = tracker.SendSignal(s)
				case "cleanup":
					o.mark("caller.beforeCleanupInvoke", nil)
					returned, err := registry.Start(s)
					if err != nil {
						failure = err
						break
					}
					id := o.identity(returned, fmt.Sprintf("actual-cleanup-promise-%d", len(snapshots)+1), "Promise")
					o.mark("caller.afterCleanupInvoke", row{"identity": id, "nativePromise": false, "actualOwnerFuture": true})
					operations := []string{}
					for _, r := range o.trace {
						operations = append(operations, r["operation"].(string))
					}
					snapshots = append(snapshots, row{"afterInvokeOrdinal": len(o.trace), "operationsAtReturn": operations, "identity": id})
					observed, err := returned.Then(s, func(later *cooperative.Scope, _ struct{}, err error) error {
						if err != nil {
							o.mark("observer.cleanup.Then.rejected", row{"promiseIdentity": id, "error": o.shape(err)})
							outcome = row{"kind": "rejected", "promiseIdentity": id, "error": o.shape(err)}
						} else {
							o.mark("observer.cleanup.Then.fulfilled", row{"promiseIdentity": id})
							outcome = row{"kind": "fulfilled", "promiseIdentity": id}
						}
						return nil
					})
					if err != nil {
						failure = err
						break
					}
					observedFutures = append(observedFutures, observed)
					observedID := o.identity(observed, "", "Promise")
					o.mark("caller.afterCleanupThenRegistered", row{"helperIdentity": id, "observerIdentity": observedID, "samePromise": observed == returned})
					if op.Caller == "await" {
						_, err = returned.Await(s)
						if err != nil {
							o.mark("caller.cleanup.await.rejected", row{"promiseIdentity": id, "error": o.shape(err)})
						} else {
							o.mark("caller.cleanup.await.fulfilled", row{"promiseIdentity": id})
						}
					}
				default:
					failure = o.unsupportedError("unknown original operation " + op.Call)
				}
				if failure != nil {
					o.mark("caller.operation.throw", row{"original": original, "error": o.shape(failure)})
					var transport *sessionactivity.CapabilityError
					if errors.As(failure, &transport) {
						o.unsupported = append(o.unsupported, transport.Boundary)
						return failure
					}
					var cap *capabilityError
					if errors.As(failure, &cap) {
						return failure
					}
				} else {
					o.mark("caller.operation.return", row{"original": original})
				}
			}
			o.mark("caller.bodyEnd", nil)
			result["bodyEnded"] = true
			return nil
		})
		// Close joins only the existing Owner lifetime. No cleanup Promise is
		// awaited here; queued reactions may be canceled, which is recorded as a
		// native-exit parity gap for observer-only Source callers.
		owner.Close()
		// Observer-only profiles are recorded after administrative Close, without
		// an extra await/drain for observer promises. If not delivered, keep pending.
		facts := []row{}
		for _, f := range observedFutures {
			settled := false
			select {
			case <-f.Done():
				settled = true
			default:
			}
			facts = append(facts, row{"identity": o.ids[f], "settledAtAdministrativeOwnerClose": settled})
		}
		result["outcome"] = outcome
		result["promiseFacts"] = row{"cleanupSnapshots": snapshots, "externalReleaseReceived": released, "observerSettlementsAtAdministrativeOwnerClose": facts}
		result["primitiveCalls"] = row{"now": nowOrdinal, "date": dateOrdinal, "serialize": serializeOrdinal}
		if e != nil {
			result["failure"] = o.shape(e)
			exit = 2
		}
		// Closing the Owner is administrative lifetime cleanup, never a business join.
		// It does not force fulfillment/drain of pending registry callbacks.
		_ = ioDone // physical stdin has been read for awaited original controls.
	}
	result["trace"] = o.trace
	result["fsTrace"] = o.fsTrace
	result["identities"] = o.identities
	result["unsupported"] = o.unsupported
	result["exitCode"] = exit
	result["nativeGoTypesObserved"] = reflect.TypeOf(o).String()
	b, e := json.Marshal(result)
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile(filepath.Join(home, ".fixture/result.json"), b, 0600); e != nil {
		panic(e)
	}
	sum := sha256.Sum256(b)
	receipt, _ := json.Marshal(row{"actualPID": os.Getpid(), "bytes": len(b), "sha256": fmt.Sprintf("%x", sum), "resultRelative": ".fixture/result.json"})
	_, _ = os.Stdout.Write(append(receipt, '\n'))
	if exit != 0 {
		os.Exit(exit)
	}
}
