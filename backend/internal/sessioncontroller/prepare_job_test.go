package sessioncontroller

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

var prepareStatFault = errors.New("original external fstat fault")
var prepareCloseFault = errors.New("original external close fault")

// Every held result comes from one native private operation before the barrier.
// Faults alter only the external I/O result, never Writer policy or entries.
type preparationFiles struct {
	OSFiles
	mu             sync.Mutex
	hold, fault    string
	held, release  chan struct{}
	releaseOnce    sync.Once
	flags          []int
	files          []*preparationFile
	closes, mkdirs int
}

func newPreparationFiles(hold, fault string) *preparationFiles {
	return &preparationFiles{hold: hold, fault: fault, held: make(chan struct{}), release: make(chan struct{})}
}
func (files *preparationFiles) barrier(phase string) {
	if files.hold == phase {
		close(files.held)
		<-files.release
	}
}
func (files *preparationFiles) unblock() { files.releaseOnce.Do(func() { close(files.release) }) }
func (files *preparationFiles) MkdirAll(path string, mode fs.FileMode) error {
	err := files.OSFiles.MkdirAll(path, mode)
	files.mu.Lock()
	files.mkdirs++
	files.mu.Unlock()
	files.barrier("mkdir")
	if err == nil && files.fault == "mkdir" {
		return fs.ErrPermission
	}
	return err
}
func (files *preparationFiles) OpenFile(path string, flags int, mode fs.FileMode) (TranscriptFile, error) {
	file, err := files.OSFiles.OpenFile(path, flags, mode)
	if err != nil {
		return nil, err
	}
	files.mu.Lock()
	files.flags = append(files.flags, flags)
	wrapped := &preparationFile{TranscriptFile: file, files: files}
	files.files = append(files.files, wrapped)
	files.mu.Unlock()
	files.barrier("open")
	if files.fault == "open" {
		_ = wrapped.Close()
		return nil, fs.ErrPermission
	}
	return wrapped, nil
}

type preparationFile struct {
	TranscriptFile
	files *preparationFiles
}
type preparationInfo struct {
	fs.FileInfo
	directory, missingIdentity bool
}

func (info preparationInfo) Mode() fs.FileMode {
	if info.directory {
		return fs.ModeDir
	}
	return info.FileInfo.Mode()
}
func (info preparationInfo) Sys() any {
	if info.missingIdentity {
		return nil
	}
	return info.FileInfo.Sys()
}
func (file *preparationFile) Stat() (fs.FileInfo, error) {
	info, err := file.TranscriptFile.Stat()
	file.files.barrier("stat")
	if err != nil {
		return nil, err
	}
	switch file.files.fault {
	case "stat", "stat-close":
		return nil, prepareStatFault
	case "directory":
		return preparationInfo{FileInfo: info, directory: true}, nil
	case "identity":
		return preparationInfo{FileInfo: info, missingIdentity: true}, nil
	}
	return info, nil
}
func (file *preparationFile) Close() error {
	err := file.TranscriptFile.Close()
	file.files.mu.Lock()
	file.files.closes++
	file.files.mu.Unlock()
	file.files.barrier("close")
	if err == nil && (file.files.fault == "close" || file.files.fault == "stat-close") {
		return prepareCloseFault
	}
	return err
}
func preparationHost(t *testing.T, files *preparationFiles) (*cooperative.Owner, *Writer, *PendingWriter) {
	t.Helper()
	owner := cooperative.New()
	writer := actualPendingWriter(t, owner, t.TempDir(), files)
	var pending *PendingWriter
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		writer.options.Schedule = func(time.Duration, func()) func() { return func() {} }
		var err error
		pending, err = NewPendingWriter(writer, owner)
		return err
	})
	t.Cleanup(func() {
		files.unblock()
		if err := pending.Close(context.Background()); err != nil {
			t.Error(err)
		}
		owner.Close()
	})
	return owner, writer, pending
}
func preparationIdentity(t *testing.T, owner *cooperative.Owner, writer *Writer, path string) bool {
	t.Helper()
	var found bool
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		writer.mu.Lock()
		_, found = writer.identities[path]
		writer.mu.Unlock()
		return nil
	})
	return found
}
func assertPreparationClosed(t *testing.T, files *preparationFiles, count int) {
	t.Helper()
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.closes != count {
		t.Fatalf("native close count %d, want %d", files.closes, count)
	}
	for _, file := range files.files {
		if _, err := file.TranscriptFile.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("descriptor survived settlement: %v", err)
		}
	}
}

