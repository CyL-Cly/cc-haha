package remoteio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceURLHydrationAdoptedSIDAndOriginalHistoryRepersistence(t *testing.T) {
	var mu sync.Mutex
	persisted := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			fmt.Fprint(w, `{"loglines":[{"type":"user","uuid":"old-v1-user","parentUuid":null,"sessionId":"10101010-1010-4010-8010-101010101010","timestamp":"2026-10-01T00:00:00.000Z","message":{"role":"user","content":"OLD_V1_REMOTE_CONTEXT"}},{"type":"assistant","uuid":"old-v1-assistant","parentUuid":"old-v1-user","sessionId":"10101010-1010-4010-8010-101010101010","timestamp":"2026-10-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"OLD_V1_ASSISTANT_CONTEXT"}]}}]}`)
			return
		}
		var entry protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&entry)
		mu.Lock()
		persisted = append(persisted, entry)
		mu.Unlock()
		w.WriteHeader(201)
		fmt.Fprint(w, `{}`)
	}))
	defer peer.Close()
	want := runSourceFixture(t, "native-remoteio-http.fixture.ts", map[string]any{"legacy": true, "resume": true, "url": peer.URL + "/ingress/private", "env": map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-v1-token", "ENABLE_SESSION_PERSISTENCE": "1", "CLAUDE_CODE_SIMPLE": "1"}})
	mu.Lock()
	defer mu.Unlock()
	if want.String("sessionId") != "70707070-7070-4070-8070-707070707070" {
		t.Fatal("actual URL identity changed", string(raw(want)))
	}
	ids := []string{}
	for _, entry := range persisted {
		ids = append(ids, entry.String("uuid"))
	}
	if len(ids) != 0 {
		t.Fatal("Source URL-adoption re-persistence contract differs", ids, string(raw(want)))
	}
}
