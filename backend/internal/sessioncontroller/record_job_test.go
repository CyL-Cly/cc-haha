package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func recordLoader(t *testing.T, physical *uuidIO) *sessionhistory.Loader {
	t.Helper()
	if physical == nil {
		physical = &uuidIO{}
	}
	physical.counts = map[string]int{}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: physical, Stat: physical.Stat, ReadFile: physical.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	return loader
}
func recordHost(t *testing.T, files TranscriptFiles, physical *uuidIO) (*cooperative.Owner, *Writer, *PendingWriter, *sessionhistory.Loader) {
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
	t.Cleanup(func() { _ = pending.Close(context.Background()); owner.Close() })
	return owner, writer, pending, recordLoader(t, physical)
}
func startRecord(t *testing.T, owner *cooperative.Owner, pending *PendingWriter, loader *sessionhistory.Loader, messages []LiveRecord, options RecordOptions) *RecordFuture {
	t.Helper()
	var future *RecordFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		var err error
		future, err = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, messages, options)
		return err
	})
	return future
}
func flushRecord(t *testing.T, owner *cooperative.Owner, pending *PendingWriter) *FlushFuture {
	t.Helper()
	var future *FlushFuture
	pendingCall(t, owner, cooperative.Flush, func(scope *cooperative.Scope) error { var err error; future, err = pending.Flush(scope); return err })
	return future
}
func recordBytes(t *testing.T, writer *Writer) string {
	t.Helper()
	b, e := os.ReadFile(writer.target())
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestIndependentRecordColdCacheIsUntrackedAndUsesLateOriginalOuter(t *testing.T) {
	physical := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
	owner, writer, pending, loader := recordHost(t, OSFiles{}, physical)
	if e := os.MkdirAll(filepath.Dir(writer.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(writer.target(), []byte(uuidOld), 0600); e != nil {
		t.Fatal(e)
	}
	message := plainMessage("new-real", "first\n原始 prompt")
	prompt := quote("before-ASK")
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return prompt }})
	awaitSignal(t, physical.entered, "actual cold UUID read")
	if err := flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		if pending.tracked != 0 {
			return fmt.Errorf("outer cache await tracked %d", pending.tracked)
		}
		message.Set("lateOuter", quote("owner input after cache start"))
		prompt = quote("later-ASK")
		return nil
	})
	select {
	case <-future.Done():
		t.Fatal("outer Record settled before cold native read")
	default:
	}
	close(physical.released)
	uuid, err := future.Wait(context.Background())
	if err != nil || text(uuid) != "new-real" {
		t.Fatalf("Record %s/%v", uuid, err)
	}
	if err := flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	data := recordBytes(t, writer)
	if !strings.Contains(data, `"lateOuter":"owner input after cache start"`) || !strings.Contains(data, `"promptId":"later-ASK"`) {
		t.Fatal(data)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		cache, ok, e := pending.LookupSessionUUIDCache(scope, writer.options.SessionID)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("cache absent")
		}
		set, e := cache.Await(scope)
		if e != nil {
			return e
		}
		found, e := set.OwnerHas(scope, quote("new-real"))
		if e != nil {
			return e
		}
		if !found || len(writer.members) != 0 || pending.tracked != 0 {
			return fmt.Errorf("actual Set/legacy members/tracked %v/%d/%d", found, len(writer.members), pending.tracked)
		}
		return nil
	})
}

func TestIndependentRecordPrepareTrackedFinallySurvivesObserverCancellation(t *testing.T) {
	for _, fault := range []string{"", "stat-close"} {
		t.Run(fault, func(t *testing.T) {
			files := newPreparationFiles("stat", fault)
			owner, writer, pending, loader := recordHost(t, files, nil)
			t.Cleanup(files.unblock)
			message := plainMessage("during-prepare", "actual prompt")
			future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
			awaitSignal(t, files.held, "real prepare fstat")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := future.Wait(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				if pending.tracked != 1 {
					return fmt.Errorf("insert count %d", pending.tracked)
				}
				message.Set("lateOuter", raw(true))
				return nil
			})
			flush := flushRecord(t, owner, pending)
			select {
			case <-flush.Done():
				t.Fatal("tracked flush skipped held prepare")
			default:
			}
			// This external held fstat has ordinal 1, like the original probes.
			// Its next independent prepare must remain a real second fstat.
			files.hold = ""
			files.unblock()
			_, err := future.Wait(context.Background())
			if fault == "stat-close" {
				if !errors.Is(err, prepareCloseFault) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if err := flush.Wait(context.Background()); err != nil {
				t.Fatalf("Flush copied outer Record error %v", err)
			}
			pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
				if pending.tracked != 0 {
					return fmt.Errorf("finally count %d", pending.tracked)
				}
				return nil
			})
			if fault == "" {
				data := recordBytes(t, writer)
				if !strings.Contains(data, `"lateOuter":true`) {
					t.Fatal(data)
				}
				files.mu.Lock()
				calls := len(files.flags)
				files.mu.Unlock()
				if calls != 3 {
					t.Fatalf("two original prepares + real drain opens: %d", calls)
				}
			}
		})
	}
}

