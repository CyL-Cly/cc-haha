package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func readObject(store *Store, path string, fallback Object) (Object, error) {
	return ReadRecoverableJSON(store, ReadOptions[Object]{Path: path, Label: "settings", Default: fallback, Normalize: NormalizeObject})
}

func quietStore() *Store { return New(Options{Warn: func(string) {}}) }

func TestFileSystemAccessorPreservesInjectedBoundary(t *testing.T) {
	boundary := &faultFS{}
	if New(Options{FS: boundary}).FileSystem() != boundary {
		t.Fatal("store exposed a different persistence boundary")
	}
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path string, expected []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, expected) {
		t.Fatalf("file %s = %q, %v; want %q", path, data, err, expected)
	}
}

func TestMissingAndJSBlankFilesCloneDefaultsWithoutWriting(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte(" \t\r\n\u00a0\u1680\u2000\u2028\u2029\u3000\ufeff")} {
		t.Run(fmt.Sprintf("blank-%x", raw), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if raw != nil {
				writeFixture(t, path, raw)
			}
			fallback := Object{"nested": json.RawMessage(`{"enabled":true}`)}
			store := quietStore()
			first, err := readObject(store, path, fallback)
			if err != nil {
				t.Fatal(err)
			}
			first["nested"][2] = 'X'
			first["added"] = json.RawMessage(`true`)
			second, err := readObject(store, path, fallback)
			if err != nil || !reflect.DeepEqual(second, fallback) || len(second) != 1 {
				t.Fatalf("default was shared: %v, %v", second, err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if raw == nil && len(entries) != 0 {
				t.Fatalf("missing read wrote files: %v", entries)
			}
			if raw != nil {
				assertFile(t, path, raw)
				if len(entries) != 1 {
					t.Fatalf("blank file was quarantined: %v", entries)
				}
			}
		})
	}
}

func TestLegacyUnknownFieldsAndRawNumbersSurviveReadAndWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy, err := os.ReadFile("testdata/legacy-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, legacy)
	store := quietStore()
	value, err := readObject(store, path, Object{})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, legacy)
	if string(value["largeInteger"]) != "9007199254740993123456789" || string(value["futureDecimal"]) != "1.234567890123456789e+100" {
		t.Fatalf("unknown numbers lost precision: %v", value)
	}
	if _, exists := value["schemaVersion"]; exists {
		t.Fatal("storage added a global schema")
	}
	value["language"] = json.RawMessage(`"中文 😀 <&>"`)
	if err := store.AtomicWriteJSON(path, "settings", value, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	rewritten, err := readObject(store, path, Object{})
	if err != nil || len(rewritten) != len(value) {
		t.Fatalf("round trip changed unknowns: %v, %v", rewritten, err)
	}
	for key, expected := range value {
		var expectedCompact, actualCompact bytes.Buffer
		if err := json.Compact(&expectedCompact, expected); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&actualCompact, rewritten[key]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(expectedCompact.Bytes(), actualCompact.Bytes()) {
			t.Fatalf("field %s changed: %s vs %s", key, expectedCompact.Bytes(), actualCompact.Bytes())
		}
	}
	raw, _ := os.ReadFile(path)
	if !bytes.HasSuffix(raw, []byte("\n")) || !bytes.Contains(raw, []byte("\n  \"")) || bytes.Contains(raw, []byte(`\u003c`)) {
		t.Fatalf("unexpected JSON formatting: %s", raw)
	}
}

