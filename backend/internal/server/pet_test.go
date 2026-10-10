package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"golang.org/x/net/websocket"
)

func TestPetPhysicalHTTPProjectionMembershipAndIndependentCredentials(t *testing.T) {
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	o.Authorization = access.Authorization{LocalToken: "desktop-secret", PetToken: "pet-secret", ValidateH5Token: func(context.Context, string) (bool, error) { return false, nil }}
	o.AuthRequired = true
	_, host := testHost(t, o)
	call := func(method, path, body, token string, status int) json.RawMessage {
		t.Helper()
		r, _ := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Access-Control-Request-Method", "GET")
		response, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != status {
			t.Fatalf("%s %s status%d want%d body%s", method, path, response.StatusCode, status, data)
		}
		return data
	}
	ids := []string{}
	for i := 0; i < 12; i++ {
		b, _ := json.Marshal(map[string]string{"workDir": o.WorkDir})
		data := call("POST", "/api/sessions", string(b), "desktop-secret", 201)
		var row struct{ SessionID string }
		_ = json.Unmarshal(data, &row)
		ids = append(ids, row.SessionID)
	}
	data := call("GET", "/api/sessions?limit=90&offset=100&project=ignored&view=invalid", "", "pet-secret", 200)
	var result struct {
		Sessions []map[string]any
		Total    int
		Index    any
	}
	_ = json.Unmarshal(data, &result)
	if len(result.Sessions) != 9 || result.Total != 9 || result.Index != nil {
		t.Fatalf("projection %s", data)
	}
	for _, row := range result.Sessions {
		if len(row) != 8 || row["projectPath"] != "" || row["workDir"] != nil || row["workDirExists"] != false {
			t.Fatalf("leaked%v", row)
		}
	}
	data = call("GET", "/api/sessions?limit=2", "", "pet-secret", 200)
	_ = json.Unmarshal(data, &result)
	if len(result.Sessions) != 2 || result.Total != 2 {
		t.Fatal(string(data))
	}
	data = call("GET", "/api/sessions?limit=9", "", "pet-secret", 200)
	_ = json.Unmarshal(data, &result)
	member := result.Sessions[0]["id"].(string)
	nonMember := ""
	for _, id := range ids {
		found := false
		for _, row := range result.Sessions {
			found = found || row["id"] == id
		}
		if !found {
			nonMember = id
			break
		}
	}
	call("GET", "/api/sessions/"+member+"/chat/status", "", "pet-secret", 200)
	denied := call("GET", "/api/sessions/"+nonMember+"/chat/status", "", "pet-secret", 403)
	if !bytes.Contains(denied, []byte("cannot access this session")) {
		t.Fatal(string(denied))
	}
	for _, path := range []string{"/api/providers", "/api/sessions/" + member + "/messages", "/api/sessions/" + member + "/chat", "/api/sessions/%73id/chat/status"} {
		call("GET", path, "", "pet-secret", 403)
	}
	call("POST", "/api/sessions", `{}`, "pet-secret", 403)
	call("GET", "/api/sessions?token=pet-secret", "", "", 401)
	call("GET", "/api/sessions?limit=-1", "", "pet-secret", 400)
	call("GET", "/api/sessions?offset=-1", "", "pet-secret", 400)
	call("OPTIONS", "/api/sessions", "", "pet-secret", 204)
}

func TestPetPhysicalWebSocketFiltersAndSwitchesWithoutStoppingFullClient(t *testing.T) {
	fake := &transportSession{received: make(chan json.RawMessage, 8)}
	o := testOptions(t, func(_ context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
		fake.emit = emit
		return fake, nil
	})
	s, host := testHost(t, o)
	id := createTestSession(t, host, o.WorkDir)
	// All subsequent network ingress uses independent real registered credentials.
	s.options.Authorization = access.Authorization{LocalToken: "desktop-secret", PetToken: "pet-secret", ValidateH5Token: func(context.Context, string) (bool, error) { return false, nil }}
	s.options.AuthRequired = true
	connect := func(token string) *websocket.Conn {
		t.Helper()
		c, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id+"?token="+token, "", host.URL)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = c.Close() })
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		receiveType(t, c, "connected")
		receiveType(t, c, "session_state")
		return c
	}
	full := connect("desktop-secret")
	pet := connect("pet-secret")
	for _, message := range []string{`{"type":"user_message","content":"/bad"}`, `{"type":"permission_response"}`, `{"type":"user_message","content":"hi","attachments":[]}`} {
		_ = websocket.Message.Send(pet, message)
		data := receiveType(t, pet, "error")
		if !bytes.Contains(data, []byte("PET_CAPABILITY_DENIED")) || bytes.Contains(data, []byte("/bad")) {
			t.Fatal(string(data))
		}
	}
	_ = websocket.Message.Send(pet, `{"type":"user_message","content":"regular followup"}`)
	select {
	case input := <-fake.received:
		if !bytes.Contains(input, []byte("regular followup")) {
			t.Fatal(string(input))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual shared transport did not receive user")
	}
	s.broadcast(id, []byte(`{"type":"content_delta","text":"private transcript"}`))
	s.broadcast(id, []byte(`{"type":"permission_requests_snapshot","toolRequestIds":["secret"],"computerUseRequestIds":["secret"],"turnActive":true}`))
	s.broadcast(id, []byte(`{"type":"message_complete","usage":{"input_tokens":999,"output_tokens":777},"text":"private transcript"}`))
	data := receiveType(t, pet, "permission_requests_snapshot")
	if bytes.Contains(data, []byte("secret")) {
		t.Fatal(string(data))
	}
	data = receiveType(t, pet, "message_complete")
	if bytes.Contains(data, []byte("private")) || bytes.Contains(data, []byte("999")) {
		t.Fatal(string(data))
	}
	data = receiveType(t, full, "content_delta")
	if !bytes.Contains(data, []byte("private transcript")) {
		t.Fatal("full client was projected", string(data))
	}
	replacement := connect("pet-secret")
	_ = websocket.Message.Send(replacement, `{"type":"ping"}`)
	receiveType(t, replacement, "pong")
	var closed string
	if e := websocket.Message.Receive(pet, &closed); e == nil {
		t.Fatalf("previous pet still open %s", closed)
	}
	_ = websocket.Message.Send(full, `{"type":"ping"}`)
	receiveType(t, full, "pong")
	_ = websocket.Message.Send(replacement, `{"type":"stop_generation"}`)
	select {
	case raw := <-fake.received:
		if !bytes.Contains(raw, []byte("stop_generation")) {
			t.Fatal(string(raw))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pet stop was not delivered")
	}
}
