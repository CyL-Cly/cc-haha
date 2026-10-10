package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type observedFS struct {
	OSFileSystem
	operations []string
	fail       string
	remaining  int
}

func (files *observedFS) before(name string) error {
	files.operations = append(files.operations, name)
	if name == files.fail && files.remaining > 0 {
		files.remaining--
		return errors.New("injected " + name)
	}
	return nil
}
func (f *observedFS) MkdirAll(p string, m fs.FileMode) error {
	if e := f.before("mkdir"); e != nil {
		return e
	}
	return f.OSFileSystem.MkdirAll(p, m)
}
func (f *observedFS) Lstat(p string) (fs.FileInfo, error) {
	if e := f.before("lstat"); e != nil {
		return nil, e
	}
	return f.OSFileSystem.Lstat(p)
}
func (f *observedFS) Stat(p string) (fs.FileInfo, error) {
	if e := f.before("stat"); e != nil {
		return nil, e
	}
	return f.OSFileSystem.Stat(p)
}
func (f *observedFS) Exists(p string) bool { _ = f.before("exists"); return f.OSFileSystem.Exists(p) }
func (f *observedFS) Chmod(p string, m fs.FileMode) error {
	if e := f.before("chmod"); e != nil {
		return e
	}
	return f.OSFileSystem.Chmod(p, m)
}
func (f *observedFS) Append(p string, b []byte, m fs.FileMode) error {
	if e := f.before("append"); e != nil {
		return e
	}
	return f.OSFileSystem.Append(p, b, m)
}
func (f *observedFS) Rename(p, q string) error {
	if e := f.before("rename"); e != nil {
		return e
	}
	return f.OSFileSystem.Rename(p, q)
}
func (f *observedFS) ReadDir(p string) ([]string, error) {
	if e := f.before("readDir"); e != nil {
		return nil, e
	}
	return f.OSFileSystem.ReadDir(p)
}
func (f *observedFS) Remove(p string) error {
	if e := f.before("remove"); e != nil {
		return e
	}
	return f.OSFileSystem.Remove(p)
}

