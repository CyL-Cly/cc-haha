package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestWorktreeStateEagerAppendStripsEphemeralFieldsAndDoesNotCreate(t *testing.T) {
	owner, writer, pending := makePending(t, OSFiles{})
	if e := beginPending(t, owner, pending).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	path := writer.Snapshot().String("path")
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	session := json.RawMessage(`{"usedSparsePaths":true,"future":2,"worktreeName":"w","originalCwd":"/private/original","worktreePath":"/private/original/.claude/worktrees/w","sessionId":"real","creationDurationMs":19}`)
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		future, e := pending.BeginSaveWorktreeState(scope, session)
		if e != nil {
			return e
		}
		// The original appendEntryToFile completes in this stack, before any
		// pending writer flush, subsequent tool, or SDK lifetime drain.
		select {
		case <-future.Done():
		default:
			t.Error("Source sync append deferred")
		}
		return nil
	})
	after, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	appended := after[len(before):]
	if bytes.Count(appended, []byte{'\n'}) != 1 || !bytes.Contains(appended, []byte(`"type":"worktree-state"`)) || bytes.Contains(appended, []byte("creationDurationMs")) || bytes.Contains(appended, []byte("usedSparsePaths")) || bytes.Contains(appended, []byte("future")) {
		t.Fatalf("exact eager state entry %s", appended)
	}
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Source fchmod", info, e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		_, e := pending.BeginSaveWorktreeState(scope, json.RawMessage("null"))
		return e
	})
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("missing transcript recreated", e)
	}
	// A new session pointer has no physical file. Its state is cached for later
	// materialization and must not create a metadata-only transcript.
	newPath := filepath.Join(filepath.Dir(path), "not-yet-created.jsonl")
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		writer.mu.Lock()
		writer.path = ""
		writer.mu.Unlock()
		_, e := pending.BeginSaveWorktreeState(scope, session)
		return e
	})
	if _, e = os.Stat(newPath); !os.IsNotExist(e) {
		t.Fatal("fresh transcript materialized", e)
	}
	var snapshot struct{ Metadata map[string]json.RawMessage }
	raw, _ := writer.Snapshot().MarshalJSON()
	_ = json.Unmarshal(raw, &snapshot)
	if !bytes.Contains(snapshot.Metadata["worktreeSession"], []byte(`"worktreeName":"w"`)) {
		t.Fatal("startup metadata state absent")
	}
}
