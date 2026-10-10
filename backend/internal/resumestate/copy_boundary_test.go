package resumestate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
)

func TestActualCopyOperationKeepsNativeFaultsAndDestinationBytes(t *testing.T) {
	home := t.TempDir()
	files := OSFiles{}
	old, next := filepath.Join(home, "original"), filepath.Join(home, "copied")
	writePrivate(t, old, "original-copy-bytes")
	if err := os.Chmod(old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := files.CopyFile(old, next); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(next); err != nil || string(data) != "original-copy-bytes" {
		t.Fatal("copy failed to persist independent bytes")
	}
	if err := files.CopyFile(filepath.Join(home, "missing"), next); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("copy missing input lost cause")
	}
	if data, err := os.ReadFile(next); err != nil || string(data) != "original-copy-bytes" {
		t.Fatal("failed copy truncated existing target")
	}
	if err := files.CopyFile(old, home); err == nil {
		t.Fatal("copy to directory succeeded")
	}
}

func TestBackgroundSettingsSnapshotAndCompletedBackupsUseRealFiles(t *testing.T) {
	for _, mode := range []string{"completed", "cleanup-zero", "skip-history", "late-invalid", "nonstring-backup", "nonstring-SID", "empty-snapshots"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			files := &heldCopies{started: make(chan struct{}), release: make(chan struct{})}
			options, events := testOptions(t, home, files)
			options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
			if mode == "skip-history" {
				options.Environment["CLAUDE_CODE_SKIP_PROMPT_HISTORY"] = "true"
			}
			if mode == "cleanup-zero" {
				writePrivate(t, filepath.Join(home, ".claude", "settings.json"), `{"cleanupPeriodDays":0}`)
			}
			writePrivate(t, filepath.Join(home, ".claude", "file-history", oldSID, "backup"), "protected")
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			input := historyInput(quote("backup"))
			if mode == "completed" {
				input.Source = []byte(stringsReplace(string(input.Source), `"trackedFileBackups"`, `"completedFileBackups"`))
				value := object(input.Source)
				snapshots, _ := array(value.Get("fileHistorySnapshots"))
				snapshot := object(snapshots[0])
				snapshot.Set("trackedFileBackups", []byte(`{}`))
				value.Set("fileHistorySnapshots", encode([]json.RawMessage{encode(snapshot)}))
				input.Source = encode(value)
			}
			if mode == "nonstring-backup" {
				input = historyInput([]byte(`3`))
			}
			if mode == "nonstring-SID" {
				input.Source = []byte(stringsReplace(string(input.Source), `"`+oldSID+`"`, `3`))
			}
			if mode == "empty-snapshots" {
				value := object(input.Source)
				value.Set("messages", []byte(`[]`))
				input.Source = encode(value)
			}
			result, err := service.Load(input)
			if err != nil || result == nil {
				t.Fatal(err)
			}
			if mode != "nonstring-backup" && mode != "nonstring-SID" && mode != "empty-snapshots" {
				<-files.started
			}
			if mode == "late-invalid" {
				bad := object([]byte(`{"model":3}`))
				options.SettingsState.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: &bad})
			}
			close(files.release)
			service.AwaitBackground()
			want := 0
			if mode == "completed" {
				want = 1
			}
			if len(service.State().PendingEntries) != want {
				t.Fatal("background startup queue ignored real settings/fault boundary")
			}
			if mode == "late-invalid" && !stringsContains(encode(events.errors), "Failed to record copy backup snapshot") {
				t.Fatal("late background error ownership lost")
			}
			data, err := os.ReadFile(filepath.Join(home, ".claude", "file-history", oldSID, "backup"))
			if err != nil || string(data) != "protected" {
				t.Fatal("restoration mutated original backup")
			}
		})
	}
}

func stringsReplace(source, old, next string) string { return strings.ReplaceAll(source, old, next) }

func TestPlansMemoizedDirectoryReportsFailureAndContinuesLocalResume(t *testing.T) {
	home := t.TempDir()
	options, events := testOptions(t, home, &heldCopies{mkdirErr: fs.ErrPermission})
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	user := object(plain("u", "q"))
	user.Set("slug", quote("plan"))
	if result, err := service.Load(original([]json.RawMessage{encode(user)})); err != nil || result == nil || len(events.errors) != 1 {
		t.Fatal("mkdir diagnostic swallowed or local resume changed", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "plans")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("failed mkdir fabricated directory")
	}
}
