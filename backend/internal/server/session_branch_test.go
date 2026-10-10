package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestDesktopSessionBranchHTTPValidatesCopiesAndIndexesCanonicalHistory(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("fork must not start model/runtime")
		return nil, nil
	})
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	path := filepath.Join(o.ConfigDir, "projects", projectpath.Sanitize(o.WorkDir), id+".jsonl")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range []map[string]any{{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": id, "timestamp": "2026-10-01T00:00:00Z", "message": map[string]any{"role": "user", "content": "Original fork prompt"}}, {"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": id, "timestamp": "2026-10-01T00:00:01Z", "message": map[string]any{"id": "api-a1", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Original answer"}}}}, {"type": "user", "uuid": "u2", "parentUuid": "a1", "sessionId": id, "timestamp": "2026-10-01T00:00:02Z", "message": map[string]any{"role": "user", "content": "Must not copy later turn"}}} {
		raw, _ := json.Marshal(row)
		data = append(data, append(raw, '\n')...)
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	s.noteSessionMutation(path)
	endpoint := "/api/sessions/" + id + "/branch"
	for _, body := range []string{`{`, `{}`, `{"targetMessageId":null}`, `{"targetMessageId":"   "}`, `{"targetMessageId":"a1","title":null}`, `{"targetMessageId":"not-active"}`} {
		requestJSON(t, host, "POST", endpoint, body, 400)
	}
	requestJSON(t, host, "GET", endpoint, "", 405)
	// Validate the body before attempting source lookup, as the original API does.
	requestJSON(t, host, "POST", "/api/sessions/60000000-0000-4000-8000-000000000009/branch", `{}`, 400)
	requestJSON(t, host, "POST", "/api/sessions/60000000-0000-4000-8000-000000000009/branch", `{"targetMessageId":"a1"}`, 404)
	result := requestJSON(t, host, "POST", endpoint, `{"targetMessageId":"  a1  ","title":"  UI fork  "}`, 201)
	var fork struct{ SessionID, Title, WorkDir, SourceSessionID, TargetMessageID string }
	if e = json.Unmarshal(result, &fork); e != nil {
		t.Fatal(e)
	}
	if fork.SessionID == "" || fork.SessionID == id || fork.Title != "UI fork (Branch)" || fork.WorkDir != o.WorkDir || fork.SourceSessionID != id || fork.TargetMessageID != "a1" {
		t.Fatal(string(result))
	}
	newPath := filepath.Join(filepath.Dir(path), fork.SessionID+".jsonl")
	copied, e := os.ReadFile(newPath)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(copied), "Must not copy later turn") || !strings.Contains(string(copied), `"forkedFrom"`) {
		t.Fatal(string(copied))
	}
	before, e := os.ReadFile(path)
	if e != nil || string(before) != string(data) {
		t.Fatal("fork mutated original", e)
	}
	requestJSON(t, host, "GET", "/api/sessions/"+fork.SessionID+"/messages?mode=full", "", 200)
	listed := requestJSON(t, host, "GET", "/api/sessions", "", 200)
	if !strings.Contains(string(listed), fork.SessionID) || !strings.Contains(string(listed), fork.Title) {
		t.Fatal("fork omitted from sidebar index", string(listed))
	}
	result = requestJSON(t, host, "POST", endpoint, `{"targetMessageId":"a1","title":"UI fork"}`, 201)
	if !strings.Contains(string(result), "UI fork (Branch 2)") {
		t.Fatal(string(result))
	}
}
