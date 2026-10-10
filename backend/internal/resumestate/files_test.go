package resumestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type heldCopies struct {
	OSFiles
	started, release           chan struct{}
	once                       sync.Once
	linkErr, copyErr, mkdirErr error
}

func (files *heldCopies) Link(old, new string) error {
	if files.started != nil {
		files.once.Do(func() { close(files.started) })
		<-files.release
	}
	if files.linkErr != nil {
		return files.linkErr
	}
	return files.OSFiles.Link(old, new)
}
func (files *heldCopies) CopyFile(old, new string) error {
	if files.copyErr != nil {
		return files.copyErr
	}
	return files.OSFiles.CopyFile(old, new)
}
func (files *heldCopies) MkdirAll(path string) error {
	if files.mkdirErr != nil {
		return files.mkdirErr
	}
	return files.OSFiles.MkdirAll(path)
}
func historyInput(name json.RawMessage) Input {
	snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: quote("u")}, protocol.JSONField{Name: "trackedFileBackups", Value: encode(protocol.NewJSONObject(protocol.JSONField{Name: "file.txt", Value: encode(protocol.NewJSONObject(protocol.JSONField{Name: "backupFileName", Value: name}, protocol.JSONField{Name: "version", Value: []byte("1")}))}))})
	return original([]json.RawMessage{plain("u", "q")}, protocol.JSONField{Name: "fileHistorySnapshots", Value: encode([]json.RawMessage{encode(snapshot)})})
}
func TestRealFileCopyKeepsReturnBeforeBackgroundAndStartupQueue(t *testing.T) {
	home := t.TempDir()
	files := &heldCopies{started: make(chan struct{}), release: make(chan struct{})}
	options, _ := testOptions(t, home, files)
	options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	old := filepath.Join(home, ".claude", "file-history", oldSID, "backup")
	writePrivate(t, old, "protected-original")
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Load(historyInput(quote("backup")))
	if err != nil || result == nil {
		t.Fatal(err)
	}
	<-files.started
	if len(service.State().PendingEntries) != 0 {
		t.Fatal("Load joined or forged pending copy completion")
	}
	close(files.release)
	service.AwaitBackground()
	after := service.State()
	if len(after.PendingEntries) != 1 {
		t.Fatalf("real pending startup entry missing: %+v", after)
	}
	newPath := filepath.Join(home, ".claude", "file-history", tempSID, "backup")
	a, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(newPath)
	if err != nil || !os.SameFile(a, b) {
		t.Fatal("copied instead of actual hardlink")
	}
	if _, err := os.Stat(service.projectDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("created metadata-only transcript")
	}
	after.PendingEntries[0][0] = 'x'
	if service.State().PendingEntries[0][0] != '{' {
		t.Fatal("state observer mutated pending writer bytes")
	}
}
func TestRealFallbackCopyFaultsAndUnsafeNames(t *testing.T) {
	for _, test := range []struct {
		name              string
		link, copy, mkdir error
		backup            json.RawMessage
		wantEntries       int
		wantError         string
	}{{"cross-device-copy", syscall.EXDEV, nil, nil, quote("backup"), 1, "Error hard linking"}, {"copy-fails", syscall.EXDEV, syscall.EACCES, nil, quote("backup"), 0, "Error copying"}, {"mkdir-fails", nil, nil, syscall.EACCES, quote("backup"), 0, "permission denied"}, {"unsafe-name", nil, nil, nil, quote("../sentinel"), 0, ""}, {"null-name", nil, nil, nil, []byte("null"), 1, ""}} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			files := &heldCopies{linkErr: test.link, copyErr: test.copy, mkdirErr: test.mkdir}
			options, events := testOptions(t, home, files)
			options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
			old := filepath.Join(home, ".claude", "file-history", oldSID, "backup")
			writePrivate(t, old, "original")
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Load(historyInput(test.backup))
			if err != nil {
				t.Fatal(err)
			}
			service.AwaitBackground()
			if len(service.State().PendingEntries) != test.wantEntries {
				t.Fatalf("bad snapshot ownership: %+v", service.State())
			}
			if test.wantError != "" && !stringsContains(encode(events.errors), test.wantError) {
				t.Fatalf("fault disappeared: %s", encode(events.errors))
			}
			if test.name == "cross-device-copy" {
				a, _ := os.Stat(old)
				b, err := os.Stat(filepath.Join(home, ".claude", "file-history", tempSID, "backup"))
				if err != nil || os.SameFile(a, b) {
					t.Fatal("fallback did not perform actual copy")
				}
			}
			data, err := os.ReadFile(old)
			if err != nil || string(data) != "original" {
				t.Fatal("original backup mutated")
			}
		})
	}
}
func stringsContains(raw json.RawMessage, value string) bool {
	return strings.Contains(string(raw), value)
}
