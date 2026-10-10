package sessionhistory

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func actualOptions(events *[]Diagnostic, mutex *sync.Mutex) Options {
	return Options{Files: sessionstore.OSFileSystem{}, Stat: func(path string) (int64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: os.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: CompareStrings, OnDiagnostic: func(value Diagnostic) {
		mutex.Lock()
		defer mutex.Unlock()
		*events = append(*events, value)
	}}
}
func nativeLoader(t *testing.T) (*Loader, *[]Diagnostic) {
	t.Helper()
	events := []Diagnostic{}
	value, err := New(actualOptions(&events, &sync.Mutex{}))
	if err != nil {
		t.Fatal(err)
	}
	return value, &events
}
func writeHistory(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestActualSourceContractsFromIndependentFiles(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Source string
		Cases  []struct {
			Content  string
			Disabled bool
			Steps    []struct {
				Operation     string
				KeepAllLeaves bool
			}
			Expected struct {
				Results []struct {
					Bytes string
					Error map[string]string
				}
				Events []struct {
					EventName string
					Metadata  json.RawMessage
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contract.Source, "Bun 1.3.14 actual") || len(contract.Cases) < 20 {
		t.Fatalf("missing actual Source evidence: %s/%d", contract.Source, len(contract.Cases))
	}
	for index, sample := range contract.Cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			path := writeHistory(t, sample.Content)
			loader, events := nativeLoader(t)
			loader.options.DisablePrecompactSkip = func() bool { return sample.Disabled }
			for index, step := range sample.Steps {
				var value any
				var err error
				if step.Operation == "path" {
					value, err = loader.LoadMessagesFromJSONLPath(path)
				} else {
					value, err = loader.LoadTranscriptFile(path, step.KeepAllLeaves)
				}
				expected := sample.Expected.Results[index]
				if err != nil {
					name := "Error"
					if failure, ok := err.(interface{ ErrorName() string }); ok {
						name = failure.ErrorName()
					}
					if !reflect.DeepEqual(expected.Error, map[string]string{"name": name, "code": "ERROR", "message": err.Error()}) {
						t.Fatalf("Source error %v != %v", expected.Error, err)
					}
					continue
				}
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				if expected.Bytes != hex.EncodeToString(raw) {
					t.Fatalf("Source raw result differs at step %d\n%s", index, raw)
				}
			}
			if len(*events) != len(sample.Expected.Events) {
				t.Fatalf("Source event count %d != %d", len(sample.Expected.Events), len(*events))
			}
			for index, expected := range sample.Expected.Events {
				event := (*events)[index]
				var compact bytes.Buffer
				if err := json.Compact(&compact, expected.Metadata); err != nil {
					t.Fatal(err)
				}
				if event.Code != expected.EventName || !bytes.Equal(event.Fields, compact.Bytes()) {
					t.Fatalf("Source diagnostic differs: %#v != %#v", event, expected)
				}
			}
			final, err := os.ReadFile(path)
			if err != nil || string(final) != sample.Content {
				t.Fatal("read-only reducer mutated transcript", err)
			}
		})
	}
}

func TestMetadataChunkFilteringAndIncompleteCarry(t *testing.T) {
	loader, _ := nativeLoader(t)
	meta := `{"type":"tag","sessionId":"s","tag":"preserved"}`
	for _, before := range []string{
		strings.Repeat("x", 64*1024-5) + "\n",
		strings.Repeat("x", 64*1024-40) + "\n",
		strings.Repeat("x", 64*1024+20) + "\n",
		strings.Repeat("x", 2*64*1024+20) + "\n",
		strings.Repeat("x", 64*1024) + "\n",
	} {
		for _, eof := range []bool{false, true} {
			content := before + meta
			if !eof {
				content += "\n"
			}
			path := writeHistory(t, content)
			result, err := loader.preMetadata(path, int64(len(content)))
			if err != nil || string(result) != meta+map[bool]string{false: "\n", true: ""}[eof] {
				t.Fatalf("metadata lost after %d bytes/eof=%v: %q/%v", len(before), eof, result, err)
			}
		}
	}
	content := `{"type":"custom-title","sessionId":"s","customTitle":"` + strings.Repeat("x", 64*1024+20) + `"}` + "\n" + meta + "\n"
	path := writeHistory(t, content)
	result, err := loader.preMetadata(path, int64(len(content)))
	if err != nil || !bytes.Contains(result, []byte(`"custom-title"`)) || !bytes.HasSuffix(result, []byte(meta+"\n")) {
		t.Fatal("metadata prefix carry was discarded", err)
	}
	for _, content := range []string{"", "unrecognized tail", strings.Repeat("x", 128*1024)} {
		result, err := loader.preMetadata(writeHistory(t, content), int64(len(content)))
		if err != nil || len(result) != 0 {
			t.Fatal(result, err)
		}
	}
}
func TestPreparseRawParentChainKeepsMetadataAndIgnoresNestedUUIDs(t *testing.T) {
	root := row(nativeUUID, "null", "user", "")
	next := "11111111-2222-3333-4444-000000000002"
	dead := "11111111-2222-3333-4444-000000000003"
	meta := `{"type":"summary","leafUuid":"s","summary":"preserved"}` + "\n"
	live := row(next, string(quote(nativeUUID)), "user", `,"nested":{"uuid":"11111111-2222-3333-4444-000000000099","timestamp":"nested","text":"\\\"{x}"}`)
	orphan := row(dead, "null", "user", `,"padding":"`+strings.Repeat("x", 8192)+`"`)
	data := []byte(root + orphan + meta + live)
	if got := walkBeforeParse(data); string(got) != root+meta+live {
		t.Fatal("nested UUID poisoned raw parent index", string(got))
	}
	for _, content := range []string{root, strings.Replace(root, `"type":"user"`, `"type":"user","isSidechain":true`, 1), "no indexed lines\n", `{"parentUuid":null,"type":"progress"}` + "\n"} {
		if got := walkBeforeParse([]byte(content)); string(got) != content {
			t.Fatal("unindexed/short transcript filtered", string(got))
		}
	}
	noSuffix := strings.Replace(row(next, string(quote(nativeUUID)), "user", ""), `"uuid":"`+next+`","timestamp":"2025-01-01T00:00:01Z"`, `"timestamp":"2025-01-01T00:00:01Z","uuid":"`+next+`"`, 1)
	if got := walkBeforeParse([]byte(root + orphan + noSuffix)); string(got) != root+noSuffix {
		t.Fatal("old field-order fallback lost chain", string(got))
	}
	cycleRoot := row(nativeUUID, string(quote(next)), "user", "")
	cycleLeaf := row(next, string(quote(nativeUUID)), "assistant", "")
	if got := walkBeforeParse([]byte(orphan + cycleRoot + cycleLeaf)); string(got) != cycleRoot+cycleLeaf {
		t.Fatal("cycle did not stop safely", string(got))
	}
}
func TestUTF16IdentityCollationAndUnsupportedDateProfiles(t *testing.T) {
	for _, sample := range []struct {
		left, right string
		want        int
	}{{`"a"`, `"B"`, -1}, {`"😀"`, `"😀"`, 0}, {`"B"`, `"a"`, 1}} {
		order, err := CompareStrings(json.RawMessage(sample.left), json.RawMessage(sample.right))
		if err != nil || order != sample.want {
			t.Fatal(order, err)
		}
	}
	for _, value := range []string{`null`, `"\ud800"`, `"\udfff"`} {
		if _, err := CompareStrings(json.RawMessage(value), quote("a")); !errors.Is(err, ErrUnsupportedCollation) {
			t.Fatal(err)
		}
	}
	if key(json.RawMessage("-0.0"), 0) != key(json.RawMessage("0e400"), 0) || key(json.RawMessage("9007199254740993"), 0) != key(json.RawMessage("9007199254740992"), 0) || key(nil, 0) == key(json.RawMessage("null"), 0) {
		t.Fatal("JS map primitive identity changed")
	}
	if key(json.RawMessage(`{}`), 1) == key(json.RawMessage(`{}`), 2) {
		t.Fatal("object keys collapsed")
	}
	if object(json.RawMessage(" \n")).IsObject() {
		t.Fatal("whitespace became an object")
	}
	loader, _ := nativeLoader(t)
	if _, _, err := loader.date(quote("2025/01/02")); err == nil {
		t.Fatal("unsupported legacy Date.parse silently accepted")
	}
	if _, valid, err := loader.date(json.RawMessage("1e400")); err != nil || valid {
		t.Fatal(valid, err)
	}
	if _, _, err := loader.date(json.RawMessage(`{}`)); err == nil {
		t.Fatal("object Date.parse silently accepted")
	}
	if err := loader.sortByTimestamp([]record{{value: object(json.RawMessage(`{"timestamp":"\ud800"}`))}, {value: object(json.RawMessage(`{"timestamp":"valid"}`))}}); !errors.Is(err, ErrUnsupportedCollation) {
		t.Fatal(err)
	}
}
func TestPrunedAuxiliaryLeavesAndExplicitMalformedSnipCycle(t *testing.T) {
	loader, events := nativeLoader(t)
	root := row("root", "null", "user", "")
	content := root + row("real", `"root"`, "assistant", "") + row("aux", `"root"`, "system", "") + row("a", `"b"`, "attachment", "") + row("b", `"a"`, "system", "") + row("terminal", `"a"`, "system", "")
	loader.options.LeafPrune = func() bool { return true }
	value, err := loader.LoadTranscriptFile(writeHistory(t, content), true)
	if err != nil || len(value.Leaves) != 1 || value.Leaves[0] != key(quote("real"), -1) || len(*events) != 1 || (*events)[0].Code != "tengu_transcript_parent_cycle" {
		t.Fatal(value.Leaves, *events, err)
	}
	cycle := row("a", `"b"`, "user", "") + row("b", `"a"`, "assistant", "") + row("live", `"a"`, "user", `,"snipMetadata":{"removedUuids":["a","b"]}`)
	if _, err := loader.LoadTranscriptFile(writeHistory(t, cycle), true); !errors.Is(err, ErrMalformedSnipCycle) {
		t.Fatal("Source nonterminating snip cycle must be explicit unsupported error", err)
	}
	unsupported := row("root", "null", "user", `,"snipMetadata":{"removedUuids":{"future":true}}`)
	path := writeHistory(t, unsupported)
	if _, err := loader.LoadTranscriptFile(path, true); !errors.Is(err, ErrUnsupportedSnipIterator) {
		t.Fatal("unsupported Bun error rendering silently succeeded", err)
	}
	if bytes, err := os.ReadFile(path); err != nil || string(bytes) != unsupported {
		t.Fatal("unsupported input mutated transcript", err)
	}
}
func row(id, parent, kind string, fields string) string {
	return fmt.Sprintf(`{"parentUuid":%s,"type":%q,"message":{"role":%q,"content":"raw"},"uuid":%q,"timestamp":"2025-01-01T00:00:01Z"%s}`, parent, kind, kind, id, fields) + "\n"
}

const nativeUUID = "11111111-2222-3333-4444-000000000001"

func TestFreshReadFailureRecoveryAndConcurrentCalls(t *testing.T) {
	loader, events := nativeLoader(t)
	path := filepath.Join(t.TempDir(), "new.jsonl")
	value, err := loader.LoadMessagesFromJSONLPath(path)
	if err != nil || len(value.Messages) != 0 {
		t.Fatal(value, err)
	}
	content := row(nativeUUID, "null", "user", "")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var pending sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		pending.Add(1)
		go func() {
			defer pending.Done()
			result, err := loader.LoadMessagesFromJSONLPath(path)
			if err != nil {
				failures <- err
			} else if len(result.Messages) != 1 {
				failures <- fmt.Errorf("missing fresh history: %v", result)
			}
		}()
	}
	pending.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if len(*events) != 0 {
		t.Fatal(*events)
	}
	if err := os.WriteFile(path, []byte("null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = loader.LoadMessagesFromJSONLPath(path)
	if err != nil || len(value.Messages) != 0 {
		t.Fatal("stale raw messages after rewrite", value, err)
	}
	if _, err := loader.LoadTranscriptFile("\xff", false); !errors.Is(err, sessionprojection.ErrUnsupportedPath) {
		t.Fatal(err)
	}
}
func TestRequiredDependenciesAreExplicit(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("accepted absent production dependencies")
	}
	events := []Diagnostic{}
	options := actualOptions(&events, &sync.Mutex{})
	for _, remove := range []func(*Options){func(o *Options) { o.Files = nil }, func(o *Options) { o.Stat = nil }, func(o *Options) { o.ReadFile = nil }, func(o *Options) { o.DisablePrecompactSkip = nil }, func(o *Options) { o.LeafPrune = nil }, func(o *Options) { o.ParseDate = nil }, func(o *Options) { o.Compare = nil }, func(o *Options) { o.OnDiagnostic = nil }} {
		copy := options
		remove(&copy)
		if _, err := New(copy); err == nil {
			t.Fatal("accepted absent dependency")
		}
	}
}
func TestLargeCompactionSeamsSnapshotsAndMetadata(t *testing.T) {
	loader, _ := nativeLoader(t)
	const boundary = `{"type":"system","padding":"` + "PADDING" + `","subtype":"compact_boundary","uuid":"boundary"}` + "\n"
	for _, carry := range []int{8, 40} {
		start := 6*1024*1024 - carry
		head := row(nativeUUID, "null", "user", "")
		head = strings.Replace(head, `"content":"raw"`, `"content":"`+strings.Repeat("x", start-len(head)+3)+`"`, 1)
		if len(head) != start {
			t.Fatal(len(head), start)
		}
		content := head + strings.Replace(boundary, "PADDING", strings.Repeat("y", 320), 1) + row("last", `"boundary"`, "user", "")
		path := writeHistory(t, content)
		result, err := loader.LoadTranscriptFile(path, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := result.messages[key(quote(nativeUUID), -1)]; exists != (carry == 8) {
			t.Fatal("short-carry Source branch changed", carry)
		}
	}
	large := strings.Repeat("x", 6*1024*1024)
	content := `{"type":"custom-title","sessionId":"s","customTitle":"before"}` + "\n" + row(nativeUUID, "null", "user", `,"huge":"`+large+`"`) + `{"type":"attribution-snapshot","messageId":"old","huge":"` + large + `"}` + "\n" + `{"type":"system","subtype":"compact_boundary","uuid":"boundary"}` + "\n" + row("last", `"boundary"`, "user", "") + `{"type":"attribution-snapshot","messageId":"last","unknown":true}`
	path := writeHistory(t, content)
	result, err := loader.LoadTranscriptFile(path, false)
	if err != nil || len(result.messages) != 2 || len(result.maps["attributionSnapshots"].keys) != 1 || len(result.maps["customTitles"].keys) != 1 {
		t.Fatal("compact metadata/snapshot boundary", err)
	}
	loader.options.DisablePrecompactSkip = func() bool { return true }
	result, err = loader.LoadTranscriptFile(path, false)
	if err != nil || len(result.messages) != 3 || len(result.maps["attributionSnapshots"].keys) != 2 {
		t.Fatal("kill switch changed", err)
	}
}

type faultFiles struct {
	open error
	file *faultFile
}

func (files faultFiles) Open(string) (sessionstore.File, error) { return files.file, files.open }

type faultFile struct {
	reads int
	read  error
	close error
}

func (file *faultFile) ReadAt([]byte, int64) (int, error)    { file.reads++; return 0, file.read }
func (file *faultFile) Close() error                         { return file.close }
func (file *faultFile) Stat() (sessionstore.Snapshot, error) { return sessionstore.Snapshot{}, nil }
func TestRealReadBoundariesPropagateFaultsInternallyAndSourceSwallowsLoadFaults(t *testing.T) {
	loader, _ := nativeLoader(t)
	failure := errors.New("private fixture read fault")
	loader.options.Files = faultFiles{open: failure}
	if _, err := loader.readLarge("private", 9); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := loader.preMetadata("private", 9); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	file := &faultFile{read: failure}
	loader.options.Files = faultFiles{file: file}
	if _, err := loader.readLarge("private", 9); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := loader.preMetadata("private", 9); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	closeFailure := errors.New("private fixture close fault")
	file.close = closeFailure
	if _, err := loader.readLarge("private", 9); !errors.Is(err, closeFailure) {
		t.Fatal(err)
	}
	if _, err := loader.preMetadata("private", 9); !errors.Is(err, closeFailure) {
		t.Fatal(err)
	}
	file.read = io.EOF
	file.close = nil
	if value, err := loader.readLarge("private", 9); err != nil || value.data == nil {
		t.Fatal(value, err)
	}
	if value, err := loader.preMetadata("private", 9); err != nil || len(value) != 0 {
		t.Fatal(value, err)
	}
	loader.options.Stat = func(string) (int64, error) { return SkipPrecompactThreshold + 1, nil }
	file.read = failure
	if value, err := loader.LoadTranscriptFile("private", false); err != nil || len(value.messages) != 0 {
		t.Fatal("Source read catch changed", value, err)
	}
	loader.options.DisablePrecompactSkip = func() bool { return true }
	loader.options.ReadFile = func(string) ([]byte, error) { return nil, failure }
	if value, err := loader.LoadTranscriptFile("private", false); err != nil || len(value.messages) != 0 {
		t.Fatal("Source readFile catch changed", value, err)
	}
}
