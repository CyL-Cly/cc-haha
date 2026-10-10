package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

// The barrier follows the real private append call, withholding only its native
// completion. It does not replace flush, Record, owner reactions or metadata.
type cleanupFiles struct {
	sessioncontroller.OSFiles
	mu                         sync.Mutex
	events                     []string
	entered, release           chan struct{}
	once                       sync.Once
	appendFault, metadataFault error
	onStop                     func()
}

func (f *cleanupFiles) event(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, s)
}
func (f *cleanupFiles) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}
func (f *cleanupFiles) OpenFile(p string, flags int, mode fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	file, err := f.OSFiles.OpenFile(p, flags, mode)
	if err != nil {
		return nil, err
	}
	return &cleanupFile{TranscriptFile: file, files: f}, nil
}

type cleanupFile struct {
	sessioncontroller.TranscriptFile
	files *cleanupFiles
}

func (f *cleanupFile) Write(b []byte) (int, error) {
	if strings.Contains(string(b), `"type":"custom-title"`) {
		f.files.event("metadata:physical-write")
		if f.files.metadataFault != nil {
			return 0, f.files.metadataFault
		}
	}
	n, err := f.TranscriptFile.Write(b)
	if strings.Contains(string(b), `"uuid":"held-append"`) {
		f.files.event("append:native-return")
		f.files.once.Do(func() { close(f.files.entered) })
		<-f.files.release
		f.files.event("append:completion-release")
		if f.files.appendFault != nil {
			return n, f.files.appendFault
		}
	}
	return n, err
}
func (f *cleanupFile) Close() error {
	err := f.TranscriptFile.Close()
	f.files.event("native:close")
	return err
}
func cleanupCall(t *testing.T, o *cooperative.Owner, run func(*cooperative.Scope) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.Run(ctx, run); err != nil {
		t.Fatal(err)
	}
}
func cleanupSignal(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("physical completion barrier was not reached")
	}
}
func cleanupObject(s string) protocol.JSONObject {
	var o protocol.JSONObject
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		panic(err)
	}
	return o
}

