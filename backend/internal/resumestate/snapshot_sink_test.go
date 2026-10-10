package resumestate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSnapshotSinkIsRealAndKeepsLegacyObserverBoundary(t *testing.T) {
	for _, name := range []string{"nil-legacy", "actual-sink", "failed-sink"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			files := &heldCopies{started: make(chan struct{}), release: make(chan struct{})}
			options, errorsList := testOptions(t, home, files)
			options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
			writePrivate(t, filepath.Join(home, ".claude", "file-history", oldSID, "backup"), "original")
			sinkPath := filepath.Join(home, "actual-startup-queue")
			var seen protocol.JSONObject
			if name != "nil-legacy" {
				options.OnSnapshotRecord = func(entry protocol.JSONObject) error {
					seen = entry.Clone()
					if name == "failed-sink" {
						return errors.New("native writer failed")
					}
					data, err := entry.MarshalJSON()
					if err != nil {
						return err
					}
					return os.WriteFile(sinkPath, data, 0600)
				}
			}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Load(historyInput(quote("backup")))
			if err != nil || result == nil {
				t.Fatal(err)
			}
			<-files.started
			if _, err = os.Stat(sinkPath); !os.IsNotExist(err) || len(service.State().PendingEntries) != 0 {
				t.Fatal("Load joined the real background writer")
			}
			close(files.release)
			service.AwaitBackground()
			if name == "failed-sink" {
				if len(service.State().PendingEntries) != 0 || len(errorsList.errors) == 0 {
					t.Fatal("failed writer forged persisted observer entry")
				}
				return
			}
			if len(service.State().PendingEntries) != 1 {
				t.Fatal("successful real copy lost pending observer")
			}
			if name == "actual-sink" {
				data, err := os.ReadFile(sinkPath)
				if err != nil || string(data) != string(service.State().PendingEntries[0]) {
					t.Fatal("real sink and observer differ", err)
				}
				seen.Set("snapshot", nil)
				if service.State().PendingEntries[0] == nil {
					t.Fatal("sink observer changed pending entry")
				}
			}
		})
	}
}
