package pdfread

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceProcess struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Error  string `json:"error,omitempty"`
}
type sourceStats struct {
	Size  int64   `json:"size"`
	Mtime float64 `json:"mtime"`
}
type sourcePDFCase struct {
	Action            string            `json:"action"`
	File              string            `json:"file,omitempty"`
	Size              *int64            `json:"size,omitempty"`
	Bytes             string            `json:"bytes,omitempty"`
	Mtime             float64           `json:"mtime,omitempty"`
	Stats             []sourceStats     `json:"stats,omitempty"`
	StatError         string            `json:"statError,omitempty"`
	ReadError         string            `json:"readError,omitempty"`
	DirectoryError    string            `json:"directoryError,omitempty"`
	MkdirError        string            `json:"mkdirError,omitempty"`
	Entries           []string          `json:"entries"`
	Images            map[string]string `json:"images,omitempty"`
	Processes         []sourceProcess   `json:"processes,omitempty"`
	Repetitions       int               `json:"repetitions,omitempty"`
	Options           *Range            `json:"-"`
	PageOptions       map[string]int    `json:"options,omitempty"`
	Pages             *string           `json:"pages,omitempty"`
	Model             string            `json:"model,omitempty"`
	AbortAfterProcess int               `json:"abortAfterProcess,omitempty"`
}

func actualPDFSource(t *testing.T, input map[string]any) (protocol.JSONObject, string) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	input["home"] = home
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-pdf-read.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PDF_READ_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source PDF fixture: %v\n%.8000s", err, out)
	}
	var value protocol.JSONObject
	if err = value.UnmarshalJSON(out); err != nil {
		t.Fatalf("actual Source PDF output: %v\n%.8000s", err, out)
	}
	return value, home
}

func sourceNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	raw, _ := json.Marshal(value)
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}
func sameSourceJSON(t *testing.T, actual any, expected json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	normalizeConcurrentImageCalls(got)
	normalizeConcurrentImageCalls(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Go %s\nSource %s", raw, expected)
	}
}

// Source Promise.all and native joined jobs may interleave their FS/codec
// primitives differently. Compare their complete page-job inventory here;
// image blocks themselves retain the Source's exact sorted order above.
func normalizeConcurrentImageCalls(value any) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	calls, ok := object["calls"].([]any)
	if !ok {
		return
	}
	start := -1
	for index, call := range calls {
		if call.(map[string]any)["type"] == "readdir" {
			start = index + 1
		}
	}
	if start < 0 {
		return
	}
	for _, call := range calls[start:] {
		kind := call.(map[string]any)["type"]
		if kind != "read" && kind != "image" {
			return
		}
	}
	sort.Slice(calls[start:], func(i, j int) bool {
		left, _ := json.Marshal(calls[start+i])
		right, _ := json.Marshal(calls[start+j])
		return bytes.Compare(left, right) < 0
	})
}

func TestActualSourcePDFPageRangesAndValidation(t *testing.T) {
	ranges := []string{"", "  ", "0", "1", "+2", "-2", "3-5", "20-39", "1-21", "3-", "3--", "1-2-3", "3.8", "1e2", "0x10", "2junk", "1-3tail", "1--2", "\ufeff 2 \ufeff", "\u00852", "1-\u00852", "1-\ufeff2", "\u20003\u202f", "\n2\t", "bad\ntext", `bad"quote`, "9007199254740993", "1000000000000000000000", "999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999"}
	source, _ := actualPDFSource(t, map[string]any{"ranges": ranges})
	var rows []json.RawMessage
	_ = json.Unmarshal(source.Get("ranges"), &rows)
	if len(rows) != len(ranges) {
		t.Fatal("Source range row count", string(source.Get("ranges")))
	}
	for i, pages := range ranges {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			parsed := ParseRange(pages)
			var projection any
			if parsed != nil {
				projection = map[string]string{"firstPage": sourceNumber(parsed.First), "lastPage": sourceNumber(parsed.Last)}
			}
			validation := map[string]any{"result": true}
			if err := ValidatePages(pages); err != nil {
				code := 8
				if parsed == nil {
					code = 7
				}
				validation = map[string]any{"result": false, "message": err.Error(), "errorCode": code}
			}
			sameSourceJSON(t, map[string]any{"range": projection, "validation": validation}, rows[i])
		})
	}
}