// Construct real shared Controller/Writer services over empty private settings.
// No ASK/model, Source prepared message, secondary owner or business mock is used.
func cleanupHost(t *testing.T, f *cleanupFiles) (*Host, *sessionhistory.Loader, string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	owner := cooperative.New()
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	uuid := func() string { return "22222222-2222-4222-8222-222222222222" }
	factory, err := messages.NewFactory(uuid, now)
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(func() float64 { return 1700000000000 })
	if err != nil {
		t.Fatal(err)
	}
	physical := resumestate.OSFiles{}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: physical, Stat: func(p string) (int64, error) {
		info, e := os.Stat(p)
		if e != nil {
			return 0, e
		}
		return info.Size(), nil
	}, ReadFile: physical.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(r json.RawMessage) (json.RawMessage, error) { return sessionresume.RelativePath(home, home, r) }, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: func(e error) { f.event("Record-error:" + e.Error()) }})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "NODE_ENV": "production", "CLAUDE_CODE_SIMPLE": "1"}
	controller, err := sessioncontroller.New(sessioncontroller.Options{AsyncPersistence: true, ResumeOptions: resumestate.Options{NativeProjectPaths: true, ConfigDir: config, CWD: home, OriginalCWD: home, CurrentSessionID: uuid(), NonInteractive: true, Environment: env, Files: physical, Loader: loader, Reducer: reducer, ParseDate: sessionprojection.ParseDateUTC, Stat: func(p string) (resumestate.Stat, error) {
		s, e := os.Stat(p)
		if e != nil {
			return resumestate.Stat{}, e
		}
		return resumestate.Stat{Size: s.Size(), Modified: float64(s.ModTime().UnixMilli())}, nil
	}, Now: func() int64 { return now().UnixMilli() }, UUID: uuid, OnDiagnostic: func(sessionresume.Diagnostic) {}, OnError: func(e error) { f.event("resume-error:" + e.Error()) }, OnPhase: f.event, PolicyPaths: []string{filepath.Join(home, "policy.json")}, SettingsState: state}, WriterOptions: sessioncontroller.WriterOptions{ConfigDir: config, CWD: home, SessionID: uuid(), Version: "fixture-version", Environment: env, SettingsState: state, Files: f, Branch: func() (string, error) {
		return sessioncontroller.NoRepositoryBranch(home, func(p string) (os.FileInfo, error) {
			if p != filepath.Join(home, ".git") {
				return nil, os.ErrNotExist
			}
			return os.Stat(p)
		})
	}, OnError: func(e error) { f.event("writer-error:" + e.Error()) }, Schedule: func(time.Duration, func()) func() {
		return func() {
			if f.onStop != nil {
				f.onStop()
			}
		}
	}, Now: now}, Factory: factory, UUID: uuid, Now: now, OnPhase: f.event, BaseToolsCLI: []string{""}, CustomSystemPrompt: json.RawMessage(`"original private system"`)})
	if err != nil {
		t.Fatal(err)
	}
	writer := controller.Writer()
	cleanupCall(t, owner, func(*cooperative.Scope) error {
		m := cleanupObject(`{"type":"user","uuid":"setup","message":{"role":"user","content":"private setup"}}`)
		_, e := writer.Record([]*protocol.JSONObject{&m}, nil)
		return e
	})
	pending, err := sessioncontroller.NewPendingWriter(writer, owner)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{options: Options{Owner: owner, Controller: controller, OnPhase: f.event}, pending: pending}
	cleanupCall(t, owner, func(s *cooperative.Scope) error {
		future, e := pending.Flush(s)
		if e != nil {
			return e
		}
		return future.Await(s)
	})
	cleanupCall(t, owner, func(*cooperative.Scope) error {
		writer.RestoreMetadata(cleanupObject(`{"customTitle":"private title"}`))
		return nil
	})
	path := writer.Snapshot().String("path")
	t.Cleanup(func() {
		f.once.Do(func() { close(f.entered) })
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		_ = pending.Close(context.Background())
		owner.Close()
		controller.AwaitBackground()
	})
	return h, loader, path
}
func newCleanupFiles() *cleanupFiles {
	return &cleanupFiles{entered: make(chan struct{}), release: make(chan struct{})}
}
func cleanupAwait(t *testing.T, h *Host, f *runtimecleanup.Future) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.options.Owner.Run(ctx, func(s *cooperative.Scope) error { _, e := f.Await(s); return e })
}
func eventIndex(events []string, s string) int {
	for i, e := range events {
		if e == s {
			return i
		}
	}
	return -1
}

