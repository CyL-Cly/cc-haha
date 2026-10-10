package clisettings

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

type prefixHandle struct {
	fd                uintptr
	sample            []byte
	readErr, closeErr error
	calls             *[]string
	t                 *testing.T
}

func (h *prefixHandle) Fd() uintptr { return h.fd }
func (h *prefixHandle) ReadAt(buffer []byte, offset int64) (int, error) {
	*h.calls = append(*h.calls, "sample")
	if len(buffer) != 4096 || offset != 0 || !bytes.Equal(buffer, make([]byte, 4096)) {
		h.t.Fatal("encoding probe must receive zero-filled4096 buffer and position0")
	}
	return copy(buffer, h.sample), h.readErr
}
func (h *prefixHandle) Close() error {
	*h.calls = append(*h.calls, "close")
	return h.closeErr
}

type prefixFiles struct {
	OSFileSystem
	handle           *prefixHandle
	full             []byte
	openErr, fullErr error
	calls            []string
	helperPaths      []string
}

func (f *prefixFiles) OpenReadFile(path string) (ReadHandle, error) {
	f.calls = append(f.calls, "open")
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.handle.calls = &f.calls
	return f.handle, nil
}
func (f *prefixFiles) ReadFile(path string) ([]byte, error) {
	f.calls = append(f.calls, "full")
	return append([]byte(nil), f.full...), f.fullErr
}
func (f *prefixFiles) ContentReadStarted(path string) { f.helperPaths = append(f.helperPaths, path) }

func leBytes(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

func TestContentEncodingUsesClosedPrefixBeforeIndependentFullRead(t *testing.T) {
	cases := []struct {
		name         string
		sample, full []byte
		want         string
	}{
		{"shortUTF8", []byte("old"), []byte("new\r\n中文🙂"), "new\n中文🙂"},
		{"emptySample", nil, []byte("中文🙂"), "中文🙂"},
		{"UTF16FromSample", []byte{0xff, 0xfe}, leBytes("changed\r\n中文"), "changed\n中文"},
		{"FullBOMCannotChangeUTF8", []byte("x"), []byte{0xff, 0xfe, 'A', 0}, "��A\x00"},
		{"BESampleIsUTF8", []byte{0xfe, 0xff}, []byte("unchanged"), "unchanged"},
		{"SingleFFSampleIsUTF8", []byte{0xff}, []byte("unchanged"), "unchanged"},
		{"UTF16OddTrailingByteIgnored", []byte{0xff, 0xfe}, []byte{'A', 0, 'x'}, "A"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := &prefixFiles{handle: &prefixHandle{t: t, fd: 9, sample: test.sample, readErr: io.EOF}, full: test.full}
			got, err := readContent(f, filepath.Join(t.TempDir(), "settings.json"))
			if err != nil || got != test.want {
				t.Fatalf("content %q err %v want %q", got, err, test.want)
			}
			if !reflect.DeepEqual(f.calls, []string{"open", "sample", "close", "full"}) {
				t.Fatalf("actual boundary order %v", f.calls)
			}
		})
	}
}

func TestContentPrefixErrorAndClosePrecedence(t *testing.T) {
	openErr := errors.New("original open error")
	readErr := errors.New("original sample error")
	closeErr := errors.New("original close error")
	fullErr := errors.New("original full error")
	cases := []struct {
		name                    string
		fd                      uintptr
		open, read, close, full error
		want                    error
		calls                   []string
	}{
		{"OpenFailure", 9, openErr, nil, nil, nil, openErr, []string{"open"}},
		{"ReadFailureStillCloses", 9, nil, readErr, nil, nil, readErr, []string{"open", "sample", "close"}},
		{"CloseFailureReplacesRead", 9, nil, readErr, closeErr, nil, closeErr, []string{"open", "sample", "close"}},
		{"CloseFailureStopsFull", 9, nil, nil, closeErr, nil, closeErr, []string{"open", "sample", "close"}},
		{"FullFailureAfterClose", 9, nil, nil, nil, fullErr, fullErr, []string{"open", "sample", "close", "full"}},
		{"FDZeroSkipsExplicitClose", 0, nil, readErr, closeErr, nil, readErr, []string{"open", "sample"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := &prefixFiles{openErr: test.open, fullErr: test.full, handle: &prefixHandle{t: t, fd: test.fd, readErr: test.read, closeErr: test.close}}
			_, err := readContent(f, filepath.Join(t.TempDir(), "settings.json"))
			if err != test.want || !reflect.DeepEqual(f.calls, test.calls) {
				t.Fatalf("identity/order: %v %v", err, f.calls)
			}
		})
	}
}

func TestNativePrefixMissingAndShortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_, _, err := readEncodingSample(OSFileSystem{}, path)
	var failure *os.PathError
	if !errors.As(err, &failure) || failure.Op != "open" || !os.IsNotExist(err) {
		t.Fatalf("missing must fail at native open: %v", err)
	}
	if err = os.WriteFile(path, []byte("{\"a\":1}"), 0600); err != nil {
		t.Fatal(err)
	}
	buffer, n, err := readEncodingSample(OSFileSystem{}, path)
	if err != nil || n != 7 || len(buffer) != 4096 || string(buffer[:n]) != "{\"a\":1}" || !bytes.Equal(buffer[n:], make([]byte, 4096-n)) {
		t.Fatalf("actual short native sample n%d err%v", n, err)
	}
	got, err := readContent(OSFileSystem{}, path)
	if err != nil || got != "{\"a\":1}" {
		t.Fatalf("full %q err%v", got, err)
	}
}

func TestParserUsesOuterResolvedHelperAndOriginalCacheKey(t *testing.T) {
	dir := t.TempDir()
	actual := filepath.Join(dir, "actual.json")
	alias := filepath.Join(dir, "alias.json")
	if err := os.WriteFile(actual, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		t.Fatal(err)
	}
	f := &prefixFiles{handle: &prefixHandle{t: t, fd: 7, sample: []byte("{}")}, full: []byte("{}")}
	service, state, _ := testService(t, f, nil)
	_, err = service.ParseFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.helperPaths) != 1 || f.helperPaths[0] != resolved || state.GetCachedParsedFile(alias) == nil || state.GetCachedParsedFile(actual) != nil || state.GetCachedParsedFile(resolved) != nil {
		t.Fatalf("helper paths/cache: %v", f.helperPaths)
	}
	f.calls = nil
	_, err = service.ParseFile(alias)
	if err != nil || len(f.calls) != 0 || len(f.helperPaths) != 1 {
		t.Fatalf("cached file must not resolve or probe again %v %v", f.calls, err)
	}
}

func TestMissingParserCachesNullButResetRunsProbeAgain(t *testing.T) {
	f := &prefixFiles{openErr: os.ErrNotExist}
	service, state, dir := testService(t, f, nil)
	path := filepath.Join(dir, "missing.json")
	for i := 0; i < 2; i++ {
		got, err := service.ParseFile(path)
		if err != nil || got.Settings != nil || len(got.Errors) != 0 {
			t.Fatalf("missing/null: %#v %v", got, err)
		}
	}
	if strings.Join(f.calls, ",") != "open" || len(f.helperPaths) != 1 {
		t.Fatalf("cache did not stop repeated probe %v", f.calls)
	}
	state.ResetSettingsCache()
	_, err := service.ParseFile(path)
	if err != nil || strings.Join(f.calls, ",") != "open,open" || len(f.helperPaths) != 2 {
		t.Fatalf("reset did not reopen %v %v", f.calls, err)
	}
}