type heldRecordBranch struct {
	done  chan struct{}
	value string
	err   error
}

func (f *heldRecordBranch) Done() <-chan struct{} { return f.done }
func (f *heldRecordBranch) Wait(ctx context.Context) (string, error) {
	select {
	case <-f.done:
		return f.value, f.err
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}
func TestIndependentRecordGitAwaitKeepsOriginalReferenceAndSingleBranch(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	branch := &heldRecordBranch{done: make(chan struct{}), value: "actual-branch"}
	entered := make(chan struct{})
	calls := 0
	user := plainMessage("git-old-id", "prompt before await")
	prompt := quote("early")
	options := RecordOptions{PromptID: func() json.RawMessage { return prompt }, Branch: func(scope *cooperative.Scope) (RecordBranchFuture, error) {
		if !scope.BelongsTo(owner) {
			return nil, cooperative.ErrRestricted
		}
		calls++
		close(entered)
		return branch, nil
	}}
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: user}}, options)
	awaitSignal(t, entered, "Git capability admission")
	flush := flushRecord(t, owner, pending)
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		user.Set("uuid", quote("git-late-id"))
		user.Set("lateOuter", quote("after Git start"))
		prompt = quote("late")
		return nil
	})
	select {
	case <-future.Done():
		t.Fatal("Record skipped original Git future")
	default:
	}
	close(branch.done)
	if id, e := future.Wait(context.Background()); e != nil || text(id) != "git-late-id" {
		t.Fatalf("%s/%v", id, e)
	}
	if e := flush.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, writer)
	if !strings.Contains(data, `"uuid":"git-late-id"`) || !strings.Contains(data, `"gitBranch":"actual-branch"`) || !strings.Contains(data, `"promptId":"late"`) || calls != 1 {
		t.Fatal(data, calls)
	}
}

func TestIndependentRecordPrefixCompactHintAndOrdinaryGitFailure(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	first := plainMessage("prefix", "base")
	next := plainMessage("next", "fresh")
	options := RecordOptions{PromptID: func() json.RawMessage { return quote("turn") }}
	if _, e := startRecord(t, owner, pending, loader, []LiveRecord{{Record: first}}, options).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	compact := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("compact_boundary")}, protocol.JSONField{Name: "uuid", Value: quote("compact")})
	ready := make(chan struct{})
	close(ready)
	options.Branch = func(*cooperative.Scope) (RecordBranchFuture, error) {
		return &heldRecordBranch{done: ready, err: os.ErrPermission}, nil
	}
	if id, e := startRecord(t, owner, pending, loader, []LiveRecord{{Record: &compact}, {Record: first}, {Record: next}}, options).Wait(context.Background()); e != nil || text(id) != "next" {
		t.Fatalf("%s/%v", id, e)
	}
	options.StartingParentUUID = quote("next")
	if id, e := startRecord(t, owner, pending, loader, []LiveRecord{{Record: first}, {Record: next}}, options).Wait(context.Background()); e != nil || text(id) != "next" {
		t.Fatalf("prefix %s/%v", id, e)
	}
	if e := flushRecord(t, owner, pending).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, writer)
	if !strings.Contains(data, `"parentUuid":null,"logicalParentUuid":null,"isSidechain":false,"type":"system"`) || !strings.Contains(data, `"parentUuid":"compact","isSidechain":false,"promptId":"turn","type":"user","uuid":"next"`) {
		t.Fatal(data)
	}
	lines := strings.Split(strings.TrimSpace(data), "\n")
	if len(lines) != 3 || strings.Contains(lines[2], `"gitBranch"`) {
		t.Fatal(data)
	}
}

