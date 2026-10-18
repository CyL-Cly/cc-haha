package sessioncontroller

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

func TestNativeParentClearKeepsAlreadyAdmittedPhysicalAppendTarget(t *testing.T) {
	parentOwner, childOwner := cooperative.New(), cooperative.New()
	defer parentOwner.Close()
	defer childOwner.Close()
	home := t.TempDir()
	parent, _ := bootstrapstate.New(parentOwner, "original-parent", home, func() (string, error) { return "new-parent", nil })
	child, _ := bootstrapstate.New(childOwner, "original-parent", home, func() (string, error) { return "child", nil })
	files := &appendIO{hold: "open", entered: make(chan struct{}), released: make(chan struct{}), counts: map[string]int{}}
	var released sync.Once
	defer released.Do(func() { close(files.released) })
	writer := actualPendingWriter(t, childOwner, home, files)
	var pending *PendingWriter
	pendingCall(t, childOwner, cooperative.Input, func(scope *cooperative.Scope) error {
		cap, err := bootstrapstate.BindNativeParentClientState(scope, child, parent)
		if err != nil {
			return err
		}
		writer.options.Bootstrap, writer.options.SessionID, writer.options.AgentID = child, child.SessionID(), "retained-child"
		writer.nativeParentSession = &nativeParentSessionSnapshot{child: child, parent: parent, client: cap}
		pending, err = NewPendingWriter(writer, childOwner)
		return err
	})
	defer pending.Close(context.Background())
	oldTarget := writer.target()
	if err := os.MkdirAll(filepath.Dir(oldTarget), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldTarget, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, childOwner, cooperative.Read, func(*cooperative.Scope) error { writer.AdoptSessionFile(); return nil })
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: files.StatSize, ReadFile: files.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	files.armed = true
	files.mu.Unlock()
	oldEntry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: quote("ALREADY_ADMITTED_ORIGINAL_TARGET")}, protocol.JSONField{Name: "sessionId", Value: quote(parent.SessionID())})
	oldFuture := beginAppend(t, childOwner, pending, loader, &oldEntry)
	awaitSignal(t, files.entered, "actual original child append descriptor")
	pendingCall(t, parentOwner, cooperative.Input, func(scope *cooperative.Scope) error { _, err := parent.RegenerateSessionID(scope, true); return err })
	pendingCall(t, childOwner, cooperative.Read, func(scope *cooperative.Scope) error { return writer.refreshNativeParentSessionOnOwner(scope) })
	newTarget := writer.target()
	if oldTarget == newTarget {
		t.Fatal("Parent clear failed to select the next actual child target")
	}
	// The open descriptor and admitted path still belong to the old Append.
	// Regeneration changes future records, not already-started physical work.
	released.Do(func() { close(files.released) })
	if err = oldFuture.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	var flush *FlushFuture
	pendingCall(t, childOwner, cooperative.Flush, func(scope *cooperative.Scope) error { var e error; flush, e = pending.Flush(scope); return e })
	if err = flush.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(oldTarget)
	if err != nil || !bytes.Contains(oldBytes, []byte("ALREADY_ADMITTED_ORIGINAL_TARGET")) {
		t.Fatal("Admitted append migrated or disappeared", err, string(oldBytes))
	}
	if _, err = os.Stat(newTarget); !os.IsNotExist(err) {
		t.Fatal("Admitted old append created the new transcript", err)
	}
	if err = os.MkdirAll(filepath.Dir(newTarget), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(newTarget, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, childOwner, cooperative.Read, func(*cooperative.Scope) error { writer.AdoptSessionFile(); return nil })
	newEntry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: quote("NEXT_ACTUAL_NEW_TARGET")}, protocol.JSONField{Name: "sessionId", Value: quote(parent.SessionID())})
	newFuture := beginAppend(t, childOwner, pending, loader, &newEntry)
	if err = newFuture.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, childOwner, cooperative.Flush, func(scope *cooperative.Scope) error { var e error; flush, e = pending.Flush(scope); return e })
	if err = flush.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	newBytes, err := os.ReadFile(newTarget)
	afterOld, oldErr := os.ReadFile(oldTarget)
	if err != nil || oldErr != nil || !bytes.Contains(newBytes, []byte("NEXT_ACTUAL_NEW_TARGET")) || bytes.Contains(newBytes, []byte("ALREADY_ADMITTED_ORIGINAL_TARGET")) || !bytes.Equal(oldBytes, afterOld) {
		t.Fatal("Next child append lost new target or mutated old transcript", err, oldErr, string(newBytes), string(afterOld))
	}
}
