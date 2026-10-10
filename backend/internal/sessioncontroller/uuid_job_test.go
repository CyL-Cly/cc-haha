package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type uuidIO struct {
	mu                sync.Mutex
	kind, fault       string
	entered, released chan struct{}
	once              sync.Once
	counts            map[string]int
}

func (io *uuidIO) complete(kind string) error {
	io.mu.Lock()
	io.counts[kind]++
	io.mu.Unlock()
	if kind == io.kind && io.entered != nil {
		io.once.Do(func() { close(io.entered); <-io.released })
	}
	if kind == io.fault {
		return os.ErrPermission
	}
	return nil
}
func (io *uuidIO) Stat(path string) (int64, error) {
	s, e := os.Stat(path)
	if e != nil {
		return 0, e
	}
	if e = io.complete("stat"); e != nil {
		return 0, e
	}
	return s.Size(), nil
}
func (io *uuidIO) ReadFile(path string) ([]byte, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if e = io.complete("readFile"); e != nil {
		return nil, e
	}
	return b, nil
}
func (io *uuidIO) Open(path string) (sessionstore.File, error) {
	f, e := (sessionstore.OSFileSystem{}).Open(path)
	if e != nil {
		return nil, e
	}
	if e = io.complete("open"); e != nil {
		_ = f.Close()
		return nil, e
	}
	return &uuidFile{File: f, io: io}, nil
}

type uuidFile struct {
	sessionstore.File
	io *uuidIO
}

func (file *uuidFile) ReadAt(b []byte, offset int64) (int, error) {
	n, e := file.File.ReadAt(b, offset)
	if fail := file.io.complete("readAt"); fail != nil {
		return n, fail
	}
	return n, e
}
func (file *uuidFile) Close() error {
	e := file.File.Close()
	if fail := file.io.complete("close"); fail != nil {
		return fail
	}
	return e
}

func uuidHarness(t *testing.T, content string, io *uuidIO, disabled bool, sids ...string) (*cooperative.Owner, *Writer, *PendingWriter, *sessionhistory.Loader) {
	t.Helper()
	owner := cooperative.New()
	writer := actualPendingWriter(t, owner, t.TempDir(), OSFiles{})
	var pending *PendingWriter
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		var err error
		pending, err = NewPendingWriter(writer, owner)
		return err
	})
	if len(sids) > 0 {
		pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.Switch(sids[0], ""); return nil })
	}
	target := writer.target()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if io == nil {
		io = &uuidIO{}
	}
	io.counts = map[string]int{}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: io, Stat: io.Stat, ReadFile: io.ReadFile, DisablePrecompactSkip: func() bool { return disabled }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pending.Close(context.Background()); owner.Close() })
	return owner, writer, pending, loader
}
func uuidBegin(t *testing.T, owner *cooperative.Owner, pending *PendingWriter, loader *sessionhistory.Loader, sid string) *SessionUUIDFuture {
	t.Helper()
	var f *SessionUUIDFuture
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { var e error; f, e = pending.SessionUUIDs(s, loader, sid); return e })
	return f
}

const uuidOld = `{"parentUuid":null,"type":"user","message":{"role":"user","content":"old"},"uuid":"original-old","timestamp":"2023-11-14T22:13:20.000Z"}` + "\n"

