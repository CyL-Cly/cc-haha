package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
)

type queueOperationReturnObservation struct {
	Phase, Tracked, Buffered int
	Recording                string
}

func TestActualSourceBufferedQueueOperationKeepsTrackUntilAsyncBodyReturns(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-transcript-queue-operation-return.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_QUEUE_OPERATION_RETURN=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=" + home}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("whole original queue operation", err, string(output), stderr.String())
	}
	var source struct {
		Events    []queueOperationReturnObservation
		Persisted bool
		Blocked   []string
	}
	if err = json.Unmarshal(output, &source); err != nil || !source.Persisted || len(source.Blocked) != 0 || len(source.Events) != 5 {
		t.Fatal("whole original receipt", err, string(output))
	}
	if source.Events[0].Tracked != 1 || source.Events[1].Tracked != 0 || source.Events[0].Recording != "pending" || source.Events[3].Recording != "fulfilled" {
		t.Fatal("original async body/return changed", string(output))
	}
	t.Log("actual original buffered queue receipt", string(output))
	owner, writer, pending, loader := uuidHarness(t, "", nil, false)
	controller := &Controller{writer: writer, sid: writer.options.SessionID, options: Options{Now: func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }, ResumeOptions: resumestate.Options{Loader: loader}}}
	actual := []queueOperationReturnObservation{}
	observed := make(chan struct{})
	var probe func(*cooperative.Scope, int)
	probe = func(scope *cooperative.Scope, index int) {
		actual = append(actual, queueOperationReturnObservation{Phase: index, Tracked: pending.tracked, Buffered: len(writer.pending)})
		if index < 4 {
			if _, e := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, index+1); return nil }); e != nil {
				t.Error(e)
			}
		} else {
			close(observed)
		}
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := controller.logQueueOwned(scope, "enqueue", raw("actual buffered queue operation")); err != nil {
			return err
		}
		_, err := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, 0); return nil })
		return err
	})
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("native actual queue probe", ctx.Err())
	}
	want := append([]queueOperationReturnObservation(nil), source.Events...)
	for i := range want {
		want[i].Recording = ""
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("actual logQueueOwned return boundary differs: Source=%+v Go=%+v", want, actual)
	}
	// Also compare the returned Promise, although the real queue logger ignores
	// it. Its body/finally/insertion/outer-await jobs share this same Owner.
	t.Run("returned-original-promise", func(t *testing.T) {
		owner, writer, pending, loader := uuidHarness(t, "", nil, false)
		var returned *WriteFuture
		actual := []queueOperationReturnObservation{}
		observed := make(chan struct{})
		var probe func(*cooperative.Scope, int)
		probe = func(scope *cooperative.Scope, phase int) {
			status := "pending"
			select {
			case <-returned.Done():
				status = "fulfilled"
			default:
			}
			actual = append(actual, queueOperationReturnObservation{Phase: phase, Tracked: pending.tracked, Buffered: len(writer.pending), Recording: status})
			if phase < 4 {
				if _, e := owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, phase+1); return nil }); e != nil {
					t.Error(e)
				}
			} else {
				close(observed)
			}
		}
		pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
			entry := object(raw(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "actual returned queue operation", "sessionId": writer.options.SessionID}))
			var e error
			returned, e = pending.BeginQueueOperation(scope, loader, &entry)
			if e != nil {
				return e
			}
			_, e = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error { probe(next, 0); return nil })
			return e
		})
		select {
		case <-observed:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if !reflect.DeepEqual(actual, source.Events) {
			t.Fatal("original recordQueueOperation Promise graph differs", source.Events, actual)
		}
		if err := returned.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	})
	var recording *RecordFuture
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		var e error
		recording, e = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: plainMessage("native-queue-materialize", "actual queue materialization message")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		return e
	})
	if _, err := recording.Wait(ctx); err != nil {
		t.Fatal("actual materialization", err)
	}
	if err := beginTrackedFlush(t, owner, pending).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(writer.target())
	if err != nil || !strings.Contains(string(disk), "actual buffered queue operation") || !strings.Contains(string(disk), "actual queue materialization message") {
		t.Fatal("actual private queue and authored row missing", err, string(disk))
	}
}

func TestQueueOperationReturnRejectsForeignMissingAndClosedAdmission(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, "", nil, false)
	foreign := cooperative.New()
	defer foreign.Close()
	entry := object(raw(map[string]any{"type": "queue-operation", "operation": "enqueue", "sessionId": writer.options.SessionID}))
	if _, err := pending.BeginQueueOperation(nil, loader, &entry); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil scope admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := pending.BeginQueueOperation(scope, loader, &entry); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign scope admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		wrong := plainMessage("foreign-kind", "not a queue operation")
		for _, input := range []*protocol.JSONObject{nil, wrong} {
			if _, err := pending.BeginQueueOperation(scope, loader, input); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("missing/original kind guard bypassed", err)
			}
		}
		if _, err := pending.BeginQueueOperation(scope, nil, &entry); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("missing history loader admitted", err)
		}
		if len(writer.pending) != 0 || pending.tracked != 0 {
			t.Error("invalid caller changed actual queue", len(writer.pending), pending.tracked)
		}
		pending.closing = true
		if _, err := pending.BeginQueueOperation(scope, loader, &entry); !errors.Is(err, ErrPendingClosed) {
			t.Error("closed admission accepted", err)
		}
		pending.closing = false
		return nil
	})
}

func TestQueueOperationReturnOwnerCloseRejectsObserverWithoutRunningFinally(t *testing.T) {
	io := &uuidIO{kind: "stat", entered: make(chan struct{}), released: make(chan struct{})}
	owner, writer, pending, loader := uuidHarness(t, uuidOld, io, false)
	defer close(io.released)
	writer.AdoptSessionFile()
	var returned *WriteFuture
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		entry := object(raw(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "owner close pending queue", "sessionId": writer.options.SessionID}))
		var err error
		returned, err = pending.BeginQueueOperation(scope, loader, &entry)
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	select {
	case <-io.entered:
	case <-ctx.Done():
		t.Fatal("real private stat did not begin", ctx.Err())
	}
	owner.Close()
	if err := returned.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("close stranded or resolved original queue observation", err)
	}
	if pending.tracked != 1 {
		t.Fatal("administrative watcher ran business finally off Owner", pending.tracked)
	}
}
