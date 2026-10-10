package teamreader

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestArchiveConcurrentWritesKeepHighestRevisionAndWholeAtomicJSON(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	var wait sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			snapshot := protocol.NewJSONObject(field("version", i), field("generatedAt", time.UnixMilli(2000+int64(i)).UTC().Format("2006-01-02T15:04:05.000Z")), field("taskListRevision", i), field("team", protocol.NewJSONObject(field("name", "team"), field("leadSessionId", "parent"), field("createdAt", 1000), field("incarnationId", "actual-id"), field("members", []protocol.JSONObject{}), field("memberCount", 0), field("activeMemberCount", 0))), field("tasks", []protocol.JSONObject{}), field("messages", []protocol.JSONObject{}))
			snapshot.Set("version", raw(time.UnixMilli(int64(i)).String()))
			failures <- reader.ArchiveSnapshot(context.Background(), snapshot)
		}(i)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	doc := reader.ReadArchive("parent")
	if doc == nil {
		t.Fatal("archive JSON not readable")
	}
	entry := reader.SelectArchiveEntry(doc, protocol.NewJSONObject())
	if entry == nil || string(lastSnapshot(*entry).Get("taskListRevision")) != "11" {
		t.Fatal("latest revision lost", entry)
	}
	files, err := os.ReadDir(filepath.Dir(reader.ArchivePath("parent")))
	if err != nil || len(files) != 1 {
		t.Fatal("atomic temporary file leaked", files, err)
	}
}
