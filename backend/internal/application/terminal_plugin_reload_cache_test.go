package application

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginReloadPreservesActualReadCacheLiveHistoryAndTranscriptPrefix(t *testing.T) {
	testTerminalLocalAppendPreservesActualReadCache(t, "reload-plugins")
}
func TestTerminalMobileCompletionPreservesActualReadCacheLiveHistoryAndTranscriptPrefix(t *testing.T) {
	testTerminalLocalAppendPreservesActualReadCache(t, "mobile")
}
func testTerminalLocalAppendPreservesActualReadCache(t *testing.T, command string) {
	stdout := "<local-command-stdout>Reloaded:"
	if command == "mobile" {
		stdout = "<local-command-stdout>(no content)</local-command-stdout>"
	}
	var calls atomic.Int32
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			response(w, "reload-read", "Read", map[string]any{"file_path": path}, "")
		case 2:
			if !strings.Contains(string(body), "ACTUAL_READ_CACHE_CONTENT") {
				t.Error("actual Read did not reach the next model request")
			}
			response(w, "reload-read-done", "", nil, "READ_CACHE_BEFORE_RELOAD_DONE")
		case 3:
			if !strings.Contains(string(body), stdout) || !strings.Contains(string(body), "READ_CACHE_BEFORE_RELOAD_DONE") {
				t.Error("local replay or prior live history disappeared")
			}
			response(w, "reload-next-human", "", nil, "RELOAD_CACHE_NEXT_HUMAN_DONE")
		default:
			t.Error("local reload started another model")
			response(w, "unexpected", "", nil, "unexpected")
		}
	}))
	defer server.Close()
	o := nativeSessionOptions(t, server.URL)
	o.Interactive = true
	path = filepath.Join(o.WorkDir, "read-before-reload.txt")
	if err := os.WriteFile(path, []byte("ACTUAL_READ_CACHE_CONTENT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "Read the actual file first"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// The controller's read cache is reconstructed from the real persisted
	// transcript on cold resume; live tools use the native shared file cache.
	id := s.process.Controller.SessionID()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.Arguments.SessionID = ""
	o.Arguments.Resume = id
	o.Native, err = newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: o.Environment, CWD: o.WorkDir, Platform: o.Platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	s, err = NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	before := s.process.Controller.ReadCache().Entries()
	if len(before) == 0 {
		t.Fatal("actual Read never created cache state")
	}
	var transcript string
	err = filepath.WalkDir(o.Environment["CLAUDE_CONFIG_DIR"], func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == id+".jsonl" {
			transcript = path
		}
		return nil
	})
	if err != nil || transcript == "" {
		t.Fatal("actual transcript not persisted", err, transcript)
	}
	prefix, err := os.ReadFile(transcript)
	if err != nil || len(prefix) == 0 {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	if handled, err := s.InvokeTerminalCommand(context.Background(), command, "kept args", func(event tui.Event) error {
		if event.Kind == tui.LocalPanelOpened {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}); err != nil || !handled {
		t.Fatal(handled, err)
	}
	if command == "mobile" {
		if panel == nil {
			t.Fatal("Actual mobile panel missing")
		}
		if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "mobile-close"}, func(tui.Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if after := s.process.Controller.ReadCache().Entries(); !reflect.DeepEqual(before, after) {
		t.Fatal("reload cleared actual Read cache", string(raw(before)), string(raw(after)))
	}
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "Continue after local reload"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("local reload changed actual model cardinality", calls.Load())
	}
	// Local records use the ordinary pending writer. Close is its physical
	// drain boundary, rather than assuming RecordLive bypasses that queue.
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(transcript)
	if err != nil || len(after) <= len(prefix) || !bytes.HasPrefix(after, prefix) || !bytes.Contains(after[len(prefix):], []byte(stdout)) {
		t.Fatal("reload failed to append through the actual writer", err, len(prefix), len(after))
	}
}