func harness(t *testing.T, files FileSystem) (*Process, *string, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "nested", "events")
	process, err := NewProcess(Options{Environment: func(string) string { return base },
		Now: func() time.Time { return time.UnixMilli(1700000000123) }, NowMillis: func() int64 { return 123 },
		PID: 42, Platform: "darwin", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return process, &base, base + ".42.current.jsonl"
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, p string) []byte { t.Helper(); b, e := os.ReadFile(p); must(t, e); return b }

func TestDisabledObservesDynamicEnvironmentBeforeClockAndIO(t *testing.T) {
	files := &observedFS{}
	process, base, active := harness(t, files)
	enabled := *base
	*base = ""
	process.options.Now = func() time.Time { t.Fatal("disabled date read"); return time.Time{} }
	process.options.NowMillis = func() int64 { t.Fatal("disabled rotation clock read"); return 0 }
	must(t, process.Log("info", "disabled", nil))
	if len(files.operations) != 0 {
		t.Fatal(files.operations)
	}
	*base = enabled
	process.options.Now = func() time.Time { return time.UnixMilli(1700000000123) }
	must(t, process.Log("info", "enabled", nil))
	if !bytes.Contains(read(t, active), []byte(`"data":{}`)) {
		t.Fatal("missing empty data")
	}
}

func TestOrderedJavaScriptJSONAndPrivateModes(t *testing.T) {
	files := &observedFS{}
	process, _, active := harness(t, files)
	must(t, os.MkdirAll(filepath.Dir(active), 0755))
	must(t, os.Chmod(filepath.Dir(active), 0755))
	must(t, os.WriteFile(active, []byte("existing\n"), 0644))
	must(t, os.Chmod(active, 0644))
	var data protocol.JSONObject
	must(t, data.UnmarshalJSON([]byte(`{"z":1e+0,"2":2,"a":"中文😀<>&\ud800","missing":null}`)))
	data.Set("undefined", nil)
	must(t, process.Log("warn", "a\nb", &data))
	want := "existing\n" + `{"timestamp":"2023-11-14T22:13:20.123Z","level":"warn","event":"a\nb","data":{"2":2,"z":1,"a":"中文😀<>&\ud800","missing":null}}` + "\n"
	if string(read(t, active)) != want {
		t.Fatalf("got %q want %q", read(t, active), want)
	}
	for path, mode := range map[string]fs.FileMode{filepath.Dir(active): 0700, active: 0600} {
		info, e := os.Stat(path)
		must(t, e)
		if info.Mode().Perm() != mode {
			t.Fatal(path, info.Mode())
		}
	}
}

func TestRotationExactBoundaryAndUTF8ByteCount(t *testing.T) {
	process, _, active := harness(t, OSFileSystem{})
	must(t, process.Log("info", "中文", nil))
	line := read(t, active)
	must(t, os.WriteFile(active, bytes.Repeat([]byte("x"), maxSegmentBytes-len(line)), 0600))
	must(t, process.Log("info", "中文", nil))
	if len(read(t, active)) != maxSegmentBytes {
		t.Fatal("rotated at inclusive byte boundary")
	}
	must(t, process.Log("info", "中文", nil))
	if !bytes.Equal(read(t, active), line) {
		t.Fatal("active segment was not recreated")
	}
	if len(read(t, strings.TrimSuffix(active, "current.jsonl")+"123-1.jsonl")) != maxSegmentBytes {
		t.Fatal("wrong rotated size")
	}
}

func TestOwnedLexicalRetentionAndProcessSequence(t *testing.T) {
	process, base, active := harness(t, OSFileSystem{})
	must(t, os.MkdirAll(filepath.Dir(active), 0700))
	for _, suffix := range []string{"10-1", "2-1", "3-1", "4-1", "5-1", "6-1"} {
		must(t, os.WriteFile(*base+".42."+suffix+".jsonl", []byte(suffix), 0600))
	}
	foreign := *base + ".999.other.jsonl"
	must(t, os.WriteFile(foreign, []byte("foreign"), 0600))
	must(t, os.WriteFile(active, bytes.Repeat([]byte("x"), maxSegmentBytes), 0600))
	must(t, process.Log("info", "rotate", nil))
	for _, suffix := range []string{"3-1", "4-1", "5-1", "6-1"} {
		if !process.options.Files.Exists(*base + ".42." + suffix + ".jsonl") {
			t.Fatal("lexical retention", suffix)
		}
	}
	if process.options.Files.Exists(*base + ".42.123-1.jsonl") {
		t.Fatal("new segment must also participate in lexical pruning")
	}
	if process.options.Files.Exists(*base + ".42.2-1.jsonl") {
		t.Fatal("more than four owned completed segments retained")
	}
	if string(read(t, foreign)) != "foreign" {
		t.Fatal("foreign segment touched")
	}
	*base = filepath.Join(t.TempDir(), "other")
	secondActive := *base + ".42.current.jsonl"
	must(t, os.WriteFile(secondActive, bytes.Repeat([]byte("x"), maxSegmentBytes), 0600))
	must(t, process.Log("info", "other", nil))
	if !process.options.Files.Exists(*base + ".42.123-2.jsonl") {
		t.Fatal("sequence reset when base changed")
	}
}

func TestRetentionUsesJavaScriptUTF16StringOrder(t *testing.T) {
	process, base, active := harness(t, OSFileSystem{})
	must(t, os.MkdirAll(filepath.Dir(active), 0700))
	for _, suffix := range []string{"\ue000", "\ue001", "\ue002", "\ue003", "😀"} {
		must(t, os.WriteFile(*base+".42."+suffix+".jsonl", []byte("keep"), 0600))
	}
	must(t, os.WriteFile(active, bytes.Repeat([]byte("x"), maxSegmentBytes), 0600))
	must(t, process.Log("info", "utf16-retention", nil))
	if !process.options.Files.Exists(*base+".42.\ue000.jsonl") || process.options.Files.Exists(*base+".42.😀.jsonl") {
		t.Fatal("rotation used UTF-8 byte order instead of the original JavaScript UTF-16 order")
	}
}

func TestStorageFailuresRetryEntireOperationOnceAndRemainSilent(t *testing.T) {
	for _, op := range []string{"mkdir", "lstat", "chmod", "stat", "append", "rename", "readDir", "remove"} {
		for _, count := range []int{1, 2} {
			t.Run(op+string(rune('0'+count)), func(t *testing.T) {
				files := &observedFS{fail: op, remaining: count}
				process, base, active := harness(t, files)
				must(t, os.MkdirAll(filepath.Dir(active), 0700))
				must(t, os.WriteFile(active, bytes.Repeat([]byte("x"), maxSegmentBytes), 0600))
				if op == "remove" {
					for _, n := range []string{"1", "2", "3", "4", "5", "6"} {
						must(t, os.WriteFile(*base+".42."+n+".jsonl", []byte("completed"), 0600))
					}
				}
				must(t, process.Log("error", "storage_error", nil))
				if len(files.operations) == 0 || files.operations[0] != "mkdir" {
					t.Fatal(files.operations)
				}
				attempts := 0
				for _, v := range files.operations {
					if v == "mkdir" {
						attempts++
					}
				}
				if attempts != 2 {
					t.Fatalf("entire storage operation must retry exactly once, got %d: %v", attempts, files.operations)
				}
				if op == "rename" && count == 1 && !files.Exists(*base+".42.123-2.jsonl") {
					t.Fatal("failed rename did not consume process sequence")
				}
				if op == "readDir" || op == "remove" {
					if !bytes.Contains(read(t, active), []byte("storage_error")) {
						t.Fatal("retry must continue after already completed rename")
					}
				}
			})
		}
	}
}

func TestFileKindsRefusedAndBrokenSymlinkPreservesExistsSemantics(t *testing.T) {
	for _, kind := range []string{"directory-symlink", "active-symlink", "active-directory", "broken-symlink", "non-directory-parent"} {
		t.Run(kind, func(t *testing.T) {
			files := &observedFS{}
			process, _, active := harness(t, files)
			target := filepath.Join(t.TempDir(), "target")
			switch kind {
			case "directory-symlink":
				must(t, os.MkdirAll(filepath.Dir(filepath.Dir(active)), 0700))
				must(t, os.Mkdir(target, 0700))
				must(t, os.Symlink(target, filepath.Dir(active)))
			case "non-directory-parent":
				must(t, os.MkdirAll(filepath.Dir(filepath.Dir(active)), 0700))
				must(t, os.WriteFile(filepath.Dir(active), []byte("file"), 0600))
			default:
				must(t, os.MkdirAll(filepath.Dir(active), 0700))
				if kind == "active-directory" {
					must(t, os.Mkdir(active, 0700))
				} else {
					if kind == "active-symlink" {
						must(t, os.WriteFile(target, []byte("target"), 0600))
					}
					must(t, os.Symlink(target, active))
				}
			}
			must(t, process.Log("info", "kind", nil))
			appendCount := 0
			for _, op := range files.operations {
				if op == "append" {
					appendCount++
				}
			}
			if kind == "broken-symlink" {
				_, targetError := os.Stat(target)
				if appendCount != 2 || !errors.Is(targetError, fs.ErrNotExist) {
					t.Fatal("Source exclusive create/O_NOFOLLOW must refuse dangling leaf on both attempts")
				}
			} else if appendCount != 0 {
				t.Fatal("refused file reached append")
			}
		})
	}
}

func TestSerializationAndInvalidDateErrorsRemainOutsideStorageCatch(t *testing.T) {
	files := &observedFS{}
	process, _, _ := harness(t, files)
	data := protocol.NewJSONObject(protocol.JSONField{Name: "invalid", Value: json.RawMessage("undefined")})
	if process.Log("info", "bad", &data) == nil || len(files.operations) != 0 {
		t.Fatal("serialization was swallowed or reached storage")
	}
	process.options.Now = func() time.Time { return time.UnixMilli(8640000000000001) }
	if process.Log("info", "bad", nil) == nil || len(files.operations) != 0 {
		t.Fatal("invalid date was swallowed or reached storage")
	}
}

func TestExplicitConstructionAndUTCExtendedYear(t *testing.T) {
	if _, err := NewProcess(Options{}); err == nil {
		t.Fatal("missing process capabilities accepted")
	}
	for _, row := range []struct {
		value time.Time
		want  string
	}{
		{time.Date(0, 1, 2, 3, 4, 5, 123000000, time.UTC), "0000-01-02T03:04:05.123Z"},
		{time.Date(-1, 1, 2, 3, 4, 5, 123000000, time.UTC), "-000001-01-02T03:04:05.123Z"},
		{time.Date(10000, 1, 2, 3, 4, 5, 123000000, time.UTC), "+010000-01-02T03:04:05.123Z"},
		{time.Date(2026, 1, 2, 11, 4, 5, 123456789, time.FixedZone("test", 8*3600)), "2026-01-02T03:04:05.123Z"},
	} {
		got, e := isoDate(row.value)
		must(t, e)
		if got != row.want {
			t.Fatal(got, row.want)
		}
	}
	files := &observedFS{}
	process, _, active := harness(t, files)
	process.options.Platform = "win32"
	must(t, process.Log("info", "windows-path-policy", nil))
	must(t, process.Log("info", "windows-existing-file", nil))
	for _, op := range files.operations {
		if op == "chmod" {
			t.Fatal("Source Windows policy performs no chmod")
		}
	}
	if !bytes.Contains(read(t, active), []byte("windows-existing-file")) {
		t.Fatal("missing second line")
	}
	// This checks the branch with native POSIX FS only, not Windows native evidence.
}

func TestSharedProcessConcurrentWritesKeepWholeJSONLines(t *testing.T) {
	process, _, active := harness(t, OSFileSystem{})
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := process.Log("info", "shared", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	lines := bytes.Split(bytes.TrimSpace(read(t, active)), []byte{'\n'})
	if len(lines) != 16 {
		t.Fatal(len(lines))
	}
	for _, line := range lines {
		if !json.Valid(line) {
			t.Fatal(string(line))
		}
	}
}

func TestNativeFileSystemErrorsAreReturnedToWriterRetry(t *testing.T) {
	files := OSFileSystem{}
	path := filepath.Join(t.TempDir(), "missing")
	if _, err := files.ReadDir(path); err == nil {
		t.Fatal("missing directory accepted")
	}
	if err := files.Append(filepath.Join(path, "child"), []byte("x"), 0600); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestNativeAppendUsesExclusiveCreateThenPrivateNoFollowDescriptor(t *testing.T) {
	files := OSFileSystem{}
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	must(t, os.WriteFile(regular, []byte("before\n"), 0644))
	must(t, os.Chmod(regular, 0644))
	must(t, files.Append(regular, []byte("after\n"), 0600))
	if string(read(t, regular)) != "before\nafter\n" {
		t.Fatal("append replaced existing bytes")
	}
	info, err := os.Stat(regular)
	must(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatal("existing descriptor permissions not tightened")
	}
	link := filepath.Join(directory, "leaf-link")
	must(t, os.Symlink(regular, link))
	if files.Append(link, []byte("must-not-append\n"), 0600) == nil {
		t.Fatal("native append followed existing leaf symbolic link")
	}
	if string(read(t, regular)) != "before\nafter\n" {
		t.Fatal("native append changed symbolic-link target")
	}
	// Native POSIX receipt only. A Windows cross-build is separate evidence.
}
