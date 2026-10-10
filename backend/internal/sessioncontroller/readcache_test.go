package sessioncontroller

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

func TestReadCacheRetainsUTF16EvictionAndObserverOwnership(t *testing.T) {
	cache := NewReadCache()
	for index := 0; index < 12; index++ {
		cache.Set(filepath.Join("/fixture", fmtInt(index)), FileState{quote("content"), float64(index)})
	}
	if len(cache.Entries()) != 10 {
		t.Fatal("entry budget changed")
	}
	if _, exists := cache.Get("/fixture/0"); exists {
		t.Fatal("oldest not evicted")
	}
	value, exists := cache.Get("/fixture/2")
	if !exists || value.Timestamp != 2 {
		t.Fatal("retained entry missing")
	}
	value.Content[1] = 'X'
	original, _ := cache.Get("/fixture/2")
	if text(original.Content) != "content" {
		t.Fatal("read observer mutated cache")
	}
	clone := cache.Clone()
	clone.Delete("/fixture/2")
	if _, exists := cache.Get("/fixture/2"); !exists {
		t.Fatal("clone deleted original")
	}
	cache.Set("/fixture/empty", FileState{quote(""), math.NaN()})
	if byteSize(quote("")) != 1 {
		t.Fatal("Source minimum entry size lost")
	}
	data, _ := json.Marshal(FileState{quote("x"), math.Inf(1)})
	if !strings.Contains(string(data), `"timestamp":null`) {
		t.Fatal("JS JSON NaN/Infinity not null")
	}
	cache.Set("/fixture/too-big", FileState{quote(strings.Repeat("x", 25*1024*1024+1)), 0})
	if _, exists := cache.Get("/fixture/too-big"); exists {
		t.Fatal("oversized file retained")
	}
	cache = NewReadCache()
	cache.Set("a", FileState{quote(strings.Repeat("a", 14*1024*1024)), 0})
	cache.Set("b", FileState{quote(strings.Repeat("b", 14*1024*1024)), 0})
	if len(cache.Entries()) != 1 {
		t.Fatal("byte budget not enforced")
	}
}

func TestReadCacheCloneKeepsCallerCapacityAndIndependentLRU(t *testing.T) {
	for _, capacity := range []int{10, 100} {
		t.Run(fmtInt(capacity), func(t *testing.T) {
			cache := newReadCache(capacity)
			for index := 0; index < capacity; index++ {
				cache.Set(filepath.Join("/fixture", fmtInt(index)), FileState{quote("original"), float64(index)})
			}
			clone := cache.Clone()
			if clone.maxEntries != capacity || len(clone.Entries()) != capacity {
				t.Fatal("clone reset caller capacity", clone.maxEntries, len(clone.Entries()))
			}
			value, exists := clone.Get("/fixture/0")
			if !exists {
				t.Fatal("clone dropped oldest retained entry")
			}
			value.Content[1] = 'X'
			clone.Set(filepath.Join("/fixture", fmtInt(capacity)), FileState{quote("new"), 101})
			if _, exists := clone.Get("/fixture/1"); exists || len(clone.Entries()) != capacity {
				t.Fatal("clone did not apply retained capacity and refreshed LRU order")
			}
			if got, exists := cache.Get("/fixture/1"); !exists || text(got.Content) != "original" {
				t.Fatal("clone changed original eviction/content state")
			}
			if got, exists := clone.Get("/fixture/0"); !exists || text(got.Content) != "original" {
				t.Fatal("observer changed clone content")
			}
			if _, exists := cache.Get(filepath.Join("/fixture", fmtInt(capacity))); exists {
				t.Fatal("clone inserted into original cache")
			}
		})
	}
	home := t.TempDir()
	controller, err := New(hostOptions(t, home).options)
	if err != nil {
		t.Fatal(err)
	}
	if controller.ReadCache().maxEntries != 100 {
		t.Fatal("headless initialization selected standalone helper capacity")
	}
}

