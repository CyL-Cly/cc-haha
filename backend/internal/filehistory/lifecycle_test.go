package filehistory_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

type gatedCapture struct {
	sessionrewind.NativeFiles
	started, release, physicalDone chan struct{}
	once                           sync.Once
	calls                          atomic.Int32
}

func (f *gatedCapture) CaptureBackup(ctx context.Context, id, base, path string, version int, suffix string, uuid func() (string, error)) (checkpointfiles.CapturedBackup, error) {
	f.calls.Add(1)
	f.once.Do(func() { close(f.started) })
	<-f.release
	result, e := f.NativeFiles.CaptureBackup(ctx, id, base, path, version, suffix, uuid)
	close(f.physicalDone)
	return result, e
}

type lifetimeFixture struct {
	owner              *cooperative.Owner
	refs               ownerasync.NativeReferences
	binding            *ownerasync.NativeBinding
	sink               *stateSink
	state              protocol.JSONObject
	service            *filehistory.Service
	work, config, path string
	notices            []error
}

func newLifetimeFixture(t *testing.T, files func(sessionrewind.NativeFiles) filehistory.Files) *lifetimeFixture {
	t.Helper()
	f := &lifetimeFixture{owner: cooperative.New(), state: emptyState(), work: t.TempDir(), config: filepath.Join(t.TempDir(), "config")}
	f.path = filepath.Join(f.work, "a.txt")
	if e := os.WriteFile(f.path, []byte("real before"), 0600); e != nil {
		t.Fatal(e)
	}
	f.binding, _ = f.refs.Bind(f.owner)
	f.sink = &stateSink{owner: f.owner, binding: f.binding, records: []recordedSnapshot{}}
	native := sessionrewind.NativeFiles{ConfigHome: f.config}
	var impl filehistory.Files = native
	if files != nil {
		impl = files(native)
	}
	var e error
	f.service, e = filehistory.New(filehistory.Options{Owner: f.owner, Binding: f.binding, Sink: f.sink, Files: impl, ConfigHome: f.config, OriginalCWD: f.work, SessionID: func(scope *cooperative.Scope) (string, error) {
		if !scope.BelongsTo(f.owner) {
			return "", cooperative.ErrRestricted
		}
		return "physical-lifetime-session", nil
	}, Enabled: func(*cooperative.Scope) (bool, error) { return true, nil }, GetState: func(scope *cooperative.Scope) (protocol.JSONObject, error) {
		if !scope.BelongsTo(f.owner) {
			return protocol.JSONObject{}, cooperative.ErrRestricted
		}
		return f.state.Clone(), nil
	}, SetState: func(scope *cooperative.Scope, value protocol.JSONObject) error {
		return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { f.state = value.Clone(); return nil })
	}, Now: func() time.Time { return time.UnixMilli(1700000000000) }, UUID: func() (string, error) { return "native-lifetime-uuid", nil }, OnError: func(e error) { f.notices = append(f.notices, e) }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(f.owner.Close)
	return f
}
func waitNativeZero(t *testing.T, refs *ownerasync.NativeReferences) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, ch := refs.Snapshot()
		if n == 0 {
			return
		}
		select {
		case <-ch:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	n, _ := refs.Snapshot()
	t.Fatalf("physical native references leaked: %d", n)
}

func TestOwnedCaptureCancellationRetainsPhysicalJobAndRejectsForeignOwners(t *testing.T) {
	for _, mode := range []string{"track-cancel", "track-owner-close", "complete-cancel"} {
		t.Run(mode, func(t *testing.T) {
			started, release, physicalDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var files *gatedCapture
			f := newLifetimeFixture(t, func(native sessionrewind.NativeFiles) filehistory.Files {
				files = &gatedCapture{NativeFiles: native, started: started, release: release, physicalDone: physicalDone}
				return files
			})
			if e := f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { return f.service.Make(scope, raw("user-before")) }); e != nil {
				t.Fatal(e)
			}
			if mode == "complete-cancel" {
				f.state = object([]byte(`{"snapshots":[{"schemaVersion":2,"messageId":"user-before","trackedFileBackups":{"a.txt":{"backupFileName":null,"version":1,"backupTime":"2023-11-14T22:13:20.000Z"}}}],"trackedFiles":["a.txt"],"snapshotSequence":1}`))
			}
			foreign := cooperative.New()
			defer foreign.Close()
			if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
				for _, run := range []func() error{func() error { return f.service.Make(scope, raw("foreign")) }, func() error { return f.service.Track(scope, f.path, raw("foreign")) }, func() error { return f.service.Complete(scope, raw("foreign")) }} {
					if e := run(); !errors.Is(e, cooperative.ErrRestricted) {
						t.Errorf("foreign accepted: %v", e)
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if files.calls.Load() != 0 {
				t.Fatal("foreign owner dispatched actual IO")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- f.owner.Run(ctx, func(scope *cooperative.Scope) error {
					if mode == "complete-cancel" {
						return f.service.Complete(scope, raw("user-before"))
					}
					return f.service.Track(scope, f.path, raw("parent-assistant"))
				})
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("physical capture not started")
			}
			if count, _ := f.refs.Snapshot(); count != 1 {
				t.Fatalf("physical job not owned: %d", count)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled observer still blocked")
			}
			if mode == "track-owner-close" {
				f.owner.Close()
			}
			if count, _ := f.refs.Snapshot(); count != 1 {
				t.Fatalf("cancel/close reclaimed unfinished native copy: %d", count)
			}
			if len(f.sink.records) != 1 {
				t.Fatal("cancel invented record before actual completion", len(f.sink.records))
			}
			close(release)
			select {
			case <-physicalDone:
			case <-time.After(5 * time.Second):
				t.Fatal("physical capture did not finish")
			}
			if mode != "track-owner-close" {
				if e := f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { return scope.AwaitFulfilled() }); e != nil {
					t.Fatal(e)
				}
			}
			waitNativeZero(t, &f.refs)
			filesOnDisk, e := os.ReadDir(filepath.Join(f.config, "file-history", "physical-lifetime-session"))
			if e != nil || len(filesOnDisk) != 1 {
				t.Fatal("cancel lost admitted physical result", e, filesOnDisk)
			}
		})
	}
}

func TestNewRejectsContradictorySinkAndNativeLedger(t *testing.T) {
	f := newLifetimeFixture(t, nil)
	foreign := cooperative.New()
	defer foreign.Close()
	var otherRefs ownerasync.NativeReferences
	other, _ := otherRefs.Bind(foreign)
	valid := filehistory.Options{Owner: f.owner, Binding: f.binding, Sink: f.sink, Files: sessionrewind.NativeFiles{ConfigHome: f.config}, ConfigHome: f.config, OriginalCWD: f.work, SessionID: func(*cooperative.Scope) (string, error) { return "s", nil }, Enabled: func(*cooperative.Scope) (bool, error) { return true, nil }, GetState: func(*cooperative.Scope) (protocol.JSONObject, error) { return f.state.Clone(), nil }, SetState: func(*cooperative.Scope, protocol.JSONObject) error { return nil }, Now: time.Now, UUID: func() (string, error) { return "uuid", nil }, OnError: func(error) {}}
	cases := []filehistory.Options{valid, valid, valid}
	cases[0].Binding = other
	cases[1].Sink = &stateSink{owner: foreign, binding: other}
	cases[2].Binding = nil
	for i, o := range cases {
		if _, e := filehistory.New(o); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatalf("case%d contradictory producer accepted %v", i, e)
		}
	}
}

