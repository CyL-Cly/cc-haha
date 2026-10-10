package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type realScheduleInput struct {
	SessionID      string            `json:"sessionID"`
	Assistant      json.RawMessage   `json:"assistant"`
	Seed           string            `json:"seed"`
	LateText       string            `json:"lateText"`
	LateOutput     int               `json:"lateOutput"`
	ProviderStart  []json.RawMessage `json:"providerStart"`
	ProviderFinish []json.RawMessage `json:"providerFinish"`
}

func realScheduleOriginal(t *testing.T) realScheduleInput {
	t.Helper()
	b, e := os.ReadFile("testdata/real_schedule_original.json")
	if e != nil {
		t.Fatal(e)
	}
	var input realScheduleInput
	if e = json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	return input
}

type realScheduleTrace struct {
	mu     sync.Mutex
	events []protocol.JSONObject
}

func (trace *realScheduleTrace) add(operation string, fields ...protocol.JSONField) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	fields = append([]protocol.JSONField{{Name: "operation", Value: quote(operation)}, {Name: "nativeUnixNano", Value: raw(time.Now().UnixNano())}}, fields...)
	trace.events = append(trace.events, protocol.NewJSONObject(fields...))
}
func (trace *realScheduleTrace) snapshot() []protocol.JSONObject {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]protocol.JSONObject(nil), trace.events...)
}

// One actual physical adapter. It delegates native calls with untouched args,
// holds only the first non-create append-open completion, and preserves the
// actual file/result/error. No Writer decision, byte snapshot or timer is faked.
type realScheduleNativeFiles struct {
	OSFiles
	trace                                       *realScheduleTrace
	held, release, written, closed              chan struct{}
	onceHold, onceRelease, onceWrite, onceClose sync.Once
	mu                                          sync.Mutex
	openFiles                                   int
	scheduledAt, appendOpenAt                   time.Time
}

func realScheduleFiles(trace *realScheduleTrace) *realScheduleNativeFiles {
	return &realScheduleNativeFiles{trace: trace, held: make(chan struct{}), release: make(chan struct{}), written: make(chan struct{}), closed: make(chan struct{})}
}
func (files *realScheduleNativeFiles) unblock() {
	files.onceRelease.Do(func() { close(files.release) })
}
func (files *realScheduleNativeFiles) OpenFile(path string, flags int, mode fs.FileMode) (TranscriptFile, error) {
	file, e := files.OSFiles.OpenFile(path, flags, mode)
	files.trace.add("native-open-return", protocol.JSONField{Name: "path", Value: quote(path)}, protocol.JSONField{Name: "flags", Value: raw(flags)}, protocol.JSONField{Name: "mode", Value: raw(int(mode))}, protocol.JSONField{Name: "error", Value: raw(fmt.Sprint(e))})
	if e != nil {
		return file, e
	}
	files.mu.Lock()
	files.openFiles++
	files.mu.Unlock()
	drain := flags&os.O_CREATE == 0
	if drain {
		files.mu.Lock()
		files.appendOpenAt = time.Now()
		files.mu.Unlock()
		files.onceHold.Do(func() {
			files.trace.add("native-open-completion-held")
			close(files.held)
			<-files.release
			files.trace.add("native-open-completion-released")
		})
	}
	return &realScheduleNativeFile{TranscriptFile: file, files: files, drain: drain}, e
}

type realScheduleNativeFile struct {
	TranscriptFile
	files *realScheduleNativeFiles
	drain bool
}

func (file *realScheduleNativeFile) Write(bytes []byte) (int, error) {
	n, e := file.TranscriptFile.Write(bytes)
	file.files.trace.add("actual-native-write", protocol.JSONField{Name: "bytes", Value: quote(string(bytes))}, protocol.JSONField{Name: "returnedN", Value: raw(n)}, protocol.JSONField{Name: "error", Value: raw(fmt.Sprint(e))})
	if e == nil {
		file.files.onceWrite.Do(func() { close(file.files.written) })
	}
	return n, e
}
func (file *realScheduleNativeFile) Close() error {
	e := file.TranscriptFile.Close()
	file.files.mu.Lock()
	file.files.openFiles--
	file.files.mu.Unlock()
	file.files.trace.add("actual-native-close", protocol.JSONField{Name: "drain", Value: raw(file.drain)}, protocol.JSONField{Name: "error", Value: raw(fmt.Sprint(e))})
	if file.drain {
		file.files.onceClose.Do(func() { close(file.files.closed) })
	}
	return e
}