func TestStandaloneReadExtractionRetainsTenEntryDefault(t *testing.T) {
	home := t.TempDir()
	history := []*protocol.JSONObject{}
	for index := 0; index < 12; index++ {
		messages := toolMessages("Read", raw(map[string]string{"file_path": "file-" + fmtInt(index)}), quote("1→original "+fmtInt(index)), nil)
		for position, field := range []string{"id", "tool_use_id"} {
			blocks, err := parseObjects(object(messages[position].Get("message")).Get("content"))
			if err != nil {
				t.Fatal(err)
			}
			blocks[0].Set(field, quote("read-"+fmtInt(index)))
			messages[position].Set("message", raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw(blocks)})))
		}
		history = append(history, messages...)
	}
	cache, err := ExtractReadCache(history, home, home, OSFiles{}, sessionprojection.ParseDateUTC)
	if err != nil {
		t.Fatal(err)
	}
	if cache.maxEntries != 10 || len(cache.Entries()) != 10 || cache.Clone().maxEntries != 10 {
		t.Fatal("headless caller changed standalone extraction default")
	}
	if _, exists := cache.Get(filepath.Join(home, "file-0")); exists {
		t.Fatal("standalone oldest entry was not evicted")
	}
	if value, exists := cache.Get(filepath.Join(home, "file-2")); !exists || text(value.Content) != "original 2" {
		t.Fatal("standalone retained original Read result lost")
	}
}
func TestReadPathAndDiskCompatibilityBoundaries(t *testing.T) {
	home := t.TempDir()
	for _, row := range []struct{ raw, expected string }{{`""`, home}, {`"~"`, home}, {`"~/file"`, filepath.Join(home, "file")}, {`"  relative  "`, filepath.Join(home, "relative")}, {`"a/e\u0301"`, filepath.Join(home, "a", "é")}} {
		value, err := expandFilePath([]byte(row.raw), home, home)
		if err != nil || value != row.expected {
			t.Fatal(row, value, err)
		}
	}
	for _, raw := range []string{`null`, `"null\u0000path"`, `"\ud800"`} {
		if _, err := expandFilePath([]byte(raw), home, home); err == nil {
			t.Fatal("bad path accepted", raw)
		}
	}
	if got := decodeDisk([]byte{0xff, 0xfe, 0x00, 0xd8, 0x0d, 0, 0x0a, 0}); string(got) != `"`+"\ufeff"+`\ud800\n"` {
		t.Fatal(string(got))
	}
	if got := text(decodeDisk([]byte("a\r\nb"))); got != "a\nb" {
		t.Fatal(got)
	}
	if got := string(stripReadContent([]byte(`"  1→a\ud800\n2\tb<system-reminder>hidden</system-reminder>\n3→c\r\n"`))); got != `"a\ud800\nb\n3→c"` {
		t.Fatal(got)
	}
	when, err := timestamp(quote("2025-99-01T00:00:00Z"), sessionprojection.ParseDateUTC)
	if err != nil || !math.IsNaN(when) {
		t.Fatal(when, err)
	}
	if _, err = timestamp([]byte("3"), sessionprojection.ParseDateUTC); err == nil {
		t.Fatal("non-string timestamp profile accepted")
	}
	if _, err = timestamp(quote("legacy local date"), sessionprojection.ParseDateUTC); err == nil {
		t.Fatal("unsupported legacy date accepted")
	}
	if !inaccessible(syscall.ELOOP) || !inaccessible(syscall.ENOTDIR) || !inaccessible(syscall.EPERM) || inaccessible(syscall.EIO) {
		t.Fatal("inaccessible disk error ownership changed")
	}
}
func toolMessages(name string, input, content json.RawMessage, extra []protocol.JSONField) []*protocol.JSONObject {
	block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_use")}, protocol.JSONField{Name: "id", Value: quote("id")}, protocol.JSONField{Name: "name", Value: quote(name)}, protocol.JSONField{Name: "input", Value: input})
	result := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: quote("id")}, protocol.JSONField{Name: "content", Value: content})
	for _, field := range extra {
		result.Set(field.Name, field.Value)
	}
	assistant := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw([]protocol.JSONObject{block})}))})
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "timestamp", Value: quote("2025-01-01T00:00:00Z")}, protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw([]protocol.JSONObject{result})}))})
	return []*protocol.JSONObject{&assistant, &user}
}
func TestReadCacheNativeFileAndInvalidOriginalInputs(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "edit.txt")
	writePrivate(t, path, "actual\r\nfile")
	for _, name := range []string{"Read", "Write", "Edit"} {
		history := toolMessages(name, []byte(`{"file_path":"edit.txt","content":"written"}`), quote("1→seen"), nil)
		cache, err := ExtractReadCache(history, home, home, OSFiles{}, sessionprojection.ParseDateUTC)
		if err != nil {
			t.Fatal(err)
		}
		value, exists := cache.Get(path)
		if !exists {
			t.Fatal(name, "consumer did not seed")
		}
		expected := map[string]string{"Read": "seen", "Write": "written", "Edit": "actual\nfile"}[name]
		if text(value.Content) != expected {
			t.Fatal(name, text(value.Content))
		}
	}
	for _, row := range []struct {
		name, input, content string
		faults               *faultFiles
		want                 bool
	}{{"Write", `{"file_path":"x","content":3}`, `"ok"`, &faultFiles{}, true}, {"Read", `{"file_path":3}`, `"ok"`, &faultFiles{}, true}, {"Edit", `{"file_path":"edit.txt"}`, `"ok"`, &faultFiles{read: syscall.EIO}, true}, {"Edit", `{"file_path":"edit.txt"}`, `"ok"`, &faultFiles{stat: syscall.EIO}, true}, {"Edit", `{"file_path":"edit.txt"}`, `"ok"`, &faultFiles{read: syscall.EACCES}, false}, {"Read", `{"file_path":"x","offset":null}`, `"1→skip"`, &faultFiles{}, false}, {"Read", `{"file_path":"x"}`, string(quote(unchangedStub)), &faultFiles{}, false}, {"Read", `{"file_path":"x"}`, `[]`, &faultFiles{}, false}} {
		history := toolMessages(row.name, []byte(row.input), []byte(row.content), nil)
		cache, err := ExtractReadCache(history, home, home, row.faults, sessionprojection.ParseDateUTC)
		if (err != nil) != row.want {
			t.Fatal(row, err)
		}
		if !row.want && len(cache.Entries()) != 0 {
			t.Fatal("excluded cache input retained")
		}
	}
	history := toolMessages("Edit", []byte(`{"file_path":"edit.txt"}`), quote("failed"), []protocol.JSONField{{Name: "is_error", Value: raw(true)}})
	cache, err := ExtractReadCache(history, home, home, OSFiles{}, sessionprojection.ParseDateUTC)
	if err != nil || len(cache.Entries()) != 0 {
		t.Fatal("failed Edit touched disk")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	history = toolMessages("Edit", []byte(`{"file_path":"edit.txt"}`), quote("ok"), nil)
	cache, err = ExtractReadCache(history, home, home, OSFiles{}, sessionprojection.ParseDateUTC)
	if err != nil || len(cache.Entries()) != 0 {
		t.Fatal("missing Edit changed resume")
	}
}

