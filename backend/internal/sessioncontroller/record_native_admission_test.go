package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type admissionRecordIO struct {
	OSFiles
	closed   atomic.Bool
	mu       sync.Mutex
	rows     []map[string]any
	statDone chan struct{}
	once     sync.Once
}

func (f *admissionRecordIO) Stat(path string) (fs.FileInfo, error) {
	info, e := os.Stat(path)
	f.mu.Lock()
	f.rows = append(f.rows, map[string]any{"operation": "actual-os.Stat", "path": path, "afterCloseReturned": f.closed.Load(), "error": fmt.Sprint(e), "physicalUTC": time.Now().UTC().Format(time.RFC3339Nano)})
	f.mu.Unlock()
	f.once.Do(func() { close(f.statDone) })
	return info, e
}
func admissionRecordWrite(t *testing.T, id string, result any) {
	t.Helper()
	destination := os.Getenv("CC_HAHA_LIFECYCLE_REPORT_DIR")
	if destination == "" {
		// Standard go test/check:go needs no external report setup. An explicit
		// private directory remains available for maintainer evidence export.
		destination = t.TempDir()
	}
	if !filepath.IsAbs(destination) {
		t.Fatal("explicit report directory required")
	}
	b, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(destination, id+".json"), append(b, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
func admissionSignal(t *testing.T, c <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatalf("administrative watchdog awaiting %s; not a semantic pass", why)
	}
}

func TestRecordNativeAdmission(t *testing.T) {
	t.Run("D-real-cold-cache-close-late-discard", func(t *testing.T) {
		f := &admissionRecordIO{statDone: make(chan struct{})}
		physical := &uuidIO{kind: "readFile", entered: make(chan struct{}), released: make(chan struct{})}
		owner, w, p, loader := recordHost(t, f, physical)
		if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(w.target(), []byte(uuidOld), 0600); e != nil {
			t.Fatal(e)
		}
		var releaseRead sync.Once
		release := func() { releaseRead.Do(func() { close(physical.released) }) }
		releaseOwner := make(chan struct{})
		var releaseScope sync.Once
		unpark := func() { releaseScope.Do(func() { close(releaseOwner) }) }
		defer func() { release(); unpark(); p.workers.Wait() }()
		message := plainMessage("original-late-after-close", "original native admission prompt")
		future := startRecord(t, owner, p, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return quote("original-prompt") }})
		admissionSignal(t, physical.entered, "actual native cold-cache read receipt")
		var cache *SessionUUIDFuture
		pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
			var e error
			cache, e = p.SessionUUIDs(s, loader, w.options.SessionID)
			return e
		})
		scopeStarted := make(chan struct{})
		cacheDelivered := make(chan struct{})
		scopeResult := make(chan error, 1)
		go func() {
			scopeResult <- owner.Run(context.Background(), func(s *cooperative.Scope) error {
				close(scopeStarted)
				if e := s.AwaitPromise(cache.Done()); e != nil {
					return e
				}
				close(cacheDelivered)
				<-releaseOwner
				return nil
			})
		}()
		admissionSignal(t, scopeStarted, "original owner stack cache await")
		closeResult := make(chan error, 1)
		closeReturned := make(chan struct{})
		go func() {
			e := p.Close(context.Background())
			f.closed.Store(true)
			closeResult <- e
			close(closeReturned)
		}()
		// Administrative observation of actual Close admission; no callback changes
		// business state or supplies Source phases. Requeue until that exact method
		// has set its flag, without sleeps or a fixed count of fulfilled awaits.
		closeAdmitted := make(chan struct{})
		var observe func(*cooperative.Scope) error
		observe = func(*cooperative.Scope) error {
			if p.closing {
				close(closeAdmitted)
				return nil
			}
			_, e := owner.SubmitIndependent(context.Background(), cooperative.Read, observe)
			return e
		}
		if _, e := owner.SubmitIndependent(context.Background(), cooperative.Read, observe); e != nil {
			t.Fatal(e)
		}
		admissionSignal(t, closeAdmitted, "actual PendingWriter.Close admission")
		release()
		admissionSignal(t, cacheDelivered, "real cache completion before queued Record reaction")
		admissionSignal(t, closeReturned, "administrative Close return while owner producer is held")
		closeError := <-closeResult
		unresolved := true
		select {
		case <-future.Done():
			unresolved = false
		default:
		}
		unpark()
		if e := <-scopeResult; e != nil {
			t.Fatal(e)
		}
		_, recordError := future.Wait(context.Background())
		if cacheError := cache.future.Wait(context.Background()); cacheError != nil {
			t.Fatalf("late Record rejection poisoned original cached promise: %v", cacheError)
		}
		pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
			if p.uuidCache.promises[w.options.SessionID] != cache {
				t.Error("late Record rejection replaced shared cached future identity")
			}
			has, err := cache.set.OwnerHas(scope, quote("original-old"))
			if err != nil || !has {
				t.Errorf("late Record changed original cached Set %v/%v", has, err)
			}
			return nil
		})
		p.workers.Wait()
		f.mu.Lock()
		rows := append([]map[string]any{}, f.rows...)
		f.mu.Unlock()
		data, e := os.ReadFile(w.target())
		if e != nil {
			t.Fatal(e)
		}
		late := false
		for _, row := range rows {
			if row["afterCloseReturned"] == true {
				late = true
			}
		}
		result := map[string]any{"PID": os.Getpid(), "domain": "administrative actual Go native/cache/Close boundary; no new Source program proof", "closeError": fmt.Sprint(closeError), "recordError": fmt.Sprint(recordError), "recordErrorType": fmt.Sprintf("%T", recordError), "recordPendingAtCloseReturn": unresolved, "lateActualStatAfterClose": late, "physicalRows": rows, "literalTranscript": string(data), "originalTranscript": uuidOld}
		admissionRecordWrite(t, "D-real-cold-cache-close-late-discard", result)
		if closeError != nil || !unresolved || late || !errors.Is(recordError, ErrPendingClosed) || strings.Contains(string(data), "original-late-after-close") {
			t.Errorf("late Record must reject before admitting any new Stat: %s", string(mustAdmissionJSON(result)))
		}
	})
}
func mustAdmissionJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
