package sessioncontroller

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This existing native-files seam holds one immutable, actual OS open result.
// CAS occurs before the hold, so other independent jobs can use their own real
// OS descriptors; sync.Once must not serialize all OpenFile calls behind it.
// No physical error/value is replaced and no Source result is wrapped.
type manualPhysicalFiles struct {
	OSFiles
	armed         atomic.Bool
	held          atomic.Bool
	entered       chan struct{}
	release       chan struct{}
	releaseOnce   sync.Once
	nativeCloses  atomic.Int32
	physicalError error
}
type manualPhysicalFile struct {
	TranscriptFile
	files  *manualPhysicalFiles
	native bool
}

func newManualPhysicalFiles() *manualPhysicalFiles {
	return &manualPhysicalFiles{entered: make(chan struct{}), release: make(chan struct{})}
}
func (files *manualPhysicalFiles) OpenFile(path string, flag int, mode fs.FileMode) (TranscriptFile, error) {
	file, err := files.OSFiles.OpenFile(path, flag, mode)
	native := flag&os.O_CREATE == 0
	if native && files.armed.Load() && files.held.CompareAndSwap(false, true) {
		files.physicalError = err
		close(files.entered)
		<-files.release
	}
	if file == nil {
		return nil, err
	}
	return &manualPhysicalFile{TranscriptFile: file, files: files, native: native}, err
}
func (file *manualPhysicalFile) Close() error {
	err := file.TranscriptFile.Close()
	if file.native {
		file.files.nativeCloses.Add(1)
	}
	return err
}
func (files *manualPhysicalFiles) unblock() { files.releaseOnce.Do(func() { close(files.release) }) }
func manualEmptyFlush(t *testing.T, owner *cooperative.Owner, pending *PendingWriter, ctx context.Context) *FlushFuture {
	t.Helper()
	empty := beginTrackedFlush(t, owner, pending)
	if err := empty.Wait(ctx); err != nil {
		t.Fatal("independent empty explicit flush inherited another job", err)
	}
	return empty
}

func TestManualDrainCloseJoinsHeldPhysicalJobAfterIndependentEmptyFlush(t *testing.T) {
	files := newManualPhysicalFiles()
	owner, writer, pending := makePending(t, files)
	defer files.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	files.armed.Store(true)
	first := beginTrackedFlush(t, owner, pending)
	awaitSignal(t, files.entered, "actual manual native open result")
	empty := manualEmptyFlush(t, owner, pending, ctx)
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		if pending.active != nil || len(pending.drains) != 1 {
			return errors.New("manual jobs changed timer slot or retained empty drain")
		}
		original, err := pending.Begin(scope)
		if err != nil {
			return err
		}
		if original != pending.drains[0].future {
			return errors.New("legacy observer lost earliest original physical job")
		}
		return nil
	})
	closed := make(chan error, 1)
	go func() { closed <- pending.Close(context.Background()) }()
	// Read only until the real Close admission fence is observed. There is no
	// fixed tick count/sleep, and no read changes native readiness or job results.
	for {
		admitted := false
		err := owner.Run(ctx, func(*cooperative.Scope) error { admitted = pending.closing; return nil })
		if err != nil {
			t.Fatal("actual Close admission", err)
		}
		if admitted {
			break
		}
	}
	select {
	case err := <-closed:
		t.Fatal("Close returned before its admitted held native job", err)
	default:
	}
	if err := empty.Wait(ctx); err != nil {
		t.Fatal("administrative Close changed fulfilled public flush", err)
	}
	files.unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal("physical Close", err)
		}
	case <-ctx.Done():
		t.Fatal("physical Close did not join", ctx.Err())
	}
	if err := first.Wait(ctx); err != nil {
		t.Fatal("first explicit return", err)
	}
	if files.nativeCloses.Load() != 1 {
		t.Fatal("original native descriptor not physically closed once", files.nativeCloses.Load())
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !bytes.Contains(disk, []byte(`"uuid":"native-first"`)) {
		t.Fatal("admitted original bytes missing after Close", err, string(disk))
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if len(pending.drains) != 0 || pending.active != nil {
			return errors.New("Close retained finished real job or timer identity")
		}
		return nil
	})
}

