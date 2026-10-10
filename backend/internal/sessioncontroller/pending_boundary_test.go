package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"io"
	"io/fs"
	"os"
	"sync"
	"testing"
	"time"
)

type pendingFaultFiles struct {
	OSFiles
	kind                string
	mu                  sync.Mutex
	natural             bool
	closes              int
	holdAfterOpen, held chan struct{}
}

func (files *pendingFaultFiles) OpenFile(path string, flag int, mode fs.FileMode) (TranscriptFile, error) {
	files.mu.Lock()
	natural := files.natural && flag&os.O_CREATE == 0
	kind := files.kind
	files.mu.Unlock()
	if natural {
		if kind == "missing" {
			return nil, fs.ErrNotExist
		}
		if kind == "open" {
			return nil, fs.ErrPermission
		}
	}
	file, err := files.OSFiles.OpenFile(path, flag, mode)
	if err != nil {
		return nil, err
	}
	wrapped := &pendingFaultFile{TranscriptFile: file, files: files, kind: kind, natural: natural}
	if natural && files.holdAfterOpen != nil {
		close(files.held)
		<-files.holdAfterOpen
	}
	return wrapped, nil
}

type pendingFaultFile struct {
	TranscriptFile
	files   *pendingFaultFiles
	kind    string
	natural bool
}
type pendingInfo struct {
	fs.FileInfo
	mode       fs.FileMode
	noIdentity bool
}

