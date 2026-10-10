package sessioncontroller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func bindRecordState(t *testing.T, owner *cooperative.Owner, writer *Writer) *bootstrapstate.State {
	t.Helper()
	state, e := bootstrapstate.New(owner, writer.options.SessionID, writer.options.CWD, func() (string, error) { return "B", nil })
	if e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.options.Bootstrap = state; return nil })
	return state
}
func readRows(t *testing.T, path string) []protocol.JSONObject {
	t.Helper()
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	rows := []protocol.JSONObject{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row protocol.JSONObject
		if e = json.Unmarshal([]byte(line), &row); e != nil {
			t.Fatal(e)
		}
		rows = append(rows, row)
	}
	return rows
}
func ownedAwaitRecord(t *testing.T, owner *cooperative.Owner, future *RecordFuture) {
	t.Helper()
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error { _, e := future.Await(scope); return e })
}
func ownedFlush(t *testing.T, owner *cooperative.Owner, pending *PendingWriter) {
	t.Helper()
	pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
		future, e := pending.Flush(scope)
		if e != nil {
			return e
		}
		return future.Await(scope)
	})
}
func TestBootstrapPersistenceTargetUsesLiveOriginalCWDWithoutRetargetingCachedPointer(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	originalTarget := writer.target()
	first := plainMessage("bootstrap-first", "original first")
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: first}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	var computed string
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if e := state.SetOriginalCWD(scope, filepath.Join(writer.options.CWD, "later-e\u0301")); e != nil {
			return e
		}
		var e error
		computed, e = writer.ComputedTranscriptPath(scope)
		return e
	})
	expected := filepath.Join(writer.options.ConfigDir, "projects", sanitize(filepath.Join(writer.options.CWD, "later-é")), state.SessionID()+".jsonl")
	if computed != expected || computed == originalTarget {
		t.Errorf("live original CWD not used: computed %s expected %s", computed, expected)
	}
	second := plainMessage("bootstrap-second", "original second")
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: second}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	if rows := readRows(t, originalTarget); len(rows) != 2 {
		t.Fatal("cached pointer retargeted", rows)
	}
	if _, e := os.Stat(computed); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("computed path unexpectedly materialized", e)
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		future, e := writer.BeginResetSessionFile(scope)
		if e != nil {
			return e
		}
		_, e = future.Await(scope)
		return e
	})
	third := plainMessage("bootstrap-third", "original third")
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: third}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	if rows := readRows(t, computed); len(rows) != 1 || rows[0].String("uuid") != "bootstrap-third" {
		t.Fatal("reset did not defer next pointer choice", rows)
	}
}

func TestBootstrapPersistenceAfterGitReadsLiveSIDSlugAndShellCWD(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	branch := &heldRecordBranch{done: make(chan struct{}), value: "original-branch"}
	entered := make(chan struct{})
	// Always release the native obligation before a failed assertion's cleanup.
	released := false
	defer func() {
		if !released {
			close(branch.done)
		}
	}()
	options := RecordOptions{PromptID: func() json.RawMessage { return nil }, Branch: func(scope *cooperative.Scope) (RecordBranchFuture, error) { close(entered); return branch, nil }}
	targetA := writer.target()
	message := plainMessage("bootstrap-late", "original message")
	future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, options)
	awaitSignal(t, entered, "actual Git await")
	lateCWD := filepath.Join(writer.options.CWD, "shell-e\u0301")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if e := state.SwitchSession(scope, "B", nil); e != nil {
			return e
		}
		cache, e := state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if e = cache.Set(scope, "B", "late-original-plan"); e != nil {
			return e
		}
		if e = state.SetOriginalCWD(scope, filepath.Join(writer.options.CWD, "new-original")); e != nil {
			return e
		}
		return state.SetCWD(scope, lateCWD)
	})
	close(branch.done)
	released = true
	ownedAwaitRecord(t, owner, future)
	ownedFlush(t, owner, pending)
	rows := readRows(t, targetA)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	row := rows[0]
	if row.String("sessionId") != "B" || row.String("slug") != "late-original-plan" || row.String("cwd") != filepath.Join(writer.options.CWD, "shell-é") || row.String("gitBranch") != "original-branch" {
		raw, _ := row.MarshalJSON()
		t.Fatalf("record captured admission state: %s", raw)
	}
	if writer.path != targetA {
		t.Fatal("late session change retargeted cached physical A file")
	}
}

func TestBootstrapPersistenceOverrideSurvivesReturnedFrameAndLaterDefaultChange(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	branch := &heldRecordBranch{done: make(chan struct{}), value: "HEAD"}
	entered := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(branch.done)
		}
	}()
	var future *RecordFuture
	override := filepath.Join(writer.options.CWD, "raw-e\u0301")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		base, e := state.Context(scope)
		if e != nil {
			return e
		}
		return base.RunWithOverride(scope, override, func(token *bootstrapstate.CWDContext) error {
			var e error
			future, e = pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: plainMessage("override-async", "original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, CWDContext: token, Branch: func(*cooperative.Scope) (RecordBranchFuture, error) { close(entered); return branch, nil }})
			return e
		})
	})
	awaitSignal(t, entered, "async descendant Git obligation")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		return state.SetCWD(scope, filepath.Join(writer.options.CWD, "default-later-e\u0301"))
	})
	close(branch.done)
	released = true
	ownedAwaitRecord(t, owner, future)
	ownedFlush(t, owner, pending)
	if row := readRows(t, writer.path)[0]; row.String("cwd") != override {
		t.Error("override lost after body returned", row.String("cwd"))
	}
	next := startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("default-async", "original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
	ownedAwaitRecord(t, owner, next)
	ownedFlush(t, owner, pending)
	if row := readRows(t, writer.path)[1]; row.String("cwd") != filepath.Join(writer.options.CWD, "default-later-é") {
		t.Error("override contaminated sibling", row.String("cwd"))
	}
}

func TestBootstrapPersistenceScopeGuardsAndForeignBindingsBeforeEffects(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	if _, e := writer.Record([]*protocol.JSONObject{plainMessage("reject", "original")}, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if _, e := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: plainMessage("reject", "original")}}, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if e := writer.Reset(); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if _, e := writer.ComputedTranscriptPath(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if _, e := writer.BeginResetSessionFile(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	if e := writer.ResetOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error(e)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	otherState, _ := bootstrapstate.New(foreign, "X", "/foreign", func() (string, error) { return "Y", nil })
	var otherCtx *bootstrapstate.CWDContext
	pendingCall(t, foreign, cooperative.Input, func(scope *cooperative.Scope) error { var e error; otherCtx, e = otherState.Context(scope); return e })
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if _, e := pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, []LiveRecord{{Record: plainMessage("reject", "original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }, CWDContext: otherCtx}); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	pendingCall(t, foreign, cooperative.Input, func(scope *cooperative.Scope) error {
		if _, e := NewPendingWriter(writer, foreign); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := writer.ComputedTranscriptPath(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := writer.BeginResetSessionFile(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := pending.BeginRecordLive(scope, loader, LiveRecordProfile{QuerySource: "sdk"}, nil, RecordOptions{}); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	if writer.path != "" || len(writer.pending) != 0 || len(writer.queues) != 0 || state.SessionID() != writer.options.SessionID {
		t.Error("guard mutated bound writer")
	}
}