func TestProjectCleanupStartDoesNotAwaitHeldAppendOrLaterCallback(t *testing.T) {
	files := newCleanupFiles()
	h, _, path := cleanupHost(t, files)
	var result *runtimecleanup.Future
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error {
		entry := cleanupObject(`{"type":"user","uuid":"held-append","message":{"role":"user","content":"physical append"}}`)
		if e := h.options.Controller.Writer().Append(&entry); e != nil {
			return e
		}
		registry, e := runtimecleanup.New(h.options.Owner)
		if e != nil {
			return e
		}
		if _, e = registry.Register(s, runtimecleanup.NewCallback(h.StartCleanup)); e != nil {
			return e
		}
		if _, e = registry.Register(s, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
			files.event("later-cleanup:entry")
			return nil, nil
		})); e != nil {
			return e
		}
		result, e = registry.Start(s)
		files.event("registry:start-return")
		return e
	})
	cleanupSignal(t, files.entered)
	events := files.snapshot()
	if eventIndex(events, "later-cleanup:entry") < 0 || eventIndex(events, "registry:start-return") < 0 || eventIndex(events, "metadata:shutdown-reappend") >= 0 {
		t.Fatalf("cleanup startup awaited or metadata ran early: %v", events)
	}
	select {
	case <-result.Done():
		t.Fatal("callback resolved before physical append completion")
	default:
	}
	close(files.release)
	if e := cleanupAwait(t, h, result); e != nil {
		t.Fatal(e)
	}
	events = files.snapshot()
	if eventIndex(events, "metadata:shutdown-reappend") <= eventIndex(events, "append:completion-release") || eventIndex(events, "metadata:physical-write") <= eventIndex(events, "metadata:shutdown-reappend") {
		t.Fatalf("metadata escaped fulfilled flush reaction: %v", events)
	}
	disk, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(disk), `"uuid":"held-append"`) || !strings.HasSuffix(string(disk), `{"type":"custom-title","customTitle":"private title","sessionId":"22222222-2222-4222-8222-222222222222"}`+"\n") {
		t.Fatalf("actual disk append/metadata missing: %s %v", disk, e)
	}
}
func TestProjectCleanupFlushRejectionRetainsIdentityAndSkipsMetadata(t *testing.T) {
	files := newCleanupFiles()
	failure := errors.New("original external append failure")
	files.appendFault = failure
	h, _, _ := cleanupHost(t, files)
	var result *runtimecleanup.Future
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error {
		entry := cleanupObject(`{"type":"user","uuid":"held-append","message":{"content":"fault"}}`)
		if e := h.options.Controller.Writer().Append(&entry); e != nil {
			return e
		}
		var e error
		result, e = h.StartCleanup(s)
		return e
	})
	cleanupSignal(t, files.entered)
	close(files.release)
	if e := cleanupAwait(t, h, result); e != failure {
		t.Fatalf("flush reason changed identity: %v", e)
	}
	if eventIndex(files.snapshot(), "metadata:shutdown-reappend") >= 0 || eventIndex(files.snapshot(), "metadata:physical-write") >= 0 {
		t.Fatal("rejected flush ran metadata")
	}
}
func TestProjectCleanupSynchronousFlushFailureIsRejectedPromise(t *testing.T) {
	files := newCleanupFiles()
	h, _, _ := cleanupHost(t, files)
	if e := h.pending.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	var result *runtimecleanup.Future
	later := false
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error {
		registry, e := runtimecleanup.New(h.options.Owner)
		if e != nil {
			return e
		}
		_, e = registry.Register(s, runtimecleanup.NewCallback(h.StartCleanup))
		if e != nil {
			return e
		}
		_, e = registry.Register(s, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { later = true; return nil, nil }))
		if e != nil {
			return e
		}
		result, e = registry.Start(s)
		return e
	})
	if !later {
		t.Fatal("async callback's synchronous flush failure stopped the registry map")
	}
	if e := cleanupAwait(t, h, result); e != sessioncontroller.ErrPendingClosed {
		t.Fatalf("sync flush reason changed: %v", e)
	}
	if eventIndex(files.snapshot(), "metadata:shutdown-reappend") >= 0 {
		t.Fatal("sync failure ran metadata")
	}
}
func TestProjectCleanupMetadataFailureIsBestEffort(t *testing.T) {
	files := newCleanupFiles()
	files.metadataFault = errors.New("original metadata fault")
	h, _, _ := cleanupHost(t, files)
	var result *runtimecleanup.Future
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error { var e error; result, e = h.StartCleanup(s); return e })
	if e := cleanupAwait(t, h, result); e != nil {
		t.Fatal("metadata's independent try/catch failed", e)
	}
	if eventIndex(files.snapshot(), "metadata:physical-write") < 0 {
		t.Fatal("metadata was not physically attempted")
	}
}
func TestProjectCleanupRejectsForeignStaleNilAndCancelledScopes(t *testing.T) {
	files := newCleanupFiles()
	h, _, _ := cleanupHost(t, files)
	foreign := cooperative.New()
	defer foreign.Close()
	cleanupCall(t, foreign, func(s *cooperative.Scope) error {
		f, e := h.StartCleanup(s)
		if f != nil || e != cooperative.ErrRestricted {
			return errors.New("foreign owner accepted")
		}
		return nil
	})
	if f, e := h.StartCleanup(nil); f != nil || e != cooperative.ErrRestricted {
		t.Fatal("nil owner accepted")
	}
	var stale *cooperative.Scope
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error { stale = s; return nil })
	if f, e := h.StartCleanup(stale); f != nil || e != cooperative.ErrRestricted {
		t.Fatal("expired scope accepted")
	}
	var absent *Host
	if f, e := absent.StartCleanup(nil); f != nil || e != cooperative.ErrRestricted {
		t.Fatal("nil host accepted")
	}
	if h.closing {
		t.Fatal("administrative guards changed cleanup state")
	}
	reason := errors.New("original cancelled start")
	ctx, cancel := context.WithCancelCause(context.Background())
	if e := h.options.Owner.Run(ctx, func(s *cooperative.Scope) error {
		cancel(reason)
		f, e := h.StartCleanup(s)
		if f != nil || e != reason {
			return errors.New("cancelled scope accepted")
		}
		return nil
	}); e != nil && e != reason {
		t.Fatal(e)
	}
	if h.closing {
		t.Fatal("cancelled entry changed cleanup state")
	}
}
func TestProjectCleanupDoesNotJoinUntrackedRecordNativeRead(t *testing.T) {
	files := newCleanupFiles()
	h, _, path := cleanupHost(t, files)
	physical := resumestate.OSFiles{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	loader, e := sessionhistory.New(sessionhistory.Options{Files: physical, Stat: func(p string) (int64, error) {
		i, e := os.Stat(p)
		if e != nil {
			return 0, e
		}
		return i.Size(), nil
	}, ReadFile: func(p string) ([]byte, error) {
		b, e := physical.ReadFile(p)
		if p == path {
			once.Do(func() { close(entered) })
			<-release
		}
		return b, e
	}, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var record *sessioncontroller.RecordFuture
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error {
		m := cleanupObject(`{"type":"user","uuid":"untracked-late","message":{"role":"user","content":"held cold cache"}}`)
		var e error
		record, e = h.pending.BeginRecordLive(s, loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, []sessioncontroller.LiveRecord{{Record: &m}}, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return json.RawMessage(`"original-live-prompt"`) }})
		return e
	})
	cleanupSignal(t, entered)
	var result *runtimecleanup.Future
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error { var e error; result, e = h.StartCleanup(s); return e })
	if e = cleanupAwait(t, h, result); e != nil {
		t.Fatal(e)
	}
	select {
	case <-record.Done():
		t.Fatal("outer Record was fabricated completed or joined")
	default:
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e = record.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	cleanupCall(t, h.options.Owner, func(s *cooperative.Scope) error {
		f, e := h.pending.Flush(s)
		if e != nil {
			return e
		}
		return f.Await(s)
	})
}