func (info pendingInfo) Mode() fs.FileMode {
	if info.mode != 0 {
		return info.mode
	}
	return info.FileInfo.Mode()
}
func (info pendingInfo) Sys() any {
	if info.noIdentity {
		return nil
	}
	return info.FileInfo.Sys()
}
func (file *pendingFaultFile) Stat() (fs.FileInfo, error) {
	if file.natural && file.kind == "stat" {
		return nil, fs.ErrPermission
	}
	info, err := file.TranscriptFile.Stat()
	if err != nil {
		return nil, err
	}
	if file.natural && file.kind == "directory" {
		return pendingInfo{FileInfo: info, mode: fs.ModeDir}, nil
	}
	if file.natural && file.kind == "identity" {
		return pendingInfo{FileInfo: info, noIdentity: true}, nil
	}
	return info, nil
}
func (file *pendingFaultFile) Write(data []byte) (int, error) {
	if file.natural && (file.kind == "write" || file.kind == "write-close") {
		return 0, io.ErrClosedPipe
	}
	if file.natural && file.kind == "short" {
		return 0, nil
	}
	return file.TranscriptFile.Write(data)
}
func (file *pendingFaultFile) Close() error {
	err := file.TranscriptFile.Close()
	if file.natural {
		file.files.mu.Lock()
		file.files.closes++
		file.files.mu.Unlock()
		if file.kind == "close" || file.kind == "write-close" {
			return fs.ErrInvalid
		}
	}
	return err
}
func (files *pendingFaultFiles) arm(kind string) {
	files.mu.Lock()
	files.natural = true
	files.kind = kind
	files.mu.Unlock()
}
func (files *pendingFaultFiles) count() int {
	files.mu.Lock()
	defer files.mu.Unlock()
	return files.closes
}
func makePending(t *testing.T, files TranscriptFiles) (*cooperative.Owner, *Writer, *PendingWriter) {
	t.Helper()
	owner := cooperative.New()
	writer := actualPendingWriter(t, owner, t.TempDir(), files)
	var pending *PendingWriter
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		writer.options.Schedule = func(time.Duration, func()) func() { return func() {} }
		_, err := writer.Record([]*protocol.JSONObject{plainMessage("native-first", "original native first")}, nil)
		if err != nil {
			return err
		}
		pending, err = NewPendingWriter(writer, owner)
		return err
	})
	t.Cleanup(func() { _ = pending.Close(context.Background()); owner.Close() })
	return owner, writer, pending
}
func beginPending(t *testing.T, owner *cooperative.Owner, pending *PendingWriter) *WriteFuture {
	t.Helper()
	var future *WriteFuture
	pendingCall(t, owner, cooperative.Flush, func(scope *cooperative.Scope) error { var err error; future, err = pending.Begin(scope); return err })
	return future
}
func TestPendingNativeOperationFaultsAndLeaseSettlement(t *testing.T) {
	for _, kind := range []string{"missing", "open", "stat", "directory", "identity", "write", "short", "close", "write-close", "encode"} {
		t.Run(kind, func(t *testing.T) {
			files := &pendingFaultFiles{}
			owner, writer, pending := makePending(t, files)
			files.arm(kind)
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				if kind == "encode" {
					writer.queues[writer.path][0].entry.Set("invalid", json.RawMessage("{"))
				}
				return nil
			})
			err := beginPending(t, owner, pending).Wait(context.Background())
			if kind == "missing" {
				if err != nil {
					t.Fatal(err)
				}
				pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
					if !writer.excluded[`"native-first"`] {
						return errors.New("missing file did not exclude original UUID")
					}
					return nil
				})
				return
			}
			if err == nil {
				t.Fatal("native failure swallowed")
			}
			if kind == "write-close" && !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("finally close did not replace append failure: %v", err)
			}
			if kind != "open" && files.count() != 1 {
				t.Fatalf("lease close count=%d", files.count())
			}
			disk, _ := os.ReadFile(writer.target())
			if kind != "close" && len(disk) != 0 {
				t.Fatalf("failed batch modified disk: %q", disk)
			}
		})
	}
}
func TestPendingLateEnqueueAndCheckpointCloseJoin(t *testing.T) {
	files := &heldFiles{mode: "open", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{})}
	owner, writer, pending := makePending(t, files)
	close(files.arm)
	first := beginPending(t, owner, pending)
	awaitSignal(t, files.held, "original first batch")
	var second *WriteFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := writer.Append(plainMessage("native-late", "late original")); err != nil {
			return err
		}
		var err error
		second, err = pending.Begin(scope)
		if second != first {
			return errors.New("checkpoint detached from active drain")
		}
		return err
	})
	canceled, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("model canceled independently")
	cancel(cause)
	if err := first.Wait(canceled); !errors.Is(err, cause) {
		t.Fatalf("wait cancellation=%v", err)
	}
	closing := make(chan error, 1)
	go func() { closing <- pending.Close(context.Background()) }()
	observedClosing := false
	for i := 0; i < 100 && !observedClosing; i++ {
		pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { observedClosing = pending.closing; return nil })
		if !observedClosing {
			time.Sleep(time.Millisecond)
		}
	}
	if !observedClosing {
		t.Fatal("close not admitted")
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if err := writer.Append(plainMessage("refused", "must not write")); !errors.Is(err, ErrPendingClosed) {
			return errors.New("append after close admitted")
		}
		_, err := writer.Record([]*protocol.JSONObject{plainMessage("refused-record", "must not write")}, nil)
		if !errors.Is(err, ErrPendingClosed) {
			return errors.New("record after close admitted")
		}
		return nil
	})
	select {
	case err := <-closing:
		t.Fatalf("Close joined before held native IO: %v", err)
	default:
	}
	close(files.release)
	select {
	case err := <-closing:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join")
	}
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pending.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(writer.target())
	if !bytes.Contains(disk, []byte("native-first")) || !bytes.Contains(disk, []byte("native-late")) || bytes.Contains(disk, []byte("refused")) {
		t.Fatalf("late original bytes=%q", disk)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		_, err := pending.Begin(scope)
		if !errors.Is(err, ErrPendingClosed) {
			return errors.New("closed Begin allowed")
		}
		return nil
	})
	pending.postDrain() // A timer already queued at Close must return without IO.
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { return nil })
}
func TestPendingPolicyChangesAndReplacedNativeTarget(t *testing.T) {
	for _, mode := range []string{"skip-before", "skip-after-open", "missing-empty"} {
		t.Run(mode, func(t *testing.T) {
			files := &heldFiles{mode: "stat", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{})}
			owner, writer, pending := makePending(t, files)
			if mode == "skip-before" {
				pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.options.SessionPersistenceDisabled = true; return nil })
				if err := beginPending(t, owner, pending).Wait(context.Background()); err != nil {
					t.Fatal(err)
				}
				return
			}
			if mode == "missing-empty" {
				if err := os.Remove(writer.target()); err != nil {
					t.Fatal(err)
				}
				if err := beginPending(t, owner, pending).Wait(context.Background()); err != nil {
					t.Fatal(err)
				}
				return
			}
			close(files.arm)
			future := beginPending(t, owner, pending)
			awaitSignal(t, files.held, "stat pending")
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				writer.options.SessionPersistenceDisabled = true
				return nil
			})
			close(files.release)
			if err := future.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			disk, _ := os.ReadFile(writer.target())
			if mode == "skip-after-open" && len(disk) != 0 {
				t.Fatal("policy was not rechecked after stat")
			}
		})
	}
	files := &heldFiles{mode: "open", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{})}
	owner, writer, pending := makePending(t, files)
	close(files.arm)
	future := beginPending(t, owner, pending)
	awaitSignal(t, files.held, "replacement before open")
	original := writer.target()
	if err := os.Rename(original, original+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("replacement sentinel\n"), 0600); err != nil {
		t.Fatal(err)
	}
	close(files.release)
	if err := future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(original)
	if string(disk) != "replacement sentinel\n" {
		t.Fatal("descriptor identity mismatch wrote replacement")
	}
}
func TestPendingOwnerCloseClosesUnadoptedLease(t *testing.T) {
	files := &pendingFaultFiles{held: make(chan struct{}), holdAfterOpen: make(chan struct{})}
	owner, writer, pending := makePending(t, files)
	files.arm("")
	future := beginPending(t, owner, pending)
	awaitSignal(t, files.held, "native opened lease not yet adopted")
	owner.Close()
	close(files.holdAfterOpen)
	if err := future.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatalf("owner rejection=%v", err)
	}
	if err := pending.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatalf("closed owner join=%v", err)
	}
	if files.count() != 1 {
		t.Fatalf("unadopted lease close=%d", files.count())
	}
	disk, _ := os.ReadFile(writer.target())
	if len(disk) != 0 {
		t.Fatal("rejected owner wrote")
	}
}
func TestPendingConstructorProfileAndFutureValidation(t *testing.T) {
	if _, err := NewPendingWriter(nil, nil); err == nil {
		t.Fatal("missing dependencies allowed")
	}
	owner, writer, pending := makePending(t, OSFiles{})
	pendingCall(t, owner, cooperative.Flush, func(*cooperative.Scope) error {
		if err := writer.Flush(); err == nil {
			return errors.New("old synchronous Flush bypassed independent job")
		}
		return nil
	})
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		if _, err := NewPendingWriter(writer, owner); err == nil {
			return errors.New("duplicate drain owner allowed")
		}
		return nil
	})
	other := cooperative.New()
	defer other.Close()
	pendingCall(t, other, cooperative.Input, func(scope *cooperative.Scope) error {
		_, err := pending.Begin(scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign owner mutated writer")
		}
		return nil
	})
	if _, err := pending.Begin(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatalf("wrong owner scope=%v", err)
	}
	if err := pending.Close(nil); err == nil {
		t.Fatal("nil Close context allowed")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(pending.Close(canceled), context.Canceled) {
		t.Fatal("canceled Close admitted")
	}
	future := newWriteFuture()
	if future.Wait(nil) == nil {
		t.Fatal("nil Future context allowed")
	}
	future.settle(io.EOF)
	future.settle(nil)
	if !errors.Is(future.Wait(canceled), io.EOF) {
		t.Fatal("settled promise replaced by late cancellation")
	}
	if !errors.Is(future.Wait(context.Background()), io.EOF) {
		t.Fatal("settlement was not once")
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		writer.queues["second-unsupported-target"] = []queuedEntry{{entry: plainMessage("second", "second")}}
		_, err := pending.Begin(scope)
		if err == nil {
			return errors.New("multiple targets allowed")
		}
		return nil
	})
	if err := pending.Close(context.Background()); err == nil {
		t.Fatal("Close bypassed unsupported order")
	}
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { delete(writer.queues, "second-unsupported-target"); return nil })
}
func TestLiveRecordRejectsBeforeAnyWriterEffects(t *testing.T) {
	owner, writer, _ := makePending(t, OSFiles{})
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		for _, profile := range []LiveRecordProfile{{}, {QuerySource: "sdk", ActiveHooks: true}, {QuerySource: "sdk", DynamicSDKQueue: true}, {QuerySource: "sdk", ResultOrAccounting: true}} {
			if _, err := writer.RecordLive(profile, nil, nil); err == nil {
				return errors.New("unsupported host profile accepted")
			}
		}
		for _, values := range [][]LiveRecord{{{}}, {{Record: plainMessage("both", "both"), Assistant: &streamhistory.Envelope{}}}, {{Assistant: &streamhistory.Envelope{}}}} {
			if _, err := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, values, nil); err == nil {
				return errors.New("malformed live input accepted")
			}
		}
		nonObject := protocol.JSONObject{}
		if _, err := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: &nonObject}}, nil); err == nil {
			return errors.New("null record accepted")
		}
		original := writer.liveEntries
		writer.liveEntries = nil
		_, err := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, nil, nil)
		writer.liveEntries = original
		if err == nil {
			return errors.New("uninitialized live map allowed")
		}
		bad := protocol.NewJSONObject(protocol.JSONField{Name: "bad", Value: json.RawMessage("{")})
		envelope := &streamhistory.Envelope{Message: bad}
		entry := protocol.NewJSONObject()
		writer.liveEntries[&entry] = envelope
		_, err = writer.marshalLiveEntry(&entry)
		delete(writer.liveEntries, &entry)
		if err == nil {
			return errors.New("bad live message marshaled")
		}
		return nil
	})
}

