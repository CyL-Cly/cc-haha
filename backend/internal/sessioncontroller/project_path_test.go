package sessioncontroller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestNativeLongProjectMaterializationKeepsCachedPathUntilActualReset(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	originalA := filepath.Join(writer.options.CWD, strings.Repeat("A", 240))
	// The original setter, rather than the path helper, owns NFC normalization.
	inputB := filepath.Join(writer.options.CWD, strings.Repeat("e\u0301", 50)+strings.Repeat("n", 180))
	originalB := filepath.Join(writer.options.CWD, strings.Repeat("é", 50)+strings.Repeat("n", 180))
	var firstPath, secondPath string
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := state.SetOriginalCWD(scope, originalA); err != nil {
			return err
		}
		var err error
		firstPath, err = writer.ComputedTranscriptPath(scope)
		return err
	})
	if len(filepath.Base(filepath.Dir(firstPath))) <= 200 {
		t.Fatal("original long target lost suffix", firstPath)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("computed path must not create native file", err)
	}
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("long-first", "first original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := state.SetOriginalCWD(scope, inputB); err != nil {
			return err
		}
		var err error
		secondPath, err = writer.ComputedTranscriptPath(scope)
		return err
	})
	if state.Snapshot().OriginalCWD != originalB || secondPath != filepath.Join(writer.options.ConfigDir, "projects", projectpath.Sanitize(originalB), state.SessionID()+".jsonl") {
		t.Fatal("actual NFC setter/derived target mismatch", state.Snapshot().OriginalCWD, secondPath)
	}
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("long-second", "cached original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	if rows := readRows(t, firstPath); len(rows) != 2 || rows[1].String("uuid") != "long-second" {
		t.Fatal("cached target was prematurely redirected", rows)
	}
	if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncached target materialized before reset", err)
	}
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		future, err := writer.BeginResetSessionFile(scope)
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	})
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("long-third", "reset original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	if rows := readRows(t, secondPath); len(rows) != 1 || rows[0].String("uuid") != "long-third" {
		t.Fatal("reset did not use new hashed directory", rows)
	}
	for _, path := range []string{firstPath, secondPath} {
		file, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if file.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
			t.Fatal("native private modes", file.Mode(), dir.Mode())
		}
	}
}

func TestNativeExplicitProjectDirectoryOverridesLongCWDThenNullReset(t *testing.T) {
	owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
	state := bindRecordState(t, owner, writer)
	original := filepath.Join(writer.options.CWD, strings.Repeat("X", 240))
	dir := filepath.Join(writer.options.ConfigDir, "explicit-original-project")
	var explicit, derived string
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := state.SetOriginalCWD(scope, original); err != nil {
			return err
		}
		if err := state.SwitchSession(scope, "original-SID", &dir); err != nil {
			return err
		}
		var err error
		explicit, err = writer.ComputedTranscriptPath(scope)
		return err
	})
	if explicit != filepath.Join(dir, "original-SID.jsonl") {
		t.Fatal("explicit project priority changed", explicit)
	}
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("explicit-first", "explicit original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := state.SwitchSession(scope, "original-SID", nil); err != nil {
			return err
		}
		future, err := writer.BeginResetSessionFile(scope)
		if err != nil {
			return err
		}
		if _, err = future.Await(scope); err != nil {
			return err
		}
		derived, err = writer.ComputedTranscriptPath(scope)
		return err
	})
	ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("derived-second", "derived original")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
	ownedFlush(t, owner, pending)
	if explicit == derived || filepath.Dir(derived) == dir || len(filepath.Base(filepath.Dir(derived))) <= 200 {
		t.Fatal("null reset retained explicit path or lost suffix", derived)
	}
	if rows := readRows(t, explicit); len(rows) != 1 || rows[0].String("uuid") != "explicit-first" {
		t.Fatal("original explicit file changed", rows)
	}
	if rows := readRows(t, derived); len(rows) != 1 || rows[0].String("uuid") != "derived-second" {
		t.Fatal("new original derived file not materialized", rows)
	}
}

func TestWriterAcceptsOriginalLongConstructorCWD(t *testing.T) {
	owner, writer, _, _ := recordHost(t, OSFiles{}, nil)
	options := writer.options
	options.CWD = filepath.Join(options.CWD, strings.Repeat("Q", 240))
	long, err := NewWriter(options)
	if err != nil {
		t.Fatal("actual long constructor still rejected", err)
	}
	if !strings.Contains(long.target(), projectpath.Sanitize(options.CWD)) || len(filepath.Base(filepath.Dir(long.target()))) <= 200 {
		t.Fatal("constructor target was not hashed", long.target())
	}
	_ = owner
}
