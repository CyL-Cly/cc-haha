package sessionmetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func collect(t *testing.T, file string, options Options) ([]Entry, Result, error) {
	t.Helper()
	var entries []Entry
	result, err := NewReader(nil).Stream(context.Background(), file, func(entry Entry, _ bool, _ int64) error { entries = append(entries, entry); return nil }, options)
	return entries, result, err
}
func fixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metadata.jsonl")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestOrdinaryMetadataRetainsValuesAndEOFBoundaries(t *testing.T) {
	raw := "{\"unknown\":9007199254740993,\"text\":\"\\ud800\"}\r\nfalse\nnull\n[]\ninvalid\n{\"tail\":true}"
	file := fixture(t, raw)
	var complete []bool
	var starts []int64
	var entries []Entry
	skipped := 0
	result, err := NewReader(nil).Stream(context.Background(), file, func(entry Entry, done bool, start int64) error {
		entries = append(entries, entry)
		complete = append(complete, done)
		starts = append(starts, start)
		return nil
	}, Options{OnSkipped: func() error { skipped++; return nil }})
	if err != nil || len(entries) != 2 || result.ProjectedRecords != 0 || result.OmittedRecords != 1 || skipped != 1 || complete[1] || starts[0] != 0 || entries[0].TextTruncated || !bytes.Contains(entries[0].Value, []byte("9007199254740993")) {
		t.Fatal(entries, complete, starts, result, err)
	}
	if result.NextOffset != int64(strings.LastIndex(raw, "\n")+1) || result.ScannedBytes != int64(len(raw)) {
		t.Fatal(result)
	}
	end := int64(strings.Index(raw, "\n") + 1)
	selected, scan, err := collect(t, file, Options{EndOffset: &end})
	if err != nil || len(selected) != 1 || scan.ScannedBytes != end || scan.NextOffset != end {
		t.Fatal(selected, scan, err)
	}
	_, empty, err := collect(t, file, Options{StartOffset: int64(len(raw))})
	if err != nil || empty.ScannedBytes != 0 || empty.NextOffset != int64(len(raw)) {
		t.Fatal(empty, err)
	}
}
func TestOversizedProjectionAllByteValidationAndRetry(t *testing.T) {
	large := strings.Repeat("x", sessionstore.HistorySemanticRecordBytes+1)
	for _, raw := range []string{
		"{\"unused\":\"" + large + "\",\"message\":{\"role\":\"user\",\"content\":\"" + strings.Repeat("a", 4095) + "😀end\"},\"cwd\":\"/kept\"}",
		"{\"unused\":\"" + large + "\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"" + strings.Repeat("z", 5000) + "\"},{\"type\":\"tool_use\",\"name\":\"Agent\",\"id\":\"i\",\"input\":{\"huge\":\"discard\"}}]}}",
	} {
		entries, scan, err := collect(t, fixture(t, raw), Options{})
		if err != nil || len(entries) != 1 || !entries[0].TextTruncated || scan.ProjectedRecords != 1 || len(entries[0].Value) > 20000 {
			t.Fatal(scan, err)
		}
	}
	for _, suffix := range []string{`\q"}`, `","n":01}`, `",}`, `"}tail`, ""} {
		raw := "{\"unused\":\"" + large + suffix + "\n{\"cwd\":\"/next\"}\n"
		entries, scan, err := collect(t, fixture(t, raw), Options{})
		if err != nil || len(entries) != 1 || scan.OmittedRecords != 1 || string(entries[0].Value) != "{\"cwd\":\"/next\"}" {
			t.Fatal(entries, scan, err)
		}
	}
	for _, raw := range []string{
		"{\"unused\":\"" + large + "\",\"workDir\":\"" + strings.Repeat("a", metadataBytes+1) + "\"}",
		"{\"unused\":\"" + large + "\",\"repository\":{\"" + strings.Repeat("a", 257) + "\":0}}",
		"{\"unused\":\"" + large + "\",\"discarded\":" + strings.Repeat("[", 130) + "0" + strings.Repeat("]", 130) + "}",
		"{\"unused\":\"" + large + "\",\"repository\":{\"many\":[" + strings.Repeat("0,", 9000) + "0]}}",
	} {
		file := fixture(t, raw)
		_, _, err := collect(t, file, Options{})
		var api *sessionstore.APIError
		if !errors.As(err, &api) || api.StatusCode != 413 || api.Code != "SESSION_METADATA_TOO_LARGE" {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("{\"cwd\":\"/retry\"}"), 0600); err != nil {
			t.Fatal(err)
		}
		entries, _, err := collect(t, file, Options{})
		if err != nil || len(entries) != 1 {
			t.Fatal(entries, err)
		}
	}
}
func TestDuplicateSlotsKeysAndPrimitiveRootTypes(t *testing.T) {
	padding := strings.Repeat("x", sessionstore.HistorySemanticRecordBytes+1)
	for _, raw := range []string{
		"{\"message\":{\"content\":\"" + padding + "\"},\"message\":{\"content\":\"final\"}}",
		"{\"message\":{\"content\":\"" + padding + "\",\"content\":\"final\"}}",
		"{\"message\":{\"content\":[{\"text\":\"" + padding + "\",\"text\":\"final\"}]}}",
		"{\"unused\":\"" + padding + "\",\"message\":[],\"repository\":{\"__proto__\":{\"safe\":true},\"\\ud800\":\"unit\",\"2\":2,\"1\":1,\"n\":-0,\"x\":1e400}}",
		"{\"unused\":\"" + padding + "\",\"message\":null,\"content\":\"" + strings.Repeat("a", 5000) + "\"}",
	} {
		entries, scan, err := collect(t, fixture(t, raw), Options{})
		if err != nil || len(entries) != 1 || entries[0].TextTruncated || scan.ProjectedRecords != 1 || !json.Valid(entries[0].Value) {
			t.Fatal(entries, scan, err)
		}
	}
	for _, raw := range []string{`"` + padding + `"`, "[" + strings.Repeat(" ", sessionstore.HistorySemanticRecordBytes+1) + "]"} {
		entries, scan, err := collect(t, fixture(t, raw), Options{})
		if len(entries) != 0 {
			t.Fatal(entries)
		}
		if raw[0] == '"' {
			if err == nil {
				t.Fatal("retained root scalar exceeded budget")
			}
		} else if err != nil || scan.ProjectedRecords != 1 {
			t.Fatal(scan, err)
		}
	}
}
func TestTokenizerFragmentedGrammarAndUTF16(t *testing.T) {
	values := []string{
		`{"repository":{"a":-0,"b":-1.25e+4,"c":1E-5,"d":1e400,"t":true,"f":false,"n":null},"message":{"content":[{"text":"😀\ud800\b\f\n\r\t\/\\\"","type":"text"}]},"cwd":"/a"}`,
		"{}", "[]", "[0,1]", "true", "false", "null", "0", "1.5", "-1", "2E+3", "0.1", "1e-2",
	}
	for _, raw := range values {
		for _, size := range []int{1, 2, 7, 64} {
			parser := tokenizer{}
			p := projection{}
			for start := 0; start < len(raw); start += size {
				end := start + size
				if end > len(raw) {
					end = len(raw)
				}
				tokens, err := parser.feed([]byte(raw[start:end]), false)
				if err != nil {
					t.Fatal(raw, size, err)
				}
				for _, token := range tokens {
					if err := p.token(token); err != nil {
						t.Fatal(err)
					}
				}
			}
			tokens, err := parser.feed(nil, true)
			if err != nil {
				t.Fatal(raw, size, err)
			}
			for _, token := range tokens {
				if err := p.token(token); err != nil {
					t.Fatal(err)
				}
			}
			if p.root == nil {
				t.Fatal(raw)
			}
			if p.root.kind == '{' {
				want, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				got, err := protocol.ParseAndStringifyJavaScriptJSON(p.entry().Value)
				if err != nil || !bytes.Equal(want, got) {
					t.Fatal(string(want), string(got), err)
				}
			}
		}
	}
	invalid := []string{"", "{", "[", "{\"x\"}", "{\"x\":}", "{0:1}", "{\"x\":0,}", "[0,]", "[}", "{}{}", "trueX", "null0", "false_", "01", "-", "1.", "1e", "1e+", "+1", "NaN", `"\q"`, `"\u123x"`, "\"\x01\"", "\"tail", "{"}
	for _, raw := range invalid {
		for _, size := range []int{1, 3, 65536} {
			parser := tokenizer{}
			var failure error
			for start := 0; start < len(raw) && failure == nil; start += size {
				end := start + size
				if end > len(raw) {
					end = len(raw)
				}
				_, failure = parser.feed([]byte(raw[start:end]), false)
			}
			if failure == nil {
				_, failure = parser.feed(nil, true)
			}
			if failure == nil {
				t.Fatal("accepted invalid JSON", raw, size)
			}
		}
	}
	for _, raw := range [][]byte{
		[]byte("{\"message\":{\"content\":\"\xf0\x9f\"}}"),
		[]byte("{\"message\":{\"content\":\"\xed\xa0\x80\xc0\xaf\xff\"}}"),
		[]byte("{\"message\":{\"content\":\"\xf0\x90\x80\x80\xe2\x82\xac\xc2\xa2\"}}"),
	} {
		p := projection{}
		parser := tokenizer{}
		for _, b := range raw {
			tokens, err := parser.feed([]byte{b}, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range tokens {
				if err := p.token(token); err != nil {
					t.Fatal(err)
				}
			}
		}
		tokens, err := parser.feed(nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range tokens {
			if err := p.token(token); err != nil {
				t.Fatal(err)
			}
		}
		want, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(raw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := protocol.ParseAndStringifyJavaScriptJSON(p.entry().Value)
		if err != nil || !bytes.Equal(want, got) {
			t.Fatal(string(want), string(got), err)
		}
	}
}

type failingFS struct {
	file    sessionstore.File
	openErr error
}

func (fs failingFS) Open(string) (sessionstore.File, error) { return fs.file, fs.openErr }

type failingFile struct {
	data              []byte
	stats             int
	statFail          int
	readErr, closeErr error
	zero              bool
	changed           bool
	closed            bool
}

func (f *failingFile) ReadAt(p []byte, offset int64) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.zero {
		return 0, nil
	}
	if offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[offset:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (f *failingFile) Stat() (sessionstore.Snapshot, error) {
	f.stats++
	if f.stats == f.statFail {
		return sessionstore.Snapshot{}, errors.New("stat fault")
	}
	stamp := "1"
	if f.changed && f.stats == 2 {
		stamp = "2"
	}
	return sessionstore.Snapshot{Dev: "1", Ino: "2", Size: int64(len(f.data)), MtimeNS: stamp}, nil
}
func (f *failingFile) Close() error { f.closed = true; return f.closeErr }
func TestFailuresAndCancellationPreserveCausesAndReleaseHandles(t *testing.T) {
	fault := errors.New("injected IO")
	for _, file := range []*failingFile{
		{data: []byte("{}"), statFail: 1}, {data: []byte("{}"), statFail: 2},
		{data: []byte("{}"), readErr: fault}, {data: []byte("{}"), zero: true},
		{data: []byte("{}"), changed: true}, {data: []byte("{}"), closeErr: fault},
	} {
		_, err := NewReader(failingFS{file: file}).Stream(context.Background(), "private", func(Entry, bool, int64) error { return nil }, Options{})
		if err == nil || !file.closed {
			t.Fatal(file, err)
		}
	}
	if _, err := NewReader(failingFS{openErr: fault}).Stream(context.Background(), "private", nil, Options{}); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	file := &failingFile{data: []byte("{}"), closeErr: fault}
	_, err := NewReader(failingFS{file: file}).Stream(context.Background(), "private", func(Entry, bool, int64) error { return errors.New("consumer") }, Options{})
	if !errors.Is(err, fault) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file = &failingFile{data: []byte("{}")}
	_, err = NewReader(failingFS{file: file}).Stream(ctx, "private", nil, Options{})
	var abort *sessionstore.AbortError
	if !errors.As(err, &abort) || !errors.Is(err, context.Canceled) || !file.closed {
		t.Fatal(err)
	}
	empty := &failingFile{}
	if _, err := NewReader(failingFS{file: empty}).Stream(ctx, "private", nil, Options{}); err != nil {
		t.Fatal("empty source has no cancellation check", err)
	}
	cause := errors.New("custom cause")
	custom, stop := context.WithCancelCause(context.Background())
	stop(cause)
	if err := check(custom); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	file = &failingFile{data: []byte("invalid\n")}
	_, err = NewReader(failingFS{file: file}).Stream(context.Background(), "private", nil, Options{OnSkipped: func() error { return cause }})
	if !errors.Is(err, cause) || !file.closed {
		t.Fatal("skip callback failure was swallowed", err)
	}
}
func TestConcurrentReadersUseIndependentProjectionState(t *testing.T) {
	file := fixture(t, "{\"type\":\"session-meta\",\"cwd\":\"/one\"}\n")
	reader := NewReader(nil)
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := reader.Stream(context.Background(), file, func(entry Entry, _ bool, _ int64) error {
				if string(entry.Value) != "{\"type\":\"session-meta\",\"cwd\":\"/one\"}" {
					return errors.New("wrong value")
				}
				return nil
			}, Options{})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
}
func TestDiscardedBodiesDoNotAllocateProportionallyToSourceSize(t *testing.T) {
	chunk := bytes.Repeat([]byte("x"), 64*1024)
	var allocations []uint64
	for _, size := range []int{8*1024*1024 + 1, 32 * 1024 * 1024, 64 * 1024 * 1024} {
		file := filepath.Join(t.TempDir(), "large.jsonl")
		handle, err := os.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.WriteString(handle, "{\"unused\":\"")
		if err != nil {
			t.Fatal(err)
		}
		for written := 0; written < size; {
			length := len(chunk)
			if size-written < length {
				length = size - written
			}
			if _, err := handle.Write(chunk[:length]); err != nil {
				t.Fatal(err)
			}
			written += length
		}
		_, err = io.WriteString(handle, "\",\"cwd\":\"/bounded\"}\n")
		if err != nil {
			t.Fatal(err)
		}
		if err := handle.Close(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		count := 0
		result, err := NewReader(nil).Stream(context.Background(), file, func(entry Entry, _ bool, _ int64) error {
			count++
			if string(entry.Value) != "{\"cwd\":\"/bounded\"}" {
				return fmt.Errorf("wrong projection %s", entry.Value)
			}
			return nil
		}, Options{})
		runtime.ReadMemStats(&after)
		if err != nil || count != 1 || result.ProjectedRecords != 1 {
			t.Fatal(result, err)
		}
		allocated := after.TotalAlloc - before.TotalAlloc
		allocations = append(allocations, allocated)
		t.Logf("discarded %d bytes: Go stream TotalAlloc=%d bytes", size, allocated)
		if allocated > allocations[0]+512*1024 {
			t.Fatal("discarded body allocation grew with input", allocations)
		}
	}
}