func TestIndependentRecordActualProviderInnerStaysLiveAtDrain(t *testing.T) {
	files := &heldFiles{mode: "stat", arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	owner, writer, pending, loader := recordHost(t, files, nil)
	t.Cleanup(func() {
		select {
		case <-files.release:
		default:
			close(files.release)
		}
	})
	var controller *streamhistory.Controller
	var envelope *streamhistory.Envelope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		controller, err = streamhistory.New(streamhistory.Options{UUID: func() string { return "actual-live" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		if err != nil {
			return err
		}
		// Text-only streams retain completed envelopes until Finish. A real
		// server-tool boundary drains them before later message_delta usage.
		for _, event := range []string{`{"type":"message_start","message":{"id":"m","role":"assistant","model":"fixture","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"original partial"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"server","name":"search"}}`, `{"type":"content_block_stop","index":1}`} {
			if err = scope.Frame(cooperative.FrameFeed, func() error {
				return controller.Feed(json.RawMessage(event), streamhistory.Timing{}, func(update streamhistory.Update) error {
					if update.Envelope != nil {
						envelope = update.Envelope
					}
					return nil
				})
			}); err != nil {
				return err
			}
		}
		if envelope == nil {
			return errors.New("provider never yielded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Assistant: envelope}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	if _, e := future.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	close(files.arm)
	flush := flushRecord(t, owner, pending)
	awaitSignal(t, files.held, "real lazy drain fstat")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		envelope.Record.Set("tooLateOuter", raw(true))
		for _, event := range []string{`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`, `{"type":"message_stop"}`} {
			if err := scope.Frame(cooperative.FrameFeed, func() error {
				return controller.Feed(json.RawMessage(event), streamhistory.Timing{}, func(streamhistory.Update) error { return nil })
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(files.release)
	if e := flush.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	data := recordBytes(t, writer)
	if !strings.Contains(data, `"output_tokens":9`) || !strings.Contains(data, `"stop_reason":"end_turn"`) || strings.Contains(data, `tooLateOuter`) {
		t.Fatal(data)
	}
}

func TestIndependentRecordExcludedPolicyIsSessionScoped(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	message := plainMessage("same-id", "prompt")
	options := RecordOptions{PromptID: func() json.RawMessage { return nil }}
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.options.SessionPersistenceDisabled = true; return nil })
	if id, e := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, options).Wait(context.Background()); e != nil || string(id) != "null" {
		t.Fatalf("%s/%v", id, e)
	}
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		writer.options.SessionPersistenceDisabled = false
		writer.Switch("actual-second-session", "")
		writer.Reset()
		return nil
	})
	if id, e := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, options).Wait(context.Background()); e != nil || text(id) != "same-id" {
		t.Fatalf("new SID exclusions %s/%v", id, e)
	}
	if e := flushRecord(t, owner, pending).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(recordBytes(t, writer), `"sessionId":"actual-second-session"`) {
		t.Fatal("wrong target session")
	}
}

// Compile-time assertion preserves the real OS loader interface; no prepared
// history or Source-selected UUIDs are supplied to the independent recorder.
var _ sessionstore.FileSystem = sessionstore.OSFileSystem{}

func TestIndependentRecordCancelledASKAwaitLeavesFinallyAndNativeJobAlive(t *testing.T) {
	files := newPreparationFiles("stat", "")
	owner, writer, pending, loader := recordHost(t, files, nil)
	t.Cleanup(files.unblock)
	message := plainMessage("cancelled-real-ask", "original prompt")
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	awaitSignal(t, files.held, "actual independently admitted fstat")
	cause := errors.New("original ASK stopped observing Record")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(cause)
	waiting, returned := make(chan struct{}), make(chan error, 1)
	go func() {
		returned <- owner.Run(ctx, func(scope *cooperative.Scope) error { close(waiting); _, err := future.Await(scope); return err })
	}()
	awaitSignal(t, waiting, "actual ASK await")
	cancel(cause)
	select {
	case err := <-returned:
		if !errors.Is(err, cause) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ASK cancellation incorrectly joined independent file job")
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.tracked != 1 {
			return fmt.Errorf("tracked job lost before physical completion: %d", pending.tracked)
		}
		files.hold = ""
		return nil
	})
	files.unblock()
	if _, err := future.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if pending.tracked != 0 {
			return fmt.Errorf("independent finally did not run: %d", pending.tracked)
		}
		return nil
	})
	data := recordBytes(t, writer)
	if strings.Count(data, `"uuid":"cancelled-real-ask"`) != 1 {
		t.Fatal("cancelled ASK lost/duplicated actual Record", data)
	}
}