func TestPreparationOwnerResumesThroughEveryPhysicalWait(t *testing.T) {
	for _, phase := range []string{"mkdir", "open", "stat", "close"} {
		t.Run(phase, func(t *testing.T) {
			files := newPreparationFiles(phase, "")
			owner, writer, pending := preparationHost(t, files)
			path := writer.target()
			var future *PreparationFuture
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					var err error
					future, err = pending.Prepare(scope, path)
					close(started)
					if err != nil {
						return err
					}
					return future.Await(scope)
				})
			}()
			awaitSignal(t, started, "prepare returned its own future")
			awaitSignal(t, files.held, "native "+phase+" result held")
			select {
			case <-future.Done():
				t.Fatal("prepare settled before its native result was released")
			default:
			}
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				writer.metadata.Set("customTitle", quote("owner update during physical wait"))
				return nil
			})
			files.unblock()
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if !preparationIdentity(t, owner, writer, path) {
				t.Fatal("native identity not adopted on owner")
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) != 0 || writer.path != "" || len(writer.members) != 0 {
				t.Fatal("physical preparation changed Record admission or wrote entries", err, string(data))
			}
			info, _ := os.Stat(path)
			dirInfo, _ := os.Stat(filepath.Dir(path))
			if info.Mode().Perm() != 0600 || dirInfo.Mode().Perm() != 0700 {
				t.Fatal("private preparation modes differ")
			}
			assertPreparationClosed(t, files, 1)
		})
	}
}

func TestPreparationRechecksPolicyOnlyAfterMkdirAwait(t *testing.T) {
	for _, stage := range []string{"before", "after-mkdir", "after-open"} {
		t.Run(stage, func(t *testing.T) {
			hold := ""
			if stage == "after-mkdir" {
				hold = "mkdir"
			} else if stage == "after-open" {
				hold = "open"
			}
			files := newPreparationFiles(hold, "")
			owner, writer, pending := preparationHost(t, files)
			var future *PreparationFuture
			pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
				if stage == "before" {
					writer.options.SessionPersistenceDisabled = true
				}
				var err error
				future, err = pending.Prepare(scope, writer.target())
				return err
			})
			if hold != "" {
				awaitSignal(t, files.held, "native policy boundary")
				pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
					writer.options.SessionPersistenceDisabled = true
					return nil
				})
				files.unblock()
			}
			if err := future.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			files.mu.Lock()
			mkdirs, opened := files.mkdirs, len(files.flags)
			files.mu.Unlock()
			wantMkdirs, wantOpened := 1, 0
			if stage == "before" {
				wantMkdirs = 0
			} else if stage == "after-open" {
				wantOpened = 1
			}
			if mkdirs != wantMkdirs || opened != wantOpened {
				t.Fatalf("wrong policy boundary: mkdir/open %d/%d, want %d/%d", mkdirs, opened, wantMkdirs, wantOpened)
			}
			assertPreparationClosed(t, files, wantOpened)
		})
	}
}

func TestPreparationNativeFailurePriorityAndNoEscapingLeases(t *testing.T) {
	for _, fault := range []string{"mkdir", "open", "stat", "directory", "identity", "close", "stat-close"} {
		t.Run(fault, func(t *testing.T) {
			files := newPreparationFiles("", fault)
			owner, writer, pending := preparationHost(t, files)
			var future *PreparationFuture
			pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
				var err error
				future, err = pending.Prepare(scope, writer.target())
				return err
			})
			err := future.Wait(context.Background())
			if err == nil {
				t.Fatal("native failure disappeared")
			}
			if fault == "stat" && !errors.Is(err, prepareStatFault) || (fault == "close" || fault == "stat-close") && !errors.Is(err, prepareCloseFault) {
				t.Fatalf("original finally error priority lost: %v", err)
			}
			if fault == "directory" && !strings.Contains(err.Error(), "Refusing non-regular transcript target:") {
				t.Fatal(err)
			}
			if preparationIdentity(t, owner, writer, writer.target()) != (fault == "close") {
				t.Fatal("identity was not stored at the original pre-close boundary")
			}
			closes := 1
			if fault == "mkdir" {
				closes = 0
			}
			assertPreparationClosed(t, files, closes)
		})
	}
}

