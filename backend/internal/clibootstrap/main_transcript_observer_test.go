package clibootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalMutationObserverRunsAfterActualAppendClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actual.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	wrapped := &mainCommittedTranscriptFile{TranscriptFile: file, path: path, committed: func(got string) error {
		called++
		if got != path {
			t.Error("different canonical file notified", got)
		}
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Error("notification preceded physical close", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "{\"type\":\"user\"}\n" {
			t.Error("notification preceded actual bytes", string(data), err)
		}
		return nil
	}}
	for _, data := range []string{"{\"type\":", "\"user\"}\n"} {
		if _, err := wrapped.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if called != 0 {
		t.Fatal("write admission announced a committed transcript")
	}
	if err := wrapped.Close(); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("actual commit not announced", called)
	}
	_ = wrapped.Close()
	if called != 1 {
		t.Fatal("duplicate close announced duplicate commit", called)
	}
}

func TestProbeOrFailedAppendDoesNotAnnounceCommittedTranscript(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		file, err := os.CreateTemp(t.TempDir(), "probe-")
		if err != nil {
			t.Fatal(err)
		}
		called := false
		wrapped := &mainCommittedTranscriptFile{TranscriptFile: file, path: file.Name(), committed: func(string) error { called = true; return nil }}
		if failWrite {
			_ = file.Close()
			if _, err := wrapped.Write([]byte("uncommitted")); err == nil {
				t.Fatal("missing actual write failure")
			}
		}
		_ = wrapped.Close()
		if called {
			t.Fatal("probe/failure announced a committed transcript", failWrite)
		}
	}
}
