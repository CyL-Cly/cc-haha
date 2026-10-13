package remoteio

import (
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceEnabledPersistenceWithoutIngressRetainsLocalTranscriptAndSnapshot(t *testing.T) {
	user := object([]byte(`{"type":"user","uuid":"local-user","timestamp":"2026-10-01T00:00:00.000Z","message":{"role":"user","content":"local only"}}`))
	want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"url": "http://127.0.0.1:1/unused", "localWithoutIngress": true, "env": map[string]string{"ENABLE_SESSION_PERSISTENCE": "1", "CLAUDE_CODE_SIMPLE": "1", "TEST_ENABLE_SESSION_PERSISTENCE": "1"}, "messages": []protocol.JSONObject{user}})
	var rows []protocol.JSONObject
	if err := json.Unmarshal(want.Get("rows"), &rows); err != nil {
		t.Fatal(err)
	}
	users, snapshots := 0, 0
	for _, row := range rows {
		if row.String("type") == "user" && row.String("uuid") == "local-user" {
			users++
		}
		if row.String("type") == "file-history-snapshot" && string(object(object(row.Get("snapshot")).Get("future")).Get("opaque")) == "true" {
			snapshots++
		}
	}
	if users != 1 || snapshots != 1 {
		t.Fatalf("actual Source local persistence contract changed: %s", want.Get("rows"))
	}
}
