package sessionstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryFS struct {
	file *memoryFile
	err  error
}

func (files memoryFS) Open(string) (File, error) { return files.file, files.err }

type memoryFile struct {
	data                     []byte
	statCalls, reads, closed int
	statError, readError     error
	closeError               error
	after                    func(*memoryFile)
	onRead                   func(*memoryFile)
	snapshot                 Snapshot
}

func (file *memoryFile) Stat() (Snapshot, error) {
	file.statCalls++
	if file.statCalls > 1 && file.after != nil {
		file.after(file)
	}
	return file.snapshot, file.statError
}
func (file *memoryFile) ReadAt(buffer []byte, offset int64) (int, error) {
	file.reads++
	if file.onRead != nil {
		file.onRead(file)
	}
	if file.readError != nil {
		return 0, file.readError
	}
	return bytes.NewReader(file.data).ReadAt(buffer, offset)
}
func (file *memoryFile) Close() error { file.closed++; return file.closeError }
func memoryReader(data string) (*Reader, *memoryFile) {
	file := &memoryFile{data: []byte(data), snapshot: Snapshot{"9", "18446744073709551614", "1700000000000000123", int64(len(data))}}
	return NewReader(memoryFS{file: file}), file
}
func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var api *APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("wanted %s, got %v", code, err)
	}
}
func TestReaderFaultsReleaseHandlesAndKeepCauses(t *testing.T) {
	cause := errors.New("boundary failed")
	for _, operation := range []string{"open", "stat", "read", "close", "zero-validation", "zero-page", "changed-read", "changed-anchors", "changed-stream", "consumer"} {
		t.Run(operation, func(t *testing.T) {
			reader, file := memoryReader("{\"id\":1}\n")
			switch operation {
			case "open":
				reader.files = memoryFS{err: cause}
			case "stat":
				file.statError = cause
			case "read":
				file.readError = cause
			case "close":
				file.closeError = cause
			case "zero-validation":
				file.data = nil
			case "zero-page":
				file.onRead = func(file *memoryFile) {
					if file.reads == 3 {
						file.data = nil
					}
				}
			case "changed-read":
				file.after = func(file *memoryFile) { file.snapshot.Ino = "changed" }
			case "changed-anchors":
				file.after = func(file *memoryFile) { file.data = []byte("{\"id\":2}\n") }
			case "changed-stream":
				file.after = func(file *memoryFile) { file.snapshot.Size-- }
			}
			var err error
			if operation == "changed-stream" || operation == "consumer" {
				_, err = reader.Stream(context.Background(), "fixture", func(json.RawMessage, bool, int64) error {
					if operation == "consumer" {
						return cause
					}
					return nil
				}, StreamOptions{})
			} else {
				_, err = reader.ReadPage(context.Background(), "fixture", PageOptions{})
			}
			if operation == "open" || operation == "stat" || operation == "read" || operation == "close" || operation == "consumer" {
				if !errors.Is(err, cause) {
					t.Fatal(err)
				}
			} else {
				errorCode(t, err, "HISTORY_CHANGED")
			}
			if operation != "open" && file.closed != 1 {
				t.Fatalf("handle closed %d times", file.closed)
			}
		})
	}
	for _, limit := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		reader, file := memoryReader("")
		_, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Limit: &limit})
		errorCode(t, err, "INVALID_HISTORY_LIMIT")
		if file.statCalls != 0 {
			t.Fatal("invalid limit opened file")
		}
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	reader, file := memoryReader("")
	if _, err := reader.ReadPage(ctx, "fixture", PageOptions{}); !errors.Is(err, cause) || file.statCalls != 0 {
		t.Fatal(err)
	}
}
func TestCursorValidationNativeBigintsAndLegacyRecovery(t *testing.T) {
	reader, file := memoryReader("{\"id\":1}\n{\"id\":2}\n")
	one := 1.0
	page, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Limit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Page.SourceVersion, "18446744073709551614") {
		t.Fatal(page.Page.SourceVersion)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(*page.Page.NextCursor)
	var valid map[string]any
	_ = json.Unmarshal(raw, &valid)
	for _, update := range []map[string]any{{"version": 2}, {"dev": 3}, {"ino": nil}, {"mtime": false}, {"size": 0.5}, {"size": -1}, {"size": 9007199254740992}, {"offset": -1}, {"offset": 100}, {"direction": "future"}, {"skipping": nil}, {"fingerprints": true}, {"fingerprints": map[string]any{"prefix": strings.Repeat("A", 64), "tail": strings.Repeat("a", 64), "boundary": strings.Repeat("a", 64)}}} {
		clone := map[string]any{}
		for key, value := range valid {
			clone[key] = value
		}
		for key, value := range update {
			clone[key] = value
		}
		encoded, _ := json.Marshal(clone)
		_, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: base64.RawURLEncoding.EncodeToString(encoded)})
		errorCode(t, err, "BAD_REQUEST")
	}
	for _, value := range []string{"bad", strings.Repeat("x", 2049), base64.RawURLEncoding.EncodeToString([]byte(`null`)), base64.RawURLEncoding.EncodeToString([]byte(`[]`))} {
		_, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: value})
		errorCode(t, err, "BAD_REQUEST")
	}
	valid["fingerprints"] = nil
	encoded, _ := json.Marshal(valid)
	legacy := base64.RawURLEncoding.EncodeToString(encoded)
	file.statCalls = 0
	file.closed = 0
	if _, err = reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: legacy}); err != nil {
		t.Fatal(err)
	}
	file.snapshot.Size++
	file.data = append(file.data, '\n')
	_, err = reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: legacy})
	errorCode(t, err, "HISTORY_CHANGED")
	file.snapshot.Size--
	file.snapshot.MtimeNS = "updated"
	_, err = reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *page.Page.NextCursor})
	errorCode(t, err, "HISTORY_CHANGED")
}
func TestBudgetQueuesAbortOverflowAndRecovery(t *testing.T) {
	for _, lane := range []Lane{PageLane, ContextLane, RecoveryLane, MetadataLane} {
		t.Run(string(lane), func(t *testing.T) {
			budget := NewBudget()
			pool := budget.pools[lane]
			var releases []func()
			for index := 0; index < pool.capacity; index++ {
				release, err := budget.acquire(context.Background(), lane)
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, release)
			}
			contexts := make([]context.CancelCauseFunc, pool.queueCapacity)
			done := make(chan error, pool.queueCapacity)
			cause := errors.New("tab closed")
			for index := 0; index < pool.queueCapacity; index++ {
				ctx, cancel := context.WithCancelCause(context.Background())
				contexts[index] = cancel
				go func() { done <- budget.With(ctx, lane, func() error { return nil }) }()
				waitQueue(t, budget, lane, index+1)
			}
			err := budget.With(context.Background(), lane, func() error { t.Fatal("overflow admitted"); return nil })
			errorCode(t, err, "HISTORY_BUSY")
			contexts[0](cause)
			if err = <-done; !errors.Is(err, cause) {
				t.Fatal(err)
			}
			waitQueue(t, budget, lane, pool.queueCapacity-1)
			for _, release := range releases {
				release()
			}
			for index := 1; index < pool.queueCapacity; index++ {
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			}
			if err = budget.With(context.Background(), lane, func() error { return cause }); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			budget.mu.Lock()
			if pool.active != 0 || len(pool.waiting) != 0 {
				t.Fatal("leaked reader slot")
			}
			budget.mu.Unlock()
		})
	}
	budget := NewBudget()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(budget.With(ctx, PageLane, func() error { return nil }), context.Canceled) {
		t.Fatal("cancelled read admitted")
	}
}
func waitQueue(t *testing.T, budget *Budget, lane Lane, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		budget.mu.Lock()
		actual := len(budget.pools[lane].waiting)
		budget.mu.Unlock()
		if actual == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("queue did not reach expected length")
}
func TestBudgetConcurrentReadOwnership(t *testing.T) {
	budget := NewBudget()
	var group sync.WaitGroup
	for index := 0; index < 64; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = budget.With(context.Background(), MetadataLane, func() error { runtime.Gosched(); return nil })
		}()
	}
	group.Wait()
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.pools[MetadataLane].active != 0 {
		t.Fatal("leaked active metadata read")
	}
}
func TestDisplayPreviewBudgetsAndFieldIdentity(t *testing.T) {
	deep := `"bottom"`
	for index := 0; index < 15; index++ {
		deep = `{"nested":` + deep + `}`
	}
	many := make([]string, 300)
	for index := range many {
		many[index] = `{"x":"body"}`
	}
	raw := `{"future":"\ud800","content":` + deep + `,"message":{"content":[` + strings.Join(many, ",") + `]},"toolUseResult":{"id":"keep","body":"` + strings.Repeat("a", 20000) + `"}}`
	result, err := DisplayPreview([]byte(raw))
	if err != nil || !bytes.Contains(result, []byte(`"bodyTruncated":true`)) || !bytes.Contains(result, []byte(`"future":"\ud800"`)) {
		t.Fatalf("preview: %s %v", result, err)
	}
	object := `{`
	for index := 0; index < 260; index++ {
		if index > 0 {
			object += ","
		}
		key, _ := json.Marshal(strings.Repeat("k", index+1))
		object += string(key) + `:[]`
	}
	object += `}`
	if _, err = DisplayPreview([]byte(`{"content":` + object + `}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"message":true}`, `{"message":-0}`, `{"message":1e400}`, `{"message":null}`, `{"message":[]}`, `{"message":"😀"}`, `{"message":{}}`, `{"content":false,"toolUseResult":0}`} {
		if _, err = DisplayPreview([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`bad`, `[]`, `null`} {
		if _, err = DisplayPreview([]byte(raw)); err == nil {
			t.Fatal("invalid preview accepted")
		}
	}
	state := previewState{remaining: 0, nodes: 0}
	if string(state.preview([]byte(`{}`), 0)) != `"[truncated preview]"` {
		t.Fatal("node limit ignored")
	}
	state.remaining = -1
	if !bytes.Contains(state.preview([]byte(`"a"`), 0), []byte("truncated preview")) {
		t.Fatal("string budget ignored")
	}
}
func TestStreamOversizedOffsetsAbortAndReadFaults(t *testing.T) {
	reader, file := memoryReader("{\"a\":1}\ninvalid\ntrue\n{\"last\":2}")
	maximum := int64(9)
	skipped := 0
	var entries []json.RawMessage
	result, err := reader.Stream(context.Background(), "fixture", func(raw json.RawMessage, complete bool, offset int64) error {
		entries = append(entries, raw)
		return nil
	}, StreamOptions{MaxRecordBytes: &maximum, OnSkipped: func() { skipped++ }})
	if err != nil || result.OmittedRecords != 2 || result.OversizedRecords != 1 || len(entries) != 1 || skipped != 2 {
		t.Fatalf("stream %+v %v entries %d", result, err, len(entries))
	}
	file.closed = 0
	file.readError = io.ErrUnexpectedEOF
	if _, err = reader.Stream(context.Background(), "fixture", func(json.RawMessage, bool, int64) error { return nil }, StreamOptions{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	file.readError = nil
	file.data = nil
	if _, err = reader.Stream(context.Background(), "fixture", func(json.RawMessage, bool, int64) error { return nil }, StreamOptions{}); err == nil {
		t.Fatal("zero read accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file.data = []byte("{}\n")
	file.snapshot.Size = 3
	if _, err = reader.Stream(ctx, "fixture", func(json.RawMessage, bool, int64) error { return nil }, StreamOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNativeFileMissingIsNotAnEmptyTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	reader := NewReader(nil)
	if _, err := reader.ReadPage(context.Background(), path, PageOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := reader.Stream(context.Background(), path, func(json.RawMessage, bool, int64) error { return nil }, StreamOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestNativeIdentityNeverFabricatesAZeroAndNamedCancellationUnwraps(t *testing.T) {
	for _, value := range []reflect.Value{reflect.ValueOf(nil), reflect.ValueOf(struct{}{})} {
		if _, err := unsignedField(value, "Ino"); err == nil {
			t.Fatal("missing native identity accepted")
		}
	}
	value := reflect.ValueOf(struct {
		Dev int64
		Ino uint64
	}{9, 18446744073709551614})
	if ino, err := unsignedField(value, "Ino"); err != nil || ino != "18446744073709551614" {
		t.Fatal(ino, err)
	}
	if dev, err := unsignedField(value, "Dev"); err != nil || dev != "9" {
		t.Fatal(dev, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := check(ctx)
	var abort *AbortError
	if !errors.As(err, &abort) || !errors.Is(err, context.Canceled) || abort.ErrorName() != "AbortError" || abort.Error() != "The operation was aborted." {
		t.Fatal(err)
	}
	api := &APIError{429, "HISTORY_BUSY", "History reader is busy; retry shortly"}
	if api.ErrorName() != "ApiError" || api.Error() != api.Message {
		t.Fatal(api)
	}
	if NewReader(nil).Budget() == nil {
		t.Fatal("missing reader budget")
	}
}

func TestEveryReadPhasePropagatesIOFailureAndReleasesItsHandle(t *testing.T) {
	// Each case addresses a different awaited FS boundary, including validation
	// and cursor encoding after the body has already been read.
	for _, phase := range []string{"initial-tail", "cursor-boundary", "after-stat", "after-prefix", "next-boundary", "previous-boundary", "stream-stat", "stream-after-stat", "stream-close", "stream-final-consumer", "stream-inner-abort"} {
		t.Run(phase, func(t *testing.T) {
			reader, file := memoryReader("{\"id\":1}\n{\"id\":2}\n{\"id\":3}")
			limit := 1.0
			initial, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Limit: &limit})
			if err != nil {
				t.Fatal(err)
			}
			file.reads = 0
			file.statCalls = 0
			file.closed = 0
			file.after = nil
			file.onRead = nil
			cause := errors.New("phase IO failure")
			options := PageOptions{Limit: &limit}
			ctx := context.Background()
			callback := func(json.RawMessage, bool, int64) error { return nil }
			switch phase {
			case "initial-tail":
				file.onRead = func(file *memoryFile) {
					if file.reads == 2 {
						file.readError = cause
					}
				}
			case "cursor-boundary":
				options.Cursor = *initial.Page.NextCursor
				file.onRead = func(file *memoryFile) {
					if file.reads == 3 {
						file.readError = cause
					}
				}
			case "after-stat", "stream-after-stat":
				file.after = func(file *memoryFile) { file.statError = cause }
			case "after-prefix":
				file.onRead = func(file *memoryFile) {
					if file.reads == 4 {
						file.readError = cause
					}
				}
			case "next-boundary":
				file.onRead = func(file *memoryFile) {
					if file.reads == 6 {
						file.readError = cause
					}
				}
			case "previous-boundary":
				options.Cursor = *initial.Page.NextCursor
				file.onRead = func(file *memoryFile) {
					if file.reads == 8 {
						file.readError = cause
					}
				}
			case "stream-stat":
				file.statError = cause
			case "stream-close":
				file.closeError = cause
			case "stream-final-consumer":
				file.data = []byte("{}")
				file.snapshot.Size = 2
				callback = func(json.RawMessage, bool, int64) error { return cause }
			case "stream-inner-abort":
				var cancel context.CancelCauseFunc
				ctx, cancel = context.WithCancelCause(ctx)
				file.onRead = func(*memoryFile) { cancel(cause) }
			}
			if strings.HasPrefix(phase, "stream-") {
				end := file.snapshot.Size
				_, err = reader.Stream(ctx, "fixture", callback, StreamOptions{EndOffset: &end})
			} else {
				_, err = reader.ReadPage(ctx, "fixture", options)
			}
			if !errors.Is(err, cause) || file.closed != 1 {
				t.Fatalf("failure %v, closes %d, reads %d", err, file.closed, file.reads)
			}
		})
	}
}

func TestCursorAnchorsRejectHiddenRewritesAndForwardRecordsCrossChunks(t *testing.T) {
	data := `{"first":"` + strings.Repeat("x", 80000) + `"}` + "\n" + `{"middle":"` + strings.Repeat("y", 80000) + `"}` + "\n" + `{"last":"` + strings.Repeat("z", 80000) + `"}` + "\n"
	reader, file := memoryReader(data)
	limit := 1.0
	last, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	middle, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *last.Page.NextCursor, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *middle.Page.NextCursor, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	forward, err := reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *first.Page.PreviousCursor, Limit: &limit})
	if err != nil || !bytes.Contains(forward.Entries[0].Entry, []byte(`"middle"`)) {
		t.Fatal(err)
	}
	decoded, err := decodeCursor(*last.Page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	file.data = append([]byte(nil), file.data...)
	file.data[decoded.offset-2048+10] = 'z'
	_, err = reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *last.Page.NextCursor})
	errorCode(t, err, "HISTORY_CHANGED")
	file.data = []byte(data)
	file.data[15] = 'z'
	_, err = reader.ReadPage(context.Background(), "fixture", PageOptions{Cursor: *last.Page.NextCursor})
	errorCode(t, err, "HISTORY_CHANGED")
}

type admissionCancellation struct {
	context.Context
	checks int
}

func (ctx *admissionCancellation) Err() error {
	ctx.checks++
	if ctx.checks > 1 {
		return context.Canceled
	}
	return nil
}
func TestCancellationAfterAdmissionAndRequiredBudget(t *testing.T) {
	budget := NewBudget()
	ctx := &admissionCancellation{Context: context.Background()}
	err := budget.With(ctx, PageLane, func() error { t.Fatal("cancelled scan started"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if budget.pools[PageLane].active != 0 {
		t.Fatal("cancelled admission leaked slot")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("nil shared budget accepted")
			}
		}()
		NewReaderWithBudget(nil, nil)
	}()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("unknown lane accepted")
			}
		}()
		_ = budget.With(context.Background(), Lane("unknown"), func() error { return nil })
	}()
	if _, err := (Entry{Entry: []byte(`bad`)}).MarshalJSON(); err == nil {
		t.Fatal("invalid entry serialized")
	}
	filePath := filepath.Join(t.TempDir(), "closed")
	if err := os.WriteFile(filePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := (OSFileSystem{}).Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err = file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := objectPairs([]byte(``)); err == nil {
		t.Fatal("malformed object accepted")
	}
	if _, err := objectPairs([]byte(`{":`)); err == nil {
		t.Fatal("malformed key accepted")
	}
	if _, err := objectPairs([]byte(`{"key":`)); err == nil {
		t.Fatal("malformed value accepted")
	}
	if !spreadObject(nil).IsObject() {
		t.Fatal("spread undefined failed")
	}
	if _, err := DisplayPreview([]byte(`{"message":[1,2]}`)); err != nil {
		t.Fatal(err)
	}
	if string(decodeBase64("%%%YWJj=ignored")) != "abc" || string(decodeBase64("YQ")) != "a" || len(decodeBase64("a")) != 0 {
		t.Fatal("Buffer base64 tolerance differs")
	}
	if len(decodeBase64("--__")) != 3 {
		t.Fatal("URL-safe alphabet rejected")
	}
}
