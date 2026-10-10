package clistartupenv

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
)

func newNativeObserverForTest(path string) *fixturePhysicalObserver {
	phase := "test"
	reads, allReads, entrances, calls := []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}
	complete := []Event{}
	return &fixturePhysicalObserver{paths: map[string]string{"user": path}, canonicalPaths: map[string]string{"user": path}, reads: &reads, allReads: &allReads, completeEvents: &complete, phase: &phase, entrances: &entrances, nativeCalls: &calls, live: map[uintptr]map[string]any{}}
}
func nativeOperationsForTest(f *fixturePhysicalObserver) []string {
	result := []string{}
	for _, row := range *f.nativeCalls {
		result = append(result, row["operation"].(string))
	}
	return result
}
func newNativeSettingsForTest(t *testing.T, path string, observer *fixturePhysicalObserver) (*clisettings.Service, *clisettings.State) {
	t.Helper()
	state, err := clisettings.NewState(func() float64 { return 946684800000 })
	if err != nil {
		t.Fatal(err)
	}
	service, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: filepath.Dir(path), OriginalCWD: filepath.Dir(path), Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 946684800000 }, PID: 12345, FS: fixtureSettingsFS{events: &[]Event{}, paths: map[string]bool{path: true}, home: filepath.Dir(path), observer: observer}})
	if err != nil {
		t.Fatal(err)
	}
	return service, state
}
func TestNativeFixtureActualPrefixOrderCacheAndEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := []byte(`{"env":{"PROBE":"中文"}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	observer := newNativeObserverForTest(resolved)
	service, state := newNativeSettingsForTest(t, path, observer)
	parsed, err := service.ParseFile(path)
	if err != nil || parsed.Settings == nil {
		t.Fatal(parsed, err)
	}
	want := []string{"OpenReadFile", "Fd", "ReadAt", "Close", "ReadFile"}
	if !reflect.DeepEqual(nativeOperationsForTest(observer), want) {
		t.Fatal(nativeOperationsForTest(observer))
	}
	if len(observer.live) != 0 || len(*observer.entrances) != 1 || len(*observer.allReads) != 1 {
		t.Fatal(observer.live, *observer.entrances, *observer.allReads)
	}
	sample := (*observer.nativeCalls)[2]
	args := sample["args"].(map[string]any)
	before, err := base64.StdEncoding.DecodeString(args["bufferBeforeBase64"].(string))
	if err != nil || !bytes.Equal(before, make([]byte, 4096)) {
		t.Fatal("sample before", err)
	}
	after, err := base64.StdEncoding.DecodeString(sample["bufferAfterBase64"].(string))
	if err != nil || !bytes.Equal(after[:len(raw)], raw) || !bytes.Equal(after[len(raw):], make([]byte, 4096-len(raw))) {
		t.Fatal("sample after", err)
	}
	if sample["result"].(map[string]any)["n"] != len(raw) || !sample["error"].(map[string]any)["isEOF"].(bool) {
		t.Fatal("actual raw short ReadAt EOF lost", sample)
	}
	if _, err = service.ParseFile(path); err != nil || len(*observer.nativeCalls) != 5 || len(*observer.entrances) != 1 {
		t.Fatal("cached parse observed extra native work", err)
	}
	state.ResetSettingsCache()
	if _, err = service.ParseFile(path); err != nil || len(*observer.nativeCalls) != 10 || len(*observer.entrances) != 2 {
		t.Fatal("reset did not observe real work", err)
	}
}
func TestNativeFixtureMissingStopsAtOriginalOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	observer := newNativeObserverForTest(path)
	service, state := newNativeSettingsForTest(t, path, observer)
	parsed, err := service.ParseFile(path)
	if err != nil || parsed.Settings != nil {
		t.Fatal(parsed, err)
	}
	if !reflect.DeepEqual(nativeOperationsForTest(observer), []string{"OpenReadFile"}) || len(*observer.allReads) != 0 || len(*observer.entrances) != 1 || len(observer.live) != 0 {
		t.Fatal(*observer.nativeCalls, *observer.allReads, *observer.entrances, observer.live)
	}
	failure := (*observer.nativeCalls)[0]["error"].(map[string]any)
	if failure["nativeType"] != "*fs.PathError" || !failure["isNotExist"].(bool) || failure["op"] != "open" || failure["path"] != path {
		t.Fatal("actual native original error lost", failure)
	}
	if err = os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if parsed, err = service.ParseFile(path); err != nil || parsed.Settings != nil || len(*observer.nativeCalls) != 1 {
		t.Fatal("cached missing mutated", parsed, err)
	}
	state.ResetSettingsCache()
	if parsed, err = service.ParseFile(path); err != nil || parsed.Settings == nil || len(*observer.nativeCalls) != 6 {
		t.Fatal("reset missing", parsed, err, *observer.nativeCalls)
	}
}

type nativeObserverIdentityHandle struct {
	calls                 []string
	originalBuffer        *[]byte
	readError, closeError error
}

func (h *nativeObserverIdentityHandle) Fd() uintptr { h.calls = append(h.calls, "Fd"); return 77 }
func (h *nativeObserverIdentityHandle) ReadAt(buffer []byte, offset int64) (int, error) {
	h.calls = append(h.calls, "ReadAt")
	if offset != 9 {
		panic("observer replaced actual offset")
	}
	*h.originalBuffer = buffer
	buffer[0] = 99
	return 1, h.readError
}
func (h *nativeObserverIdentityHandle) Close() error {
	h.calls = append(h.calls, "Close")
	return h.closeError
}
func TestNativeObserverReturnsOriginalBufferResultsAndErrorIdentity(t *testing.T) {
	originalBuffer := []byte{}
	readFault := errors.New("native read fault")
	closeFault := errors.New("native close fault")
	for _, readError := range []error{readFault, io.EOF} {
		observer := newNativeObserverForTest("/fixture/file")
		handle := &nativeObserverIdentityHandle{originalBuffer: &originalBuffer, readError: readError, closeError: closeFault}
		open := observer.start("OpenReadFile", map[string]any{"path": "/fixture/file"}, nil)
		wrapper := &fixtureNativeHandle{original: handle, observer: observer, openRow: open, path: "/fixture/file"}
		if wrapper.Fd() != 77 {
			t.Fatal("original descriptor changed")
		}
		buffer := make([]byte, 16)
		n, err := wrapper.ReadAt(buffer, 9)
		if n != 1 || err != readError || buffer[0] != 99 || &originalBuffer[0] != &buffer[0] {
			t.Fatal("observer replaced argument/result/error identity", n, err)
		}
		if err = wrapper.Close(); err != closeFault {
			t.Fatal("observer replaced close error", err)
		}
		if !reflect.DeepEqual(handle.calls, []string{"Fd", "ReadAt", "Close"}) {
			t.Fatal("observer retried native work", handle.calls)
		}
		if len(observer.live) != 1 {
			t.Fatal("failed close hidden", observer.live)
		}
	}
}
func TestNativeFixtureHomeAliasGuards(t *testing.T) {
	for _, home := range []string{"/var/folders/private-test", "/private/var/folders/private-test"} {
		if !fixtureNativeHomeAllowed(home, home, home+"/.claude", "/var/folders") {
			t.Fatal("declared private alias refused", home)
		}
		if fixtureNativeHomeAllowed(home, home+"/wrong", home+"/.claude", "/var/folders") || fixtureNativeHomeAllowed(home, home, home+"/wrong", "/var/folders") {
			t.Fatal("invalid HOME/config permitted")
		}
	}
	if fixtureNativeHomeAllowed("/Users/real", "/Users/real", "/Users/real/.claude", "/var/folders") {
		t.Fatal("non-private HOME permitted")
	}
}
