package server

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// Only the file transport is faulted; the recovery reducer, SQLite scratch
// state, history admission lane and HTTP handler all execute normally.
type recoveryFaultFiles struct {
	entered, release          chan struct{}
	once                      sync.Once
	readFailure, closeFailure error
	closed                    atomic.Int32
}
type recoveryFaultFile struct {
	sessionstore.File
	owner *recoveryFaultFiles
}

func (f *recoveryFaultFiles) Open(path string) (sessionstore.File, error) {
	file, err := (sessionstore.OSFileSystem{}).Open(path)
	if err != nil {
		return nil, err
	}
	return &recoveryFaultFile{file, f}, nil
}
func (f *recoveryFaultFile) ReadAt(p []byte, offset int64) (int, error) {
	if f.owner.entered != nil {
		f.owner.once.Do(func() { close(f.owner.entered); <-f.owner.release })
	}
	if f.owner.readFailure != nil {
		return 0, f.owner.readFailure
	}
	return f.File.ReadAt(p, offset)
}
func (f *recoveryFaultFile) Close() error {
	err := f.File.Close()
	f.owner.closed.Add(1)
	if f.owner.closeFailure != nil {
		return f.owner.closeFailure
	}
	return err
}

func recoveryLifecycleServer(t *testing.T) (*Server, sessionInfo, string, []byte) {
	t.Helper()
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Error("History recovery must not invoke a model")
		return nil, errors.New("unexpected model startup")
	})
	scratch := filepath.Join(o.HomeDir, "scratch")
	if err := os.MkdirAll(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	project := filepath.Join(o.ConfigDir, "projects", "private")
	if err = os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	data := []byte(`{"type":"user","uuid":"human","timestamp":"2026-10-07T00:00:00Z","message":{"role":"user","content":"continue original task"}}` + "\n")
	path := filepath.Join(project, id+".jsonl")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := s.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s, info, scratch, data
}

func TestHistoryRecoveryCancellationClosesReaderScratchAndDoesNotPublishCache(t *testing.T) {
	s, info, scratch, original := recoveryLifecycleServer(t)
	faults := &recoveryFaultFiles{entered: make(chan struct{}), release: make(chan struct{})}
	s.reader = sessionstore.NewReaderWithBudget(faults, s.budget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := s.recoverSessionHistory(ctx, info); result <- err }()
	select {
	case <-faults.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Actual recovery did not read history")
	}
	cancel()
	close(faults.release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Cancellation lost", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recovery failed to join after cancellation")
	}
	if faults.closed.Load() != 1 || len(s.historyRecoveryCache) != 0 {
		t.Fatal("Canceled reader/cache leaked", faults.closed.Load())
	}
	assertRecoveryScratchEmpty(t, scratch)
	s.reader = sessionstore.NewReaderWithBudget(nil, s.budget)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/sessions/"+info.ID+"/history-recovery", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"human"`) || !strings.Contains(w.Body.String(), `"content":"[user message]"`) {
		t.Fatal("Canceled lane did not release for actual HTTP retry", w.Code, w.Body)
	}
	got, err := os.ReadFile(info.Path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("Recovery modified original history", err)
	}
	assertRecoveryScratchEmpty(t, scratch)
}

func TestHistoryRecoveryReadAndCloseFaultsDoNotCachePartialState(t *testing.T) {
	for _, kind := range []string{"read", "close"} {
		t.Run(kind, func(t *testing.T) {
			s, info, scratch, _ := recoveryLifecycleServer(t)
			failure := errors.New("private fixture " + kind + " failure")
			faults := &recoveryFaultFiles{}
			if kind == "read" {
				faults.readFailure = failure
			} else {
				faults.closeFailure = failure
			}
			s.reader = sessionstore.NewReaderWithBudget(faults, s.budget)
			if _, err := s.recoverSessionHistory(context.Background(), info); !errors.Is(err, failure) {
				t.Fatal("Original read/close failure not propagated", err)
			}
			if faults.closed.Load() != 1 || len(s.historyRecoveryCache) != 0 {
				t.Fatal("Fault cached partial state or leaked reader")
			}
			assertRecoveryScratchEmpty(t, scratch)
			s.reader = sessionstore.NewReaderWithBudget(nil, s.budget)
			if _, err := s.recoverSessionHistory(context.Background(), info); err != nil {
				t.Fatal("Admission lane not released", err)
			}
		})
	}
}

func TestHistoryRecoveryCacheHitRefreshesOriginalTwoEntryLRU(t *testing.T) {
	s, first, scratch, _ := recoveryLifecycleServer(t)
	infos := []sessionInfo{first}
	for _, id := range []string{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"} {
		path := filepath.Join(filepath.Dir(first.Path), id+".jsonl")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		infos = append(infos, sessionInfo{ID: id, Path: path})
	}
	for _, index := range []int{0, 1, 0, 2} {
		if _, err := s.recoverSessionHistory(context.Background(), infos[index]); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.historyRecoveryCache) != 2 || s.historyRecoveryCache[infos[1].ID].version != "" || len(s.historyRecoveryOrder) != 2 || s.historyRecoveryOrder[0] != first.ID || s.historyRecoveryOrder[1] != infos[2].ID {
		t.Fatal("Cache hit did not refresh original LRU", s.historyRecoveryOrder)
	}
	assertRecoveryScratchEmpty(t, scratch)
}

func assertRecoveryScratchEmpty(t *testing.T, path string) {
	t.Helper()
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 0 {
		t.Fatal("Recovery scratch state leaked", files, err)
	}
}