type pdfCaseFiles struct {
	mu        sync.Mutex
	row       sourcePDFCase
	path      string
	directory string
	calls     []map[string]any
	stats     int
	runs      int
}

func (f *pdfCaseFiles) record(value map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, value)
}

type pdfCaseFileInfo struct {
	size  int64
	mtime float64
}

func (f pdfCaseFileInfo) Name() string       { return "report.pdf" }
func (f pdfCaseFileInfo) Size() int64        { return f.size }
func (f pdfCaseFileInfo) Mode() os.FileMode  { return 0600 }
func (f pdfCaseFileInfo) ModTime() time.Time { return time.Unix(0, int64(f.mtime*1e6)) }
func (f pdfCaseFileInfo) IsDir() bool        { return false }
func (f pdfCaseFileInfo) Sys() any           { return nil }

type pdfCaseEntry struct{ name string }

func (e pdfCaseEntry) Name() string               { return e.name }
func (e pdfCaseEntry) IsDir() bool                { return false }
func (e pdfCaseEntry) Type() os.FileMode          { return 0 }
func (e pdfCaseEntry) Info() (os.FileInfo, error) { return pdfCaseFileInfo{}, nil }

func (f *pdfCaseFiles) Stat(path string) (os.FileInfo, error) {
	f.record(map[string]any{"type": "stat", "path": path})
	if f.row.StatError != "" {
		return nil, errors.New(f.row.StatError)
	}
	size, mtime := int64(8), 1234.75
	if f.row.Size != nil {
		size = *f.row.Size
	}
	if f.row.Mtime != 0 {
		mtime = f.row.Mtime
	}
	if len(f.row.Stats) > 0 {
		i := f.stats
		if i >= len(f.row.Stats) {
			i = len(f.row.Stats) - 1
		}
		size, mtime = f.row.Stats[i].Size, f.row.Stats[i].Mtime
		f.stats++
	}
	return pdfCaseFileInfo{size, mtime}, nil
}
func (f *pdfCaseFiles) ReadFile(path string) ([]byte, error) {
	f.record(map[string]any{"type": "read", "path": path})
	if f.row.ReadError != "" {
		return nil, errors.New(f.row.ReadError)
	}
	data := f.row.Bytes
	if path != f.path {
		data = f.row.Images[filepath.Base(path)]
		if data == "" {
			data = "/9j/AA=="
		}
	} else if data == "" {
		data = "JVBERi0xLjQ="
	}
	return base64.StdEncoding.DecodeString(data)
}
func (f *pdfCaseFiles) MkdirAll(path string, _ os.FileMode) error {
	f.record(map[string]any{"type": "mkdir", "path": path})
	if f.row.MkdirError != "" {
		return errors.New(f.row.MkdirError)
	}
	return nil
}
func (f *pdfCaseFiles) ReadDir(path string) ([]os.DirEntry, error) {
	f.record(map[string]any{"type": "readdir", "path": path})
	if f.row.DirectoryError != "" {
		return nil, errors.New(f.row.DirectoryError)
	}
	names := f.row.Entries
	if names == nil {
		names = []string{"page-01.jpg"}
	}
	entries := make([]os.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, pdfCaseEntry{name})
	}
	return entries, nil
}
func (f *pdfCaseFiles) Run(_ context.Context, executable string, args []string, timeout time.Duration) (ProcessResult, error) {
	f.record(map[string]any{"type": "run", "executable": executable, "args": args, "timeout": timeout.Milliseconds()})
	if f.runs >= len(f.row.Processes) {
		return ProcessResult{}, fmt.Errorf("unexpected additional native PDF subprocess")
	}
	result := f.row.Processes[f.runs]
	f.runs++
	if result.Error != "" {
		return ProcessResult{}, errors.New(result.Error)
	}
	return ProcessResult{result.Code, result.Stdout, result.Stderr}, nil
}

