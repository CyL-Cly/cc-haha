package sessioncontroller

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Original Project.persistToRemote returns without a URL even when the env
// switch is enabled. Local persistence and metadata remain fully usable.
func TestRemoteEnabledWithoutURLAppendsLocalTranscriptAndFileHistory(t *testing.T) {
	owner, writer, pending, loader := uuidHarness(t, uuidOld, nil, false)
	writer.AdoptSessionFile()
	writer.options.Environment["ENABLE_SESSION_PERSISTENCE"] = "1"
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote("local-new")}, protocol.JSONField{Name: "future", Value: json.RawMessage(`{"opaque":true}`)})
	var appendFuture *WriteFuture
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		var err error
		appendFuture, err = pending.BeginAppend(s, loader, &entry, "")
		return err
	})
	if err := appendFuture.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		sink, err := NewFileHistorySink(pending, loader)
		if err != nil {
			return err
		}
		snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "messageId", Value: quote("local-new")}, protocol.JSONField{Name: "future", Value: json.RawMessage(`{"opaque":true}`)})
		f, err := sink.BeginFileHistorySnapshot(s, quote("local-new"), &snapshot, false)
		if err != nil {
			return err
		}
		if _, err = f.Await(s); err != nil {
			return err
		}
		flush, err := pending.Flush(s)
		if err != nil {
			return err
		}
		return flush.Await(s)
	})
	data, err := os.ReadFile(writer.target())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"uuid":"local-new"`) != 1 || !strings.Contains(string(data), `"type":"file-history-snapshot"`) || strings.Count(string(data), `"opaque":true`) != 2 {
		t.Fatalf("local transcript/snapshot was lost: %s", data)
	}
}