type delayedSink struct {
	stateSink
	gate, started chan struct{}
	startOnce     sync.Once
}

func (s *delayedSink) BeginFileHistorySnapshot(scope *cooperative.Scope, id json.RawMessage, snapshot *protocol.JSONObject, update bool) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	s.records = append(s.records, recordedSnapshot{append(json.RawMessage(nil), id...), snapshot.Clone(), update})
	future, e := ownerasync.NewOwnerFuture[struct{}](s.owner)
	if e != nil {
		return nil, e
	}
	if s.gate == nil {
		return future, future.Resolve(scope, struct{}{})
	}
	ref, e := s.binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	gate := s.gate
	s.startOnce.Do(func() { close(s.started) })
	go func() {
		<-gate
		receipt, e := s.owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			return future.Resolve(next, struct{}{})
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}

func TestOriginalRecordMakeAndTrackAreDetachedCompleteAwaitsAndRunsOnce(t *testing.T) {
	for _, mode := range []string{"make", "track", "complete"} {
		t.Run(mode, func(t *testing.T) {
			f := newLifetimeFixture(t, nil)
			sink := &delayedSink{stateSink: stateSink{owner: f.owner, binding: f.binding, records: []recordedSnapshot{}}, started: make(chan struct{})}
			options := filehistory.Options{Owner: f.owner, Binding: f.binding, Sink: sink, Files: sessionrewind.NativeFiles{ConfigHome: f.config}, ConfigHome: f.config, OriginalCWD: f.work, SessionID: func(*cooperative.Scope) (string, error) { return "delay-sink-session", nil }, Enabled: func(*cooperative.Scope) (bool, error) { return true, nil }, GetState: func(*cooperative.Scope) (protocol.JSONObject, error) { return f.state.Clone(), nil }, SetState: func(scope *cooperative.Scope, v protocol.JSONObject) error {
				return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { f.state = v.Clone(); return nil })
			}, Now: func() time.Time { return time.UnixMilli(1700000000000) }, UUID: func() (string, error) { return "delay-uuid", nil }, OnError: func(e error) { t.Error(e) }}
			service, e := filehistory.New(options)
			if e != nil {
				t.Fatal(e)
			}
			if mode != "make" {
				if e = f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { return service.Make(scope, raw("delay-user")) }); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "complete" {
				if e = f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { return service.Track(scope, f.path, raw("parent-tool")) }); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(f.path, []byte("real after"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			initialRecords := len(sink.records)
			sink.gate = make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- f.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					switch mode {
					case "make":
						return service.Make(scope, raw("delay-user"))
					case "track":
						return service.Track(scope, f.path, raw("parent-tool"))
					default:
						return service.Complete(scope, raw("delay-user"))
					}
				})
			}()
			select {
			case <-sink.started:
			case <-time.After(5 * time.Second):
				t.Fatal("actual sink call not started")
			}
			if mode == "complete" {
				select {
				case e := <-done:
					t.Fatal("complete returned before persistence", e)
				default:
				}
				receipt, e := f.owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { return service.Complete(scope, raw("delay-user")) })
				if e == nil {
					e = receipt.Wait()
				}
				if e != nil {
					t.Fatal(e)
				}
				if len(sink.records) != initialRecords+1 {
					t.Fatal("completion recaptured/re-recorded while first persistence pending")
				}
			} else {
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Source detached Record incorrectly awaited")
				}
			}
			if count, _ := f.refs.Snapshot(); count != 1 {
				t.Fatalf("pending original sink write not owned: %d", count)
			}
			close(sink.gate)
			if mode == "complete" {
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("complete did not observe actual sink settlement")
				}
			}
			if e = f.owner.Run(context.Background(), func(scope *cooperative.Scope) error { return scope.AwaitFulfilled() }); e != nil {
				t.Fatal(e)
			}
			waitNativeZero(t, &f.refs)
		})
	}
}