// Administrative cancellation originates inside the real Flush timer-clear
// call. It is a supplied host fault, separate from Source model cancellation.
func TestProjectCleanupAdmittedCancellationReturnsRejectedCallbackFuture(t *testing.T) {
	files := newCleanupFiles()
	h, _, _ := cleanupHost(t, files)
	reason := errors.New("original admitted cleanup cancellation")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var result *runtimecleanup.Future
	var startError error
	completed := make(chan struct{})
	runError := h.options.Owner.Run(ctx, func(s *cooperative.Scope) error {
		defer close(completed)
		entry := cleanupObject(`{"type":"user","uuid":"cancel-at-stop","message":{"content":"private queued append"}}`)
		if err := h.options.Controller.Writer().Append(&entry); err != nil {
			return err
		}
		files.onStop = func() { files.event("timer-stop:cancel"); cancel(reason) }
		result, startError = h.StartCleanup(s)
		return nil
	})
	cleanupSignal(t, completed)
	if runError != nil && runError != reason {
		t.Fatal(runError)
	}
	if !h.closing || eventIndex(files.snapshot(), "timer-stop:cancel") < 0 {
		t.Fatal("real Flush timer-clear was not entered")
	}
	if result == nil || startError != nil {
		t.Fatalf("admitted async callback became synchronous error: future=%v error=%v", result, startError)
	}
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err := result.Wait(wait)
	if err != reason {
		t.Fatalf("callback rejection lost original identity: %v", err)
	}
	if eventIndex(files.snapshot(), "metadata:shutdown-reappend") >= 0 {
		t.Fatal("administratively rejected flush ran metadata")
	}
}