func TestManualDrainOwnerCloseKeepsIndependentReturnAndCleansNativeLease(t *testing.T) {
	files := newManualPhysicalFiles()
	owner, _, pending := makePending(t, files)
	defer files.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	files.armed.Store(true)
	first := beginTrackedFlush(t, owner, pending)
	awaitSignal(t, files.entered, "unadopted real manual native lease")
	empty := manualEmptyFlush(t, owner, pending, ctx)
	owner.Close()
	files.unblock()
	if err := first.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("original manual owner rejection", err)
	}
	if err := pending.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed Owner physical join", err)
	}
	if err := empty.Wait(ctx); err != nil {
		t.Fatal("Owner close replaced previously fulfilled independent return", err)
	}
	if files.nativeCloses.Load() != 1 {
		t.Fatal("unadopted original descriptor close count", files.nativeCloses.Load())
	}
}

func TestManualDrainObserverCancellationDoesNotChangeOtherReturnOrPhysicalJob(t *testing.T) {
	files := newManualPhysicalFiles()
	owner, _, pending := makePending(t, files)
	defer files.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	files.armed.Store(true)
	first := beginTrackedFlush(t, owner, pending)
	awaitSignal(t, files.entered, "actual manual job before observer cancellation")
	empty := manualEmptyFlush(t, owner, pending, ctx)
	observer, stop := context.WithCancelCause(context.Background())
	cause := errors.New("original manual observer cancellation")
	stop(cause)
	if err := first.Wait(observer); err != cause {
		t.Fatal("observer error identity", err)
	}
	if err := empty.Wait(observer); err != nil {
		t.Fatal("canceled observer changed independent settled return", err)
	}
	select {
	case <-first.Done():
		t.Fatal("observer cancellation settled real physical job")
	default:
	}
	files.unblock()
	if err := first.Wait(ctx); err != nil {
		t.Fatal("original native completion after canceled observer", err)
	}
	if err := pending.Close(context.Background()); err != nil {
		t.Fatal("physical Close after canceled observer", err)
	}
	if files.nativeCloses.Load() != 1 {
		t.Fatal("native lease not joined once", files.nativeCloses.Load())
	}
}

func TestRuntimeMetadataAcknowledgesOwnPhysicalWriteBesideFailedManualDrain(t *testing.T) {
	files := newManualPhysicalFiles()
	owner, writer, pending := makePending(t, files)
	defer files.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := writer.target()
	backup := path + ".metadata-own-physical-backup"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	renamed := true
	defer func() {
		files.unblock()
		if renamed {
			_ = os.Remove(path)
			_ = os.Rename(backup, path)
		}
	}()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	files.armed.Store(true)
	first := beginTrackedFlush(t, owner, pending)
	awaitSignal(t, files.entered, "first actual EISDIR result before metadata")
	if !errors.Is(files.physicalError, syscall.EISDIR) {
		t.Fatal("original physical EISDIR required", files.physicalError)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	renamed = false
	state, err := bootstrapstate.New(owner, writer.options.SessionID, writer.options.CWD, func() (string, error) { return writer.options.SessionID, nil })
	if err != nil {
		t.Fatal(err)
	}
	controller := &Controller{writer: writer, options: Options{Bootstrap: state}}
	controller.options.ResumeOptions.Loader = recordLoader(t, nil)
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { writer.options.Bootstrap = state; return nil })
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("session-meta")}, protocol.JSONField{Name: "runtimeModelId", Value: quote("metadata-own-native-ack")})
	receipt, err := owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error { return controller.AppendRuntimeMetadata(scope, metadata) })
	if err != nil {
		t.Fatal(err)
	}
	timedOut := false
	select {
	case <-receipt.Done():
	case <-ctx.Done():
		timedOut = true
	}
	// Always release and physically join the real older OS result before any
	// assertion or automatic cleanup. Its original error identity must survive.
	files.unblock()
	originalErr := first.Wait(context.Background())
	metadataErr := receipt.Wait()
	if err = pending.Close(context.Background()); err != nil {
		t.Fatal("physical Close after metadata ACK", err)
	}
	if timedOut {
		t.Fatal("metadata ACK waited on another manual drain instead of its own native write")
	}
	if originalErr != files.physicalError || !errors.Is(originalErr, syscall.EISDIR) {
		t.Fatal("older manual physical error identity changed", originalErr, files.physicalError)
	}
	if metadataErr != nil {
		t.Fatal("metadata ACK inherited unrelated manual error", metadataErr)
	}
	disk, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(disk), `"runtimeModelId":"metadata-own-native-ack"`) {
		t.Fatal("metadata ACK lacked its actual persisted bytes", err, string(disk))
	}
	if files.nativeCloses.Load() != 1 {
		t.Fatal("metadata descriptor was not physically joined exactly once", files.nativeCloses.Load())
	}
}