type realScheduleColdIO struct {
	sessionstore.OSFileSystem
	trace             *realScheduleTrace
	held, release     chan struct{}
	hold, unblockOnce sync.Once
}

func (physical *realScheduleColdIO) Stat(path string) (int64, error) {
	info, e := os.Stat(path)
	if e != nil {
		return 0, e
	}
	return info.Size(), nil
}
func (physical *realScheduleColdIO) ReadFile(path string) ([]byte, error) {
	b, e := os.ReadFile(path)
	physical.trace.add("native-cold-read-return", protocol.JSONField{Name: "bytes", Value: quote(string(b))}, protocol.JSONField{Name: "error", Value: raw(fmt.Sprint(e))})
	if e == nil && physical.held != nil {
		physical.hold.Do(func() { close(physical.held); <-physical.release })
	}
	return b, e
}
func (physical *realScheduleColdIO) unblock() {
	physical.unblockOnce.Do(func() {
		if physical.release != nil {
			close(physical.release)
		}
	})
}

type realScheduleHost struct {
	owner    *cooperative.Owner
	writer   *Writer
	pending  *PendingWriter
	loader   *sessionhistory.Loader
	registry *runtimecleanup.Registry
	process  *diagnostics.Process
	state    *bootstrapstate.State
	branch   *OwnedRecordBranch
	files    *realScheduleNativeFiles
	physical *realScheduleColdIO
	trace    *realScheduleTrace
	ctx      context.Context
	input    realScheduleInput
}

