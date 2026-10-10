package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"golang.org/x/net/websocket"
)

func TestPublicAccessActualServerSharedSocketRevokeAndShutdown(t *testing.T) {
	actual := &transportSession{received: make(chan json.RawMessage, 8)}
	o := testOptions(t, func(_ context.Context, _ SessionOptions, emit Emit) (LiveSession, error) {
		actual.emit = emit
		return actual, nil
	})
	o.Authorization = access.Authorization{LocalToken: "fixture-desktop-token"}
	s, host := testHost(t, o)
	request := func(base, method, path, body, cookie string, local bool) *http.Response {
		r, e := http.NewRequest(method, base+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Origin", "https://fixture.invalid")
		if local {
			r.Header.Set("Authorization", "Bearer fixture-desktop-token")
		}
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		response, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	read := func(r *http.Response) map[string]json.RawMessage {
		data, e := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]json.RawMessage
		if e = json.Unmarshal(data, &v); e != nil {
			t.Fatalf("body %v %s", e, data)
		}
		return v
	}
	control := func(action, body string) map[string]json.RawMessage {
		r := request(host.URL, "POST", "/api/public-access/"+action, body, "", true)
		if r.StatusCode != 200 {
			t.Fatalf("control%d %s", r.StatusCode, read(r))
		}
		return read(r)
	}
	for _, path := range []string{"/api/public-access/status", "/api/public-access/status?token=fixture-desktop-token", "/api/public-access/status?localToken=fixture-desktop-token"} {
		r := request(host.URL, "GET", path, "", "", false)
		if r.StatusCode != 403 {
			t.Fatal(r.StatusCode)
		}
		read(r)
	}
	status := control("enable", `{"publicUrl":"https://fixture.invalid"}`)
	var port int
	_ = json.Unmarshal(status["port"], &port)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	session := request(host.URL, "POST", "/api/sessions", `{"workDir":`+string(jsonString(o.WorkDir))+`}`, "", true)
	if session.StatusCode != 201 {
		t.Fatal(read(session))
	}
	id := stringField(read(session), "sessionId")
	code := stringField(control("pairing", "{}"), "secret")
	pairBody, _ := json.Marshal(map[string]string{"secret": code, "name": "Actual paired browser"})
	pairResponse := request(base, "POST", "/api/public-access/pair", string(pairBody), "", false)
	if pairResponse.StatusCode != 200 {
		t.Fatal(read(pairResponse))
	}
	pair := read(pairResponse)
	claimBody, _ := json.Marshal(map[string]string{"id": stringField(pair, "id"), "claimSecret": stringField(pair, "claimSecret")})
	control("approve", `{"id":`+string(pair["id"])+`}`)
	claimResponse := request(base, "POST", "/api/public-access/claim", string(claimBody), "", false)
	cookie := strings.Split(claimResponse.Header.Get("Set-Cookie"), ";")[0]
	if claimResponse.StatusCode != 200 || cookie == "" {
		t.Fatal(read(claimResponse))
	}
	read(claimResponse)
	invalidUpgrade := request(base, "GET", "/ws/"+id, "", cookie, false)
	if invalidUpgrade.StatusCode != 400 || stringField(read(invalidUpgrade), "error") != "Upgrade failed" {
		t.Fatal("public upgrade failure contract changed")
	}
	config, e := websocket.NewConfig("ws"+strings.TrimPrefix(base, "http")+"/ws/"+id, "https://fixture.invalid")
	if e != nil {
		t.Fatal(e)
	}
	config.Header.Set("Cookie", cookie)
	socket, e := websocket.DialConfig(config)
	if e != nil {
		t.Fatal(e)
	}
	defer socket.Close()
	_ = socket.SetDeadline(time.Now().Add(4 * time.Second))
	receiveType(t, socket, "connected")
	receiveType(t, socket, "session_state")
	receiveType(t, socket, "permission_requests_snapshot")
	if e = websocket.Message.Send(socket, `{"type":"user_message","content":"actual-shared-ingress"}`); e != nil {
		t.Fatal(e)
	}
	select {
	case raw := <-actual.received:
		if !bytes.Contains(raw, []byte("actual-shared-ingress")) {
			t.Fatal(string(raw))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paired socket missed shared factory")
	}
	actual.emit(json.RawMessage(`{"type":"message_complete","fixture":"actual-shared-egress"}`))
	if raw := receiveType(t, socket, "message_complete"); !bytes.Contains(raw, []byte("actual-shared-egress")) {
		t.Fatal(string(raw))
	}
	control("revoke", `{"id":`+string(pair["id"])+`}`)
	var ignored string
	if e = websocket.Message.Receive(socket, &ignored); e == nil {
		t.Fatal("revoked socket still open")
	}
	r := request(base, "GET", "/api/sessions", "", cookie, false)
	if r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	read(r)
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	conn, e := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if e == nil {
		conn.Close()
		t.Fatal("public listener survived actual server Close")
	}
}