func TestPreparationAwaitCancellationDoesNotOwnNativeJob(t *testing.T) {
	files := newPreparationFiles("stat", "")
	owner, writer, pending := preparationHost(t, files)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original model observer stopped")
	defer cancel(cause)
	var future *PreparationFuture
	returned := make(chan error, 1)
	go func() {
		returned <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			future, err = pending.Prepare(scope, writer.target())
			if err != nil {
				return err
			}
			return future.Await(scope)
		})
	}()
	awaitSignal(t, files.held, "independent native fstat")
	cancel(cause)
	if err := <-returned; !errors.Is(err, cause) {
		t.Fatal("observer cancellation lost", err)
	}
	select {
	case <-future.Done():
		t.Fatal("model cancellation settled the independent native job")
	default:
	}
	files.unblock()
	if err := future.Wait(context.Background()); err != nil {
		t.Fatal("native job inherited model cancellation", err)
	}
	assertPreparationClosed(t, files, 1)
}

func TestPreparationCloseJoinsPhysicalContinuationAndRejectsNewAdmission(t *testing.T) {
	files := newPreparationFiles("open", "")
	owner, writer, pending := preparationHost(t, files)
	var future *PreparationFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		future, err = pending.Prepare(scope, writer.target())
		return err
	})
	awaitSignal(t, files.held, "native descriptor held")
	closing := make(chan error, 1)
	go func() { closing <- pending.Close(context.Background()) }()
	// Observe actual close admission on the owner, without a timing delay.
	for {
		var admitted bool
		pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { admitted = pending.closing; return nil })
		if admitted {
			break
		}
	}
	select {
	case err := <-closing:
		t.Fatal("Close returned before the physical job joined", err)
	default:
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if _, err := pending.Prepare(scope, writer.target()); !errors.Is(err, ErrPendingClosed) {
			return errors.New("new preparation admitted after Close")
		}
		return nil
	})
	files.unblock()
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if err := future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPreparationClosed(t, files, 1)
}

func TestPreparationRejectsWrongOwnerBeforeAnyFilesystemEffect(t *testing.T) {
	files := newPreparationFiles("", "")
	_, writer, pending := preparationHost(t, files)
	other := cooperative.New()
	defer other.Close()
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := pending.Prepare(scope, writer.target()); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign owner was accepted")
		}
		if err := (&PreparationFuture{future: newWriteFuture(), owner: pending.owner}).Await(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign scope awaited preparation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.mkdirs != 0 || len(files.flags) != 0 {
		t.Fatal("foreign owner caused native side effects")
	}
}

func TestPreparationForcedOwnerShutdownClosesUnadoptedNativeDescriptor(t *testing.T) {
	files := newPreparationFiles("open", "")
	owner := cooperative.New()
	writer := actualPendingWriter(t, owner, t.TempDir(), files)
	var pending *PendingWriter
	var future *PreparationFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		pending, err = NewPendingWriter(writer, owner)
		if err != nil {
			return err
		}
		future, err = pending.Prepare(scope, writer.target())
		return err
	})
	t.Cleanup(func() { files.unblock(); pending.workers.Wait(); owner.Close() })
	awaitSignal(t, files.held, "unadopted native open")
	// Force the exceptional lifecycle order. The owner is actually terminal
	// before the native open result is released; this is not a rejected mock.
	owner.Close()
	files.unblock()
	if err := future.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("administrative owner failure disappeared", err)
	}
	pending.workers.Wait()
	assertPreparationClosed(t, files, 1)
	if len(writer.identities) != 0 {
		t.Fatal("unadopted descriptor changed owner state after shutdown")
	}
	if err := pending.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("terminal owner was reported as a successful close", err)
	}
}

func TestPreparationCancelledAdmissionHasNoNativeEffects(t *testing.T) {
	files := newPreparationFiles("", "")
	owner, writer, pending := preparationHost(t, files)
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("actual owner admission cancellation")
	defer cancel(cause)
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel(cause)
		future, err := pending.Prepare(scope, writer.target())
		if future != nil {
			return errors.New("cancelled admission returned a native job")
		}
		return err
	}); !errors.Is(err, cause) {
		t.Fatal("cancelled scope failure lost", err)
	}
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.mkdirs != 0 || len(files.flags) != 0 {
		t.Fatal("cancelled admission caused native side effects")
	}
}