func newRealScheduleHost(t *testing.T, cold bool) *realScheduleHost {
	t.Helper()
	input := realScheduleOriginal(t)
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	h := &realScheduleHost{owner: cooperative.New(), trace: &realScheduleTrace{}, ctx: ctx, input: input}
	h.files = realScheduleFiles(h.trace)
	h.physical = &realScheduleColdIO{trace: h.trace}
	if cold {
		h.physical.held = make(chan struct{})
		h.physical.release = make(chan struct{})
	}
	// Existing helper constructs real CLI settings/global config + native Writer
	// with RealSchedule. This is not recordHost, which overrides the timer.
	h.writer = actualPendingWriter(t, h.owner, home, h.files)
	var e error
	h.state, e = bootstrapstate.New(h.owner, input.SessionID, home, func() (string, error) { return input.SessionID, nil })
	if e != nil {
		t.Fatal(e)
	}
	h.registry, e = runtimecleanup.New(h.owner)
	if e != nil {
		t.Fatal(e)
	}
	h.process, e = diagnostics.NewProcess(diagnostics.Options{Environment: func(string) string { return "" }, Now: time.Now, NowMillis: func() int64 { return time.Now().UnixMilli() }, PID: os.Getpid(), Platform: runtime.GOOS, Files: diagnostics.OSFileSystem{}})
	if e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]string{"HEAD": "ref: refs/heads/native-schedule\n", "refs/heads/native-schedule": "0000000000000000000000000000000000000000\n", "config": "[core]\nrepositoryformatversion = 0\n"} {
		p := filepath.Join(home, ".git", name)
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	watch := &gitbranch.NativePollWatch{Stat: os.Stat}
	git, e := gitbranch.New(gitbranch.Options{Owner: h.owner, CWD: func() string { return h.state.Snapshot().CWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanupOwned: func(scope *cooperative.Scope, fn func(*cooperative.Scope) error) error {
		_, e := h.registry.Register(scope, runtimecleanup.NewCallback(func(s *cooperative.Scope) (*runtimecleanup.Future, error) { return nil, fn(s) }))
		return e
	}})
	if e != nil {
		t.Fatal(e)
	}
	h.branch, e = NewOwnedGitRecordBranch(h.state, git)
	if e != nil {
		t.Fatal(e)
	}
	h.loader, e = sessionhistory.New(sessionhistory.Options{Files: h.physical, Stat: h.physical.Stat, ReadFile: h.physical.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if e != nil {
		t.Fatal(e)
	}
	pendingCall(t, h.owner, cooperative.Input, func(s *cooperative.Scope) error {
		h.writer.options.Bootstrap = h.state
		h.writer.options.SessionID = input.SessionID
		h.writer.options.OnError = func(e error) {
			if logError := h.process.Log("error", "native_schedule_writer_error", nil); logError != nil {
				t.Error(logError)
			}
			t.Error(e)
		}
		h.writer.options.Schedule = func(delay time.Duration, callback func()) func() {
			h.files.mu.Lock()
			h.files.scheduledAt = time.Now()
			h.files.mu.Unlock()
			h.trace.add("actual-RealSchedule-delegate", protocol.JSONField{Name: "delayNanoseconds", Value: raw(delay.Nanoseconds())})
			if delay != 100*time.Millisecond {
				t.Errorf("real source delay changed: %s", delay)
			}
			return RealSchedule(delay, callback)
		}
		var e error
		h.pending, e = NewPendingWriter(h.writer, h.owner)
		return e
	})
	if cold {
		if e = os.MkdirAll(filepath.Dir(h.writer.target()), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(h.writer.target(), []byte(input.Seed), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		h.files.unblock()
		h.physical.unblock()
		if e := h.pending.Close(context.Background()); e != nil {
			t.Error(e)
		}
		if e := git.Close(context.Background()); e != nil {
			t.Error(e)
		}
		watch.Join()
		h.owner.Close()
		cancel()
		h.files.mu.Lock()
		open := h.files.openFiles
		h.files.mu.Unlock()
		if open != 0 {
			t.Errorf("native descriptors retained: %d", open)
		}
		b, _ := json.Marshal(map[string]any{"PID": os.Getpid(), "HOME": os.Getenv("HOME"), "physicalWriterHome": home, "trace": h.trace.snapshot(), "nativeOpenFiles": open, "SourceBusiness": 0, "scope": "Go finite real schedule/Owner/Writer native causal proof; no Source parity or actual main/exit"})
		t.Log("REAL_SCHEDULE_RAW " + string(b))
	})
	return h
}
func (h *realScheduleHost) record(t *testing.T, message LiveRecord) *RecordFuture {
	t.Helper()
	var f *RecordFuture
	pendingCall(t, h.owner, cooperative.Input, func(s *cooperative.Scope) error {
		token, e := h.state.Context(s)
		if e != nil {
			return e
		}
		f, e = h.pending.BeginRecordLive(s, h.loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{message}, RecordOptions{PromptID: func() json.RawMessage { return nil }, CWDContext: token, Branch: func(next *cooperative.Scope) (RecordBranchFuture, error) { return h.branch.Begin(next, token) }})
		return e
	})
	return f
}
func (h *realScheduleHost) wait(t *testing.T, c <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-c:
	case <-h.ctx.Done():
		t.Fatal("external deadline: " + why)
	}
}
func (h *realScheduleHost) finishNatural(t *testing.T) string {
	t.Helper()
	h.files.mu.Lock()
	elapsed := h.files.appendOpenAt.Sub(h.files.scheduledAt)
	h.files.mu.Unlock()
	if elapsed < 100*time.Millisecond {
		t.Fatalf("native append started before real default 100ms timer: %s", elapsed)
	}
	h.trace.add("actual-native-schedule-to-append-open", protocol.JSONField{Name: "elapsedNanoseconds", Value: raw(elapsed.Nanoseconds())})
	h.files.unblock()
	h.wait(t, h.files.written, "real timer native append")
	h.wait(t, h.files.closed, "real native append close")
	if e := h.pending.WaitRuntimeNative(h.ctx); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(h.writer.target())
	if e != nil {
		t.Fatal(e)
	}
	h.trace.add("actual-raw-disk", protocol.JSONField{Name: "bytes", Value: quote(string(b))})
	return string(b)
}
func (h *realScheduleHost) mutateRaw(t *testing.T, m *protocol.JSONObject) {
	t.Helper()
	pendingCall(t, h.owner, cooperative.Input, func(*cooperative.Scope) error {
		api, ok := m.Object("message")
		if !ok {
			return errors.New("original assistant API missing")
		}
		blocks, ok := api.Array("content")
		if !ok {
			return errors.New("original content missing")
		}
		block, ok := blocks.Object(0)
		if !ok {
			return errors.New("original retained block missing")
		}
		block.Set("text", quote(h.input.LateText))
		usage, ok := api.Object("usage")
		if !ok {
			return errors.New("original usage missing")
		}
		usage.Set("output_tokens", raw(h.input.LateOutput))
		api.Set("stop_reason", quote("end_turn"))
		h.trace.add("original-owner-input-late-mutation")
		return nil
	})
}

func TestRealScheduleRecordQueueAndRawLateSnapshot(t *testing.T) {
	h := newRealScheduleHost(t, false)
	m := toolRecordObject(t, string(h.input.Assistant))
	f := h.record(t, LiveRecord{Record: m})
	if _, e := f.Wait(h.ctx); e != nil {
		t.Fatal(e)
	}
	// A real native completion gate prevents append, not Record fulfillment.
	h.wait(t, h.files.held, "natural 100ms append open")
	b, e := os.ReadFile(h.writer.target())
	if e != nil || len(b) != 0 {
		t.Fatalf("Record falsely served as disk barrier: %s/%v", b, e)
	}
	h.trace.add("awaitRecord-not-disk-barrier", protocol.JSONField{Name: "rawBytes", Value: quote(string(b))})
	h.mutateRaw(t, m)
	data := h.finishNatural(t)
	if !strings.Contains(data, `"text":"`+h.input.LateText+`"`) || !strings.Contains(data, fmt.Sprintf(`"output_tokens":%d`, h.input.LateOutput)) || !strings.Contains(data, `"stop_reason":"end_turn"`) || strings.Count(data, `"uuid":"`+m.String("uuid")+`"`) != 1 {
		t.Fatal("natural late snapshot/dedup differed", data)
	}
}
func TestRealScheduleProviderEnvelopeLateSnapshot(t *testing.T) {
	h := newRealScheduleHost(t, false)
	var assembler *streamhistory.Controller
	var envelope *streamhistory.Envelope
	if e := h.owner.Run(h.ctx, func(s *cooperative.Scope) error {
		var e error
		assembler, e = streamhistory.New(streamhistory.Options{UUID: func() string { return "30000000-0000-4000-8000-000000000003" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		if e != nil {
			return e
		}
		for _, event := range h.input.ProviderStart {
			if e = s.Frame(cooperative.FrameFeed, func() error {
				return assembler.Feed(event, streamhistory.Timing{}, func(update streamhistory.Update) error {
					if update.Envelope != nil {
						envelope = update.Envelope
					}
					return nil
				})
			}); e != nil {
				return e
			}
		}
		if envelope == nil {
			return errors.New("actual original provider events never yielded")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.record(t, LiveRecord{Assistant: envelope}).Wait(h.ctx); e != nil {
		t.Fatal(e)
	}
	h.wait(t, h.files.held, "natural live-envelope append open")
	if e := h.owner.Run(h.ctx, func(s *cooperative.Scope) error {
		envelope.Content[0].Object.Set("text", quote(h.input.LateText))
		envelope.Record.Set("lateOuter", raw(true))
		for _, event := range h.input.ProviderFinish {
			if e := s.Frame(cooperative.FrameFeed, func() error {
				return assembler.Feed(event, streamhistory.Timing{}, func(streamhistory.Update) error { return nil })
			}); e != nil {
				return e
			}
		}
		h.trace.add("actual-owner-assembler-late-usage")
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	data := h.finishNatural(t)
	if !strings.Contains(data, `"text":"`+h.input.LateText+`"`) || !strings.Contains(data, fmt.Sprintf(`"output_tokens":%d`, h.input.LateOutput)) || !strings.Contains(data, `"stop_reason":"end_turn"`) || strings.Contains(data, "lateOuter") {
		t.Fatal("actual live envelope snapshot differed", data)
	}
}
func TestRealScheduleColdReadFlushDoesNotJoinRecord(t *testing.T) {
	h := newRealScheduleHost(t, true)
	m := toolRecordObject(t, string(h.input.Assistant))
	f := h.record(t, LiveRecord{Record: m})
	h.wait(t, h.physical.held, "actual native cold UUID read completion")
	pendingCall(t, h.owner, cooperative.Read, func(*cooperative.Scope) error {
		if h.pending.tracked != 0 {
			return fmt.Errorf("unawaited cold Record incorrectly tracked: %d", h.pending.tracked)
		}
		h.trace.add("actual-cache-held-tracked-zero")
		return nil
	})
	if e := flushRecord(t, h.owner, h.pending).Wait(h.ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-f.Done():
		t.Fatal("Flush joined or prematurely settled unknown cold Record")
	default:
	}
	b, e := os.ReadFile(h.writer.target())
	if e != nil || string(b) != h.input.Seed {
		t.Fatalf("cold Flush changed original native bytes: %s/%v", b, e)
	}
	h.trace.add("actual-Flush-returned-cold-Record-pending")
	h.mutateRaw(t, m)
	h.physical.unblock()
	if _, e = f.Wait(h.ctx); e != nil {
		t.Fatal(e)
	}
	h.wait(t, h.files.held, "natural timer after actual cold Record completion")
	data := h.finishNatural(t)
	if !strings.Contains(data, `"text":"`+h.input.LateText+`"`) || !strings.Contains(data, fmt.Sprintf(`"output_tokens":%d`, h.input.LateOutput)) {
		t.Fatal("cold native continuation discarded original references", data)
	}
}
func TestRealScheduleCloseJoinsActualNativeDrain(t *testing.T) {
	h := newRealScheduleHost(t, false)
	m := toolRecordObject(t, string(h.input.Assistant))
	if _, e := h.record(t, LiveRecord{Record: m}).Wait(h.ctx); e != nil {
		t.Fatal(e)
	}
	returned := make(chan error, 1)
	go func() { returned <- h.pending.Close(h.ctx) }()
	h.wait(t, h.files.held, "Close actual native append-open")
	select {
	case e := <-returned:
		t.Fatalf("Close skipped actual held native descriptor: %v", e)
	default:
	}
	h.files.unblock()
	select {
	case e := <-returned:
		if e != nil {
			t.Fatal(e)
		}
	case <-h.ctx.Done():
		t.Fatal("Close external deadline")
	}
	if e := h.pending.WaitRuntimeNative(h.ctx); e != nil {
		t.Fatal(e)
	}
	pendingCall(t, h.owner, cooperative.Read, func(s *cooperative.Scope) error {
		if !h.pending.closing || h.pending.tracked != 0 {
			return errors.New("Close left admission/tracked state open")
		}
		_, e := h.pending.BeginRecordLive(s, h.loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: m}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if !errors.Is(e, ErrPendingClosed) {
			return fmt.Errorf("closed Record admitted: %v", e)
		}
		return nil
	})
	b, e := os.ReadFile(h.writer.target())
	if e != nil || strings.Count(string(b), `"uuid":"`+m.String("uuid")+`"`) != 1 {
		t.Fatalf("Close duplicated/dropped actual queued entry: %s/%v", b, e)
	}
	h.trace.add("actual-Close-settled-native-runtime-zero", protocol.JSONField{Name: "bytes", Value: quote(string(b))})
	// Administrative module Close is not real process exit/Source gracefulShutdown.
}

func TestRealScheduleNativeProgram(t *testing.T) {
	which := os.Getenv("CC_HAHA_REAL_SCHEDULE_CASE")
	if which == "" {
		t.Skip("only explicit frozen native child runner")
	}
	if os.Getenv("HOME") == "" || os.Getenv("USERPROFILE") != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(os.Getenv("HOME"), ".claude") {
		t.Fatal("native child private environment required")
	}
	switch which {
	case "raw-late":
		TestRealScheduleRecordQueueAndRawLateSnapshot(t)
	case "provider-late":
		TestRealScheduleProviderEnvelopeLateSnapshot(t)
	case "cold-flush":
		TestRealScheduleColdReadFlushDoesNotJoinRecord(t)
	case "close-native":
		TestRealScheduleCloseJoinsActualNativeDrain(t)
	default:
		t.Fatal("unknown original input selector", which)
	}
}