func TestUUIDPhysicalWaitIsUntrackedAndOwnerProgresses(t *testing.T) {
	for _, kind := range []string{"stat", "readFile", "open", "readAt", "close"} {
		t.Run(kind, func(t *testing.T) {
			content := uuidOld
			if kind == "open" || kind == "readAt" || kind == "close" {
				content = strings.Repeat(" ", sessionhistory.SkipPrecompactThreshold+1) + "\n" + uuidOld
			}
			io := &uuidIO{kind: kind, entered: make(chan struct{}), released: make(chan struct{})}
			owner, writer, pending, loader := uuidHarness(t, content, io, false)
			// The original synchronous Writer does not independently load old UUIDs.
			if found, _ := writer.Has(json.RawMessage(`"original-old"`)); found {
				t.Fatal("old synchronous membership unexpectedly hydrated")
			}
			future := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
			awaitSignal(t, io.entered, "private physical "+kind)
			same := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
			if same != future {
				t.Fatal("cold loads did not share promise")
			}
			flush := beginPending(t, owner, pending)
			if err := flush.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-future.Done():
				t.Fatal("unreleased physical cache settled")
			default:
			}
			admitted := false
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { admitted = true; return nil })
			if !admitted {
				t.Fatal("owner input blocked")
			}
			close(io.released)
			set, err := future.Wait(context.Background())
			if err != nil || !set.Has(json.RawMessage(`"original-old"`)) {
				t.Fatalf("load %v/%v", set, err)
			}
			if hot := uuidBegin(t, owner, pending, loader, writer.options.SessionID); hot != future {
				t.Fatal("hot promise changed")
			}
			if found, _ := writer.Has(json.RawMessage(`"original-old"`)); found {
				t.Fatal("cache illegally became second writer membership source")
			}
		})
	}
}
func TestUUIDCacheClearAndCancellationDoNotCancelFS(t *testing.T) {
	io := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
	owner, writer, pending, loader := uuidHarness(t, uuidOld, io, false)
	first := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	awaitSignal(t, io.entered, "cache read")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := first.Wait(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { return pending.ClearSessionUUIDCache(s) })
	second := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	if second == first {
		t.Fatal("clear did not remove cache promise")
	}
	close(io.released)
	for _, f := range []*SessionUUIDFuture{first, second} {
		set, e := f.Wait(context.Background())
		if e != nil || set.Len() != 1 {
			t.Fatal(set, e)
		}
	}
	if _, e := first.Wait(ctx); e != nil {
		t.Fatal("settled promise lost cached result", e)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		set, e := second.Await(s)
		if e == nil && set.Len() != 1 {
			return ioUnexpected()
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func ioUnexpected() error { return io.ErrUnexpectedEOF }
func TestUUIDPhysicalFailuresAreSwallowedAndMemoized(t *testing.T) {
	for _, kind := range []string{"stat", "readFile", "open", "readAt", "close", "missing"} {
		t.Run(kind, func(t *testing.T) {
			content := uuidOld
			if kind == "open" || kind == "readAt" || kind == "close" {
				content = strings.Repeat(" ", sessionhistory.SkipPrecompactThreshold+1) + "\n" + uuidOld
			}
			io := &uuidIO{fault: kind}
			owner, writer, pending, loader := uuidHarness(t, content, io, false)
			if kind == "missing" {
				if e := os.Remove(writer.target()); e != nil {
					t.Fatal(e)
				}
			}
			f := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
			set, e := f.Wait(context.Background())
			if e != nil || set.Len() != 0 {
				t.Fatal(set, e)
			}
			if hot := uuidBegin(t, owner, pending, loader, writer.options.SessionID); hot != f {
				t.Fatal("failure result not memoized")
			}
		})
	}
}
func TestUUIDOwnerAdmissionAndClosedGuards(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, true)
	if _, e := pending.SessionUUIDs(nil, loader, "sid"); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	other := cooperative.New()
	defer other.Close()
	r, e := other.Submit(context.Background(), cooperative.Read, func(s *cooperative.Scope) error {
		_, e := pending.SessionUUIDs(s, loader, "sid")
		if !errors.Is(e, cooperative.ErrRestricted) {
			return ioUnexpected()
		}
		return pending.ClearSessionUUIDCache(s)
	})
	if e != nil {
		t.Fatal(e)
	}
	if !errors.Is(r.Wait(), cooperative.ErrRestricted) {
		t.Fatal("foreign cache clear admitted")
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		for _, sid := range []string{"", "a/b", "a\\b", string([]byte{0xff})} {
			if _, e := pending.SessionUUIDs(s, loader, sid); e == nil {
				return ioUnexpected()
			}
		}
		if _, e := pending.SessionUUIDs(s, nil, "sid"); e == nil {
			return ioUnexpected()
		}
		return nil
	})
	f := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	set, e := f.Wait(context.Background())
	if e != nil || set.Len() != 1 {
		t.Fatal(set, e)
	}
	if _, e := f.Wait(nil); e == nil {
		t.Fatal("nil context admitted")
	}
	if _, e := f.Await(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	_, _, _, otherLoader := uuidHarness(t, uuidOld, nil, false)
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		if _, e := pending.SessionUUIDs(s, otherLoader, "other"); e == nil {
			return ioUnexpected()
		}
		return nil
	})
	if e := pending.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		if _, e := pending.SessionUUIDs(s, loader, "sid"); !errors.Is(e, ErrPendingClosed) {
			return ioUnexpected()
		}
		if e := pending.ClearSessionUUIDCache(s); !errors.Is(e, ErrPendingClosed) {
			return ioUnexpected()
		}
		return nil
	})
}

func TestUUIDCloseJoinsAdoptedDescriptor(t *testing.T) {
	io := &uuidIO{kind: "close", entered: make(chan struct{}), released: make(chan struct{})}
	owner, writer, pending, loader := uuidHarness(t, strings.Repeat(" ", sessionhistory.SkipPrecompactThreshold+1)+"\n"+uuidOld, io, false)
	f := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
	awaitSignal(t, io.entered, "real close")
	done := make(chan error, 1)
	go func() { done <- pending.Close(context.Background()) }()
	select {
	case <-done:
		t.Fatal("Close escaped live job")
	case <-time.After(10 * time.Millisecond):
	}
	close(io.released)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	set, e := f.Wait(context.Background())
	if e != nil || set.Len() != 1 {
		t.Fatal(set, e)
	}
}