type pdfCaseCodec struct{ files *pdfCaseFiles }

func (c pdfCaseCodec) Metadata(data []byte) (imageprocessing.Metadata, error) {
	c.files.record(map[string]any{"type": "image", "bytes": base64.StdEncoding.EncodeToString(data), "originalSize": len(data), "extension": "jpeg"})
	return imageprocessing.Metadata{Format: "jpeg"}, nil
}
func (c pdfCaseCodec) Encode([]byte, int, int, string, int) ([]byte, error) {
	return nil, errors.New("unexpected image encode")
}

func pdfUtilityProjection(data json.RawMessage, err error) any {
	if err == nil {
		return map[string]any{"success": true, "data": data}
	}
	reason := "unknown"
	var pdfError *Error
	if errors.As(err, &pdfError) {
		reason = pdfError.Reason
	}
	return map[string]any{"success": false, "error": map[string]string{"reason": reason, "message": err.Error()}}
}
func comparePDFCases(t *testing.T, rows []sourcePDFCase) {
	t.Helper()
	source, home := actualPDFSource(t, map[string]any{"rows": rows})
	var results []json.RawMessage
	_ = json.Unmarshal(source.Get("rows"), &results)
	if len(results) != len(rows) {
		t.Fatal("Source PDF row count", string(source.Get("rows")))
	}
	for i, row := range rows {
		t.Run(strconv.Itoa(i)+"-"+row.Action, func(t *testing.T) {
			file := row.File
			if file == "" {
				file = "report.pdf"
			}
			f := &pdfCaseFiles{row: row, path: filepath.Join(home, file), directory: filepath.Join(home, "results"), calls: []map[string]any{}}
			reader, err := New(Options{Files: f, Run: f.Run, UUID: func() string { return "fixture" }, ResultsDirectory: f.directory, Codec: pdfCaseCodec{f}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			var output any
			switch row.Action {
			case "raw":
				data, err := reader.raw(f.path)
				output = pdfUtilityProjection(data, err)
			case "count":
				count, err := reader.count(ctx, f.path)
				value := "null"
				if count != nil {
					value = sourceNumber(*count)
				}
				output = map[string]any{"count": value}
				if err != nil {
					output = map[string]string{"thrown": err.Error()}
				}
			case "probe":
				available := []bool{}
				count := row.Repetitions
				if count == 0 {
					count = 1
				}
				for j := 0; j < count; j++ {
					value, e := reader.probe(ctx)
					if e != nil {
						err = e
						break
					}
					available = append(available, value)
				}
				output = map[string]any{"available": available}
				if err != nil {
					output = map[string]string{"thrown": err.Error()}
				}
			case "extract":
				var pages *Range
				if row.PageOptions != nil {
					pages = &Range{float64(row.PageOptions["firstPage"]), float64(row.PageOptions["lastPage"])}
				}
				data, e := reader.extract(ctx, f.path, pages)
				output = pdfUtilityProjection(data, e)
			case "promote":
				data := []any{}
				original := reader.WithResultsDirectory(filepath.Join(f.directory, "original"))
				promoted := reader.WithResultsDirectory(filepath.Join(f.directory, "promoted")).WithRunner(f.Run)
				for _, value := range []*Reader{original, promoted, original} {
					result, e := value.extract(ctx, f.path, nil)
					data = append(data, pdfUtilityProjection(result, e))
				}
				output = map[string]any{"data": data}
			case "read":
				model := row.Model
				if model == "" {
					model = "deepseek-flash"
				}
				result, e := reader.Read(ctx, f.path, row.Pages, model)
				if e != nil {
					output = map[string]string{"thrown": e.Error()}
					break
				}
				blocks, e := reader.Metadata(ctx, result.Data)
				if e != nil {
					output = map[string]string{"thrown": e.Error()}
					break
				}
				cache := []any{}
				if result.CacheFull {
					cache = append(cache, map[string]any{"path": f.path, "content": "", "timestamp": result.Timestamp, "offset": 1})
				}
				output = map[string]any{"data": result.Data, "blocks": blocks, "cache": cache}
			default:
				t.Fatal("unknown native PDF action", row.Action)
			}
			sameSourceJSON(t, map[string]any{"output": output, "calls": f.calls}, results[i])
		})
	}
}
func size(value int64) *int64   { return &value }
func page(value string) *string { return &value }
func probeOK() []sourceProcess  { return []sourceProcess{{Code: 0}, {Code: 0}} }

func TestActualSourcePDFUtilitiesNativeBoundariesAndFailures(t *testing.T) {
	rows := []sourcePDFCase{
		{Action: "raw"}, {Action: "raw", Size: size(0)}, {Action: "raw", Size: size(RawLimit)}, {Action: "raw", Size: size(RawLimit + 1)},
		{Action: "raw", Bytes: base64.StdEncoding.EncodeToString([]byte("%PDF-"))}, {Action: "raw", Bytes: base64.StdEncoding.EncodeToString([]byte("%PD"))},
		{Action: "raw", Bytes: base64.StdEncoding.EncodeToString([]byte("htmlpdf"))}, {Action: "raw", Bytes: base64.StdEncoding.EncodeToString([]byte{0xa5, 0xd0, 0xc4, 0xc6, 0xad})},
		{Action: "raw", StatError: "native stat EACCES"}, {Action: "raw", ReadError: "native read failure"},
		{Action: "count", Processes: []sourceProcess{{Code: 1, Stdout: "Pages: 2"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "Pages: 10\n"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "noise\rPages: 3"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "noise\u2028Pages:\ufeff2"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "Pages:\u00852"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "Pages: 9007199254740993 garbage"}}},
		{Action: "count", Processes: []sourceProcess{{Stdout: "Pages: nope"}}},
		{Action: "count", Processes: []sourceProcess{{Error: "native pdfinfo failure"}}},
		{Action: "probe", Repetitions: 3, Processes: []sourceProcess{{Code: 0}}},
		{Action: "probe", Repetitions: 2, Processes: []sourceProcess{{Code: 99, Stderr: "version"}}},
		{Action: "probe", Repetitions: 2, Processes: []sourceProcess{{Code: 1}}},
		{Action: "probe", Processes: []sourceProcess{{Error: "probe failed"}}},
		{Action: "extract", Size: size(0)}, {Action: "extract", Size: size(ExtractLimit + 1)},
		{Action: "extract", Processes: []sourceProcess{{Code: 1}}},
		{Action: "extract", Size: size(ExtractLimit), Processes: probeOK()},
		{Action: "extract", PageOptions: map[string]int{"firstPage": 3, "lastPage": 7}, Processes: probeOK()},
		{Action: "extract", PageOptions: map[string]int{"firstPage": 0, "lastPage": 0}, Processes: probeOK()},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Code: 1, Stderr: "PASSWORD invalid"}}},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Code: 1, Stderr: "Damaged PDF"}}},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Code: 1, Stderr: "CORRUPTED"}}},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Code: 1, Stderr: "invalid"}}},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Code: 1, Stderr: "native unknown\n"}}},
		{Action: "extract", Entries: []string{}, Processes: probeOK()},
		{Action: "extract", Entries: []string{"page-2.JPG", "note.txt", "page-2.jpg", "page-01.jpg"}, Processes: probeOK()},
		{Action: "extract", StatError: "stat lost"},
		{Action: "extract", MkdirError: "directory readonly", Processes: []sourceProcess{{Code: 0}}},
		{Action: "extract", DirectoryError: "list failed", Processes: probeOK()},
		{Action: "extract", Processes: []sourceProcess{{Code: 0}, {Error: "render interrupted"}}},
	}
	comparePDFCases(t, rows)
}