func TestInvalidJSONAndRejectedShapesQuarantineExactOriginalBytes(t *testing.T) {
	corrupt, err := os.ReadFile("testdata/corrupt-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := [][]byte{corrupt, []byte(`null`), []byte(`[]`), []byte(`false`), []byte(`42`), []byte(`"settings"`), []byte(`{} {}`), []byte("\u0085")}
	for _, raw := range fixtures {
		t.Run(fmt.Sprintf("raw-%q", raw), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFixture(t, path, raw)
			var warnings []string
			store := New(Options{Now: func() time.Time { return time.UnixMilli(1234) }, Random: bytes.NewReader([]byte{1, 2, 3}), Warn: func(message string) { warnings = append(warnings, message) }})
			value, err := readObject(store, path, Object{})
			if err != nil || len(value) != 0 {
				t.Fatalf("recovery = %v, %v", value, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("original path remains after quarantine: %v", err)
			}
			backup := path + ".invalid-1234-010203"
			assertFile(t, backup, raw)
			if len(warnings) != 1 || !strings.Contains(warnings[0], backup) {
				t.Fatalf("missing quarantine diagnostic: %v", warnings)
			}
		})
	}
}

type faultFS struct {
	osFileSystem
	read   func(string) ([]byte, error)
	mkdir  func(string, fs.FileMode) error
	write  func(string, []byte, fs.FileMode) error
	rename func(string, string) error
	remove func(string) error
	copy   func(string, string) error
}

func (filesystem faultFS) ReadFile(path string) ([]byte, error) {
	if filesystem.read != nil {
		return filesystem.read(path)
	}
	return filesystem.osFileSystem.ReadFile(path)
}
func (filesystem faultFS) MkdirAll(path string, mode fs.FileMode) error {
	if filesystem.mkdir != nil {
		return filesystem.mkdir(path, mode)
	}
	return filesystem.osFileSystem.MkdirAll(path, mode)
}
func (filesystem faultFS) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if filesystem.write != nil {
		return filesystem.write(path, data, mode)
	}
	return filesystem.osFileSystem.WriteFile(path, data, mode)
}
func (filesystem faultFS) Rename(old, next string) error {
	if filesystem.rename != nil {
		return filesystem.rename(old, next)
	}
	return filesystem.osFileSystem.Rename(old, next)
}
func (filesystem faultFS) Remove(path string) error {
	if filesystem.remove != nil {
		return filesystem.remove(path)
	}
	return filesystem.osFileSystem.Remove(path)
}
func (filesystem faultFS) CopyFile(old, next string) error {
	if filesystem.copy != nil {
		return filesystem.copy(old, next)
	}
	return filesystem.osFileSystem.CopyFile(old, next)
}

func TestQuarantineFailureReturnsDefaultAndDoesNotOverwriteOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := []byte(`{"unfinished":`)
	writeFixture(t, path, raw)
	var warning string
	store := New(Options{FS: faultFS{rename: func(string, string) error { return fs.ErrPermission }}, Warn: func(message string) { warning = message }})
	value, err := readObject(store, path, Object{})
	if err != nil || len(value) != 0 || !strings.Contains(warning, "failed to quarantine") {
		t.Fatalf("failed quarantine recovery = %v, %v, %q", value, err, warning)
	}
	assertFile(t, path, raw)
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatalf("failed quarantine created another file: %v", files)
	}
}

func TestReadIOAndNormalizerFailuresDoNotBecomeSuccessfulDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := New(Options{FS: faultFS{read: func(string) ([]byte, error) { return nil, fs.ErrPermission }}})
	_, err := readObject(store, path, Object{})
	var fileError *FileError
	if !errors.As(err, &fileError) || !errors.Is(err, fs.ErrPermission) || fileError.Operation != "read" || !strings.Contains(err.Error(), path) {
		t.Fatalf("read failure hidden: %v", err)
	}
	writeFixture(t, path, []byte(`{"old":true}`))
	normalizerFailure := errors.New("normalizer failed")
	_, err = ReadRecoverableJSON(quietStore(), ReadOptions[Object]{Path: path, Normalize: func(json.RawMessage) (Object, bool, error) { return nil, false, normalizerFailure }})
	if !errors.Is(err, normalizerFailure) {
		t.Fatalf("normalizer failure hidden: %v", err)
	}
	assertFile(t, path, []byte(`{"old":true}`))
}

func TestNormalizationRemainsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := []byte(`{"sidebar":{"sort":"legacy"},"future":{"keep":true}}`)
	writeFixture(t, path, raw)
	value, err := ReadRecoverableJSON(quietStore(), ReadOptions[Object]{Path: path, Normalize: func(raw json.RawMessage) (Object, bool, error) {
		object, valid, err := NormalizeObject(raw)
		object["sidebar"] = json.RawMessage(`{"sort":"updatedAt"}`)
		return object, valid, err
	}})
	if err != nil || string(value["sidebar"]) != `{"sort":"updatedAt"}` {
		t.Fatalf("normalization failed: %v, %v", value, err)
	}
	assertFile(t, path, raw)
}

func TestAtomicWriteFailuresKeepCommittedFileAndCleanOwnTemporary(t *testing.T) {
	for _, phase := range []string{"mkdir", "write", "rename"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			original := []byte(`{"committed":true}`)
			writeFixture(t, path, original)
			unrelated := path + ".tmp.other"
			writeFixture(t, unrelated, []byte("do not clean"))
			filesystem := faultFS{}
			attempts := 0
			switch phase {
			case "mkdir":
				filesystem.mkdir = func(string, fs.FileMode) error { attempts++; return fs.ErrPermission }
			case "write":
				filesystem.write = func(path string, data []byte, mode fs.FileMode) error {
					attempts++
					_ = os.WriteFile(path, data[:len(data)/2], mode)
					return fs.ErrPermission
				}
			case "rename":
				filesystem.rename = func(string, string) error { attempts++; return fs.ErrPermission }
			}
			err := New(Options{FS: filesystem}).AtomicWriteJSON(path, "settings", Object{"next": json.RawMessage(`true`)}, WriteOptions{RetryMissingParent: true})
			if !errors.Is(err, fs.ErrPermission) || attempts != 1 {
				t.Fatalf("unexpected retry/error: %d, %v", attempts, err)
			}
			assertFile(t, path, original)
			assertFile(t, unrelated, []byte("do not clean"))
			files, _ := os.ReadDir(filepath.Dir(path))
			if len(files) != 2 {
				t.Fatalf("temporary leaked: %v", files)
			}
		})
	}
}

func TestAtomicWriteRetriesOnlyExplicitMissingParentRace(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry-%v", retry), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "parent", "settings.json")
			attempts := 0
			filesystem := faultFS{rename: func(old, next string) error {
				attempts++
				if attempts == 1 {
					return &os.PathError{Op: "rename", Path: old, Err: fs.ErrNotExist}
				}
				return os.Rename(old, next)
			}}
			err := New(Options{FS: filesystem}).AtomicWriteJSON(path, "settings", Object{"written": json.RawMessage(`true`)}, WriteOptions{RetryMissingParent: retry})
			if retry && (err != nil || attempts != 2) {
				t.Fatalf("race not retried: %d, %v", attempts, err)
			}
			if !retry && (!errors.Is(err, fs.ErrNotExist) || attempts != 1) {
				t.Fatalf("default unexpectedly retried: %d, %v", attempts, err)
			}
			files, _ := os.ReadDir(filepath.Dir(path))
			for _, file := range files {
				if strings.Contains(file.Name(), ".tmp.") {
					t.Fatalf("retry temporary leaked: %s", file.Name())
				}
			}
		})
	}
}

func TestBackupCopiesOriginalBytesAndPermissionsWithoutChangingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := []byte(" {\n  \"future\": 12345678901234567890\n}\n")
	writeFixture(t, path, raw)
	backup, err := quietStore().BackupFile(path, "bak-before-migration")
	if err != nil || !regexp.MustCompile(`\.bak-before-migration-[0-9]+-[0-9a-f]{6}$`).MatchString(backup) {
		t.Fatalf("backup failed: %s, %v", backup, err)
	}
	assertFile(t, path, raw)
	assertFile(t, backup, raw)
	originalInfo, _ := os.Stat(path)
	backupInfo, _ := os.Stat(backup)
	if backupInfo.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("backup permissions changed: %v vs %v", backupInfo.Mode(), originalInfo.Mode())
	}
	if _, err := quietStore().BackupFile(path+".missing", "bak-before-migration"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("backup failure hidden: %v", err)
	}
}

