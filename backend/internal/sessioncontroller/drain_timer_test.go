package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// These timer primitive tests assert the real ledger/owner boundary; they do
// not count an injected readiness callback as new Source-positive evidence.
func TestFileHistoryScheduledDrainCancelReleasesOnlyTimer(t *testing.T) {
	owner, writer, pending, _ := uuidHarness(t, "", nil, false)
	var fire func()
	var stop func()
	stops := 0
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		writer.options.Schedule = func(delay time.Duration, cb func()) func() {
			if delay != 100*time.Millisecond {
				t.Error(delay)
			}
			fire = cb
			return func() { stops++ }
		}
		var e error
		stop, e = pending.scheduleDrainReference(s)
		return e
	})
	if n, _ := pending.runtimeNative.snapshot(); n != 1 {
		t.Fatal("scheduled timer absent", n)
	}
	stop()
	stop()
	fire()
	if n, _ := pending.runtimeNative.snapshot(); n != 0 || stops != 1 {
		t.Fatal("cancel did not release exactly once", n, stops)
	}
	if _, e := pending.scheduleDrainReference(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
}
func TestFileHistoryScheduledReadinessRetainsReferenceUntilOwnerEntry(t *testing.T) {
	owner, writer, pending, _ := uuidHarness(t, "", nil, false)
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var fire, stop func()
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(s *cooperative.Scope) error {
			writer.options.Schedule = func(_ time.Duration, cb func()) func() { fire = cb; return func() {} }
			var e error
			stop, e = pending.scheduleDrainReference(s)
			close(held)
			<-release
			return e
		})
	}()
	<-held
	fire()
	stop()
	if n, _ := pending.runtimeNative.snapshot(); n != 1 {
		t.Fatal("ready timer released before owner entry", n)
	}
	once.Do(func() { close(release) })
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := pending.WaitRuntimeNative(context.Background()); e != nil {
		t.Fatal(e)
	}
	if n, _ := pending.runtimeNative.snapshot(); n != 0 {
		t.Fatal(n)
	}
}
func TestFileHistoryScheduledTimerOwnerCloseSettlesAdministrativeReference(t *testing.T) {
	owner, writer, pending, _ := uuidHarness(t, "", nil, false)
	var fire func()
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		writer.options.Schedule = func(_ time.Duration, cb func()) func() { fire = cb; return func() {} }
		_, e := pending.scheduleDrainReference(s)
		return e
	})
	owner.Close()
	fire()
	if n, _ := pending.runtimeNative.snapshot(); n != 0 {
		t.Fatal("rejected timer delivery leaked", n)
	}
}
func TestFileHistoryNaturalExitCheckpointWaitsForRealScheduledDrain(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, "", nil, false)
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		writer.path = writer.target()
		writer.options.Schedule = RealSchedule
		snapshot := plainMessage("timer-snapshot", "original payload")
		sink, e := NewFileHistorySink(pending, loader)
		if e != nil {
			return e
		}
		f, e := sink.BeginFileHistorySnapshot(s, snapshot.Get("uuid"), snapshot, false)
		if e != nil {
			return e
		}
		_, e = f.Await(s)
		return e
	})
	// No Flush/Close/outer Record join: only the true physical referenced timer.
	if n, _ := pending.runtimeNative.snapshot(); n == 0 {
		t.Fatal("actual 100ms timer absent from native liveness")
	}
	if e := pending.WaitRuntimeNative(context.Background()); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(writer.target())
	if e != nil || !strings.Contains(string(b), `"type":"file-history-snapshot"`) {
		t.Fatalf("natural drain absent %s %v", b, e)
	}
}
