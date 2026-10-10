package taskstore

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTeamLifecycleTransactionUsesActualTaskOperationsAndRejectsForeignStaleHandles(t *testing.T) {
	s, e := New(Options{ConfigDir: t.TempDir(), PID: os.Getpid(), Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	var retained *Service
	var id string
	e = s.WithTeamLifecycleTransaction("team", func(locked *Service) error {
		retained = locked
		created, e := locked.Create("team", protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: quote("actual team task")}, protocol.JSONField{Name: "description", Value: quote("physical persistence")}, protocol.JSONField{Name: "status", Value: quote("pending")}, protocol.JSONField{Name: "blocks", Value: []byte(`[]`)}, protocol.JSONField{Name: "blockedBy", Value: []byte(`[]`)}))
		if e != nil {
			return e
		}
		id = created.TaskID
		_, e = locked.Update("team", quote(id), protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quote("in_progress")}, protocol.JSONField{Name: "owner", Value: quote("worker")}))
		if e != nil {
			return e
		}
		if _, e = locked.Create("foreign", protocol.NewJSONObject()); e == nil || !strings.Contains(e.Error(), "Foreign or stale") {
			t.Fatal("foreign list accepted", e)
		}
		return locked.WithTeamLifecycleTransaction("team", func(nested *Service) error {
			snapshot, e := nested.Snapshot("team")
			if e != nil {
				return e
			}
			if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].String("owner") != "worker" {
				t.Fatal(snapshot)
			}
			return nil
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = retained.Delete("team", quote(id)); e == nil || !strings.Contains(e.Error(), "Foreign or stale") {
		t.Fatal("retained transaction wrote after lease release", e)
	}
	task, e := s.Get("team", quote(id))
	if e != nil || task == nil || task.String("status") != "in_progress" {
		t.Fatal(task, e)
	}
}
