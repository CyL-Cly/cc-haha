package sessionrewind

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestTurnCheckpointStartedAtUsesServerTranscriptTimestamp(t *testing.T) {
	for _, stamp := range []string{"2026-10-12T01:02:03.456Z", "invalid"} {
		user := fixtureMessage("u1", "user", t.TempDir(), "task", "")
		user.Set("timestamp", quoted(stamp))
		assistant := fixtureMessage("a1", "assistant", user.String("cwd"), "finished", "")
		service := fixtureService(t, rewindFixture{Home: t.TempDir(), Messages: []protocol.JSONObject{user, assistant}, Complete: true})
		rows, e := service.List(context.Background(), "session", false)
		if e != nil || len(rows) != 1 {
			t.Fatal(rows, e)
		}
		if stamp == "invalid" && rows[0].StartedAt != nil {
			t.Fatal("invalid timestamp exported", rows)
		}
		if stamp != "invalid" && (rows[0].StartedAt == nil || *rows[0].StartedAt != 1791766923456) {
			t.Fatal(rows)
		}
	}
}