func TestActualSourcePDFReadCallCacheAndSupplementalBlocks(t *testing.T) {
	rows := []sourcePDFCase{
		{Action: "read", Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Processes: []sourceProcess{{Stdout: "Pages: 10"}}},
		{Action: "read", Processes: []sourceProcess{{Stdout: "Pages: 11"}}},
		{Action: "read", Processes: []sourceProcess{{Code: 1}}},
		{Action: "read", Pages: page(""), Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Pages: page("1-2"), Processes: probeOK()},
		{Action: "read", Pages: page("bad"), Processes: probeOK()},
		{Action: "read", Pages: page("1-"), Processes: probeOK()},
		{Action: "read", Pages: page("1"), Entries: []string{"page-2.jpg", "page-01.jpg", "PAGE.JPG", "junk"}, Images: map[string]string{"page-2.jpg": "/9j/Ag==", "page-01.jpg": "/9j/AQ=="}, Processes: probeOK()},
		{Action: "read", Pages: page("1"), Entries: []string{"page-\ue000.jpg", "page-😀.jpg"}, Images: map[string]string{"page-\ue000.jpg": "/9j/Ag==", "page-😀.jpg": "/9j/AQ=="}, Processes: probeOK()},
		{Action: "read", Pages: page("1"), ReadError: "page read failed", Processes: probeOK()},
		{Action: "read", Pages: page("1"), Size: size(0)},
		{Action: "read", Size: size(ExtractThreshold), Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Size: size(ExtractThreshold + 1), Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 0}, {Code: 0}}},
		{Action: "read", Size: size(RawLimit + 1), Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 0}, {Code: 0}}},
		{Action: "read", Size: size(ExtractLimit + 1), Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Size: size(ExtractThreshold + 1), Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 1}}},
		{Action: "read", Model: "anthropic.claude-3-haiku-20240307-v1:0", Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 0}, {Code: 0}}},
		{Action: "read", Model: "CLAUDE-3-HAIKU@date", Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 1}}},
		{Action: "read", Pages: page("1"), Model: "claude-3-haiku", Processes: probeOK()},
		{Action: "read", Bytes: base64.StdEncoding.EncodeToString([]byte("html renamed pdf")), Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Stats: []sourceStats{{Size: 8, Mtime: 1234.75}, {Size: 8, Mtime: 2234.75}}, Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
	}
	comparePDFCases(t, rows)
}