func TestWriteLockSerializesReadModifyWriteAndContinuesAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := quietStore()
	sentinel := errors.New("first update rejected")
	if err := store.WithWriteLock(path, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errorsFound := make(chan error, 20)
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			errorsFound <- store.WithWriteLock(path, func() error {
				object, err := readObject(store, path, Object{})
				if err != nil {
					return err
				}
				object[fmt.Sprintf("update-%d", index)] = json.RawMessage(`true`)
				return store.AtomicWriteJSON(path, "settings", object, WriteOptions{})
			})
		}(index)
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	object, err := readObject(store, path, Object{})
	if err != nil || len(object) != 20 || len(store.locks) != 0 {
		t.Fatalf("concurrent patches lost or locks leaked: %d, %v, %d", len(object), err, len(store.locks))
	}
}

func TestUnrelatedWritePathsProceedAndPanicReleasesLock(t *testing.T) {
	store := quietStore()
	entered, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_ = store.WithWriteLock("a", func() error { close(entered); <-release; return nil })
	}()
	<-entered
	done := make(chan error, 1)
	go func() { done <- store.WithWriteLock("b", func() error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated path blocked")
	}
	close(release)
	<-firstDone
	func() {
		defer func() { _ = recover() }()
		_ = store.WithWriteLock("panic", func() error { panic("fixture") })
	}()
	if err := store.WithWriteLock("panic", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestWriteQueuePreservesAdmissionOrderAcrossFailedUpdate(t *testing.T) {
	store := quietStore()
	path := filepath.Join(t.TempDir(), "settings.json")
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 3)
	go func() {
		done <- store.WithWriteLock(path, func() error { close(entered); <-release; return errors.New("fixture failure") })
	}()
	<-entered
	order := []int{}
	for index := 1; index <= 2; index++ {
		go func(index int) {
			done <- store.WithWriteLock(path, func() error { order = append(order, index); return nil })
		}(index)
		// Hold the first task until this call has joined the queue. The assertion
		// below exercises the ordering of actual tasks after a failed predecessor.
		deadline := time.After(time.Second)
		ticker := time.NewTicker(time.Millisecond)
		for waiting := true; waiting; {
			store.mu.Lock()
			waiting = store.locks[path].refs != index+1
			store.mu.Unlock()
			if !waiting {
				break
			}
			select {
			case <-ticker.C:
			case <-deadline:
				close(release)
				t.Fatal("update did not join queue")
			}
		}
		ticker.Stop()
	}
	close(release)
	for count := 0; count < 3; count++ {
		<-done
	}
	if !reflect.DeepEqual(order, []int{1, 2}) {
		t.Fatalf("promise queue order changed after failure: %v", order)
	}
}

func TestValidationAndRandomFailuresNeverWrite(t *testing.T) {
	store := quietStore()
	if _, err := ReadRecoverableJSON[Object](nil, ReadOptions[Object]{}); err == nil {
		t.Fatal("missing store accepted")
	}
	if err := store.AtomicWriteJSON("", "settings", nil, WriteOptions{}); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := store.BackupFile("file", "../invalid"); err == nil {
		t.Fatal("non-filename suffix accepted")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := store.AtomicWriteJSON(path, "settings", func() {}, WriteOptions{}); err == nil {
		t.Fatal("non-JSON value accepted")
	}
	starved := New(Options{Random: bytes.NewReader(nil)})
	if err := starved.AtomicWriteJSON(path, "settings", Object{}, WriteOptions{}); err == nil {
		t.Fatal("random failure hidden")
	}
	writeFixture(t, path, []byte("{"))
	if _, err := readObject(starved, path, Object{}); err == nil {
		t.Fatal("quarantine random failure hidden")
	}
	assertFile(t, path, []byte("{"))
	if _, err := starved.BackupFile(path, "bak-before-migration"); err == nil {
		t.Fatal("backup random failure hidden")
	}
	_, err := ReadRecoverableJSON(quietStore(), ReadOptions[func()]{Path: path + ".missing", Default: func() {}, Normalize: func(json.RawMessage) (func(), bool, error) { return nil, true, nil }})
	if err == nil {
		t.Fatal("invalid default clone accepted")
	}
}