// Unknown active ASK/accounting/hooks remain caps; these native fault tests
// do not relabel the two actual Source writer-only contracts as a FullSDK host.

func TestPendingNaturalErrorCallbackAndCanceledCheckpointOwnDifferentDomains(t *testing.T) {
	files := &pendingFaultFiles{}
	owner, writer, pending := makePending(t, files)
	files.arm("write")
	var observed error
	var future *WriteFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		writer.options.OnError = func(err error) { observed = err; _ = writer.Snapshot() }
		var err error
		future, err = pending.begin(scope, true)
		return err
	})
	if err := future.Wait(context.Background()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if !errors.Is(observed, io.ErrClosedPipe) {
			return errors.New("natural failure callback not settled")
		}
		return nil
	})

	held := &heldFiles{mode: "open", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{})}
	secondOwner, secondWriter, secondPending := makePending(t, held)
	close(held.arm)
	secondFuture := beginPending(t, secondOwner, secondPending)
	awaitSignal(t, held.held, "checkpoint independent FS")
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original model turn interrupted")
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		done <- secondOwner.Run(ctx, func(scope *cooperative.Scope) error { close(started); return secondFuture.Await(scope) })
	}()
	awaitSignal(t, started, "checkpoint entered")
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("original cancel=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not release original checkpoint stack")
	}
	select {
	case <-secondFuture.Done():
		t.Fatal("model canceled filesystem job")
	default:
	}
	close(held.release)
	if err := secondPending.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(secondWriter.target())
	if !bytes.Contains(disk, []byte("native-first")) {
		t.Fatal("independent FS was canceled by model context")
	}
}