func TestActualSourcePDFSessionPromotionRetainsAvailabilityCache(t *testing.T) {
	comparePDFCases(t, []sourcePDFCase{{Action: "promote", Processes: []sourceProcess{{Code: 99, Stderr: "version"}, {Code: 0}, {Code: 0}, {Code: 0}}}})
}

func TestActualSourcePDFStartedReadIgnoresOrdinaryToolAbort(t *testing.T) {
	source, _ := actualPDFSource(t, map[string]any{"rows": []sourcePDFCase{
		{Action: "read", AbortAfterProcess: 1, Processes: []sourceProcess{{Stdout: "Pages: 1"}}},
		{Action: "read", Pages: page("1"), AbortAfterProcess: 2, Processes: probeOK()},
		{Action: "read", Size: size(ExtractThreshold + 1), AbortAfterProcess: 3, Processes: []sourceProcess{{Stdout: "Pages: 1"}, {Code: 0}, {Code: 0}}},
	}})
	var rows []protocol.JSONObject
	_ = json.Unmarshal(source.Get("rows"), &rows)
	if len(rows) != 3 {
		t.Fatal("Source aborted read rows", string(source.Get("rows")))
	}
	for index, row := range rows {
		output, _ := row.Object("output")
		data, _ := output.Object("data")
		if string(output.Get("aborted")) != "true" || output.Get("thrown") != nil || output.Get("blocks") == nil || data.String("type") == "" {
			t.Fatalf("actual Source cancelled an already-started PDF tool %d: %s", index, row.Get("output"))
		}
		var cache []json.RawMessage
		_ = json.Unmarshal(output.Get("cache"), &cache)
		if (index == 1 && len(cache) != 0) || (index != 1 && len(cache) != 1) {
			t.Fatal("Source cancelled cache semantics", index, string(output.Get("cache")))
		}
	}
}