func TestOriginalReadCacheRefusesUnsupportedIDsAndDatesBeforeSeeding(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"tool-use-id", "tool-result-id", "Read-time", "Write-time"} {
		t.Run(name, func(t *testing.T) {
			tool := "Read"
			if name == "Write-time" {
				tool = "Write"
			}
			history := toolMessages(tool, []byte(`{"file_path":"file","content":"written"}`), quote("1→read"), nil)
			if name == "tool-use-id" {
				blocks, _ := parseObjects(object(history[0].Get("message")).Get("content"))
				blocks[0].Set("id", raw(3))
				history[0].Set("message", raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw(blocks)})))
			} else if name == "tool-result-id" {
				blocks, _ := parseObjects(object(history[1].Get("message")).Get("content"))
				blocks[0].Set("tool_use_id", raw(3))
				history[1].Set("message", raw(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: raw(blocks)})))
			} else {
				history[1].Set("timestamp", quote("unported local date"))
			}
			if _, err := ExtractReadCache(history, home, home, OSFiles{}, sessionprojection.ParseDateUTC); err == nil {
				t.Fatal("unsupported original value silently seeded")
			}
		})
	}
	path := filepath.Join(home, "edit.txt")
	writePrivate(t, path, "actual")
	cache, err := ExtractReadCache(toolMessages("Edit", []byte(`{"file_path":"edit.txt"}`), quote("ok"), nil), home, home, &faultFiles{stat: syscall.EACCES}, sessionprojection.ParseDateUTC)
	if err != nil || len(cache.Entries()) != 0 {
		t.Fatal("native permission at post-read stat did not skip", err)
	}
}
