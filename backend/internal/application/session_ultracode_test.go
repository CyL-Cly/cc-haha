package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualInteractiveEffortAndKeywordUseSameNativeMainPrintAttachmentsAndResume(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		response(w, "ultra-reply", "", nil, "ACTUAL_SHARED_REPLY")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{"Workflow"}
	o.Arguments.PermissionMode = "default"
	o.Native.Environment["DISABLE_TELEMETRY"] = "1"
	o.Native.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
	o.Native.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,xhigh_effort"
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"keep":true}}`), 0600)
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	ctx := context.Background()
	submit := func(text string, suppressed bool) {
		t.Helper()
		if e := s.Submit(ctx, tui.Input{Mode: "prompt", Text: text, SuppressWorkflowKeyword: suppressed}, nil); e != nil {
			t.Fatal(text, e)
		}
	}
	submit("/effort ultracode", false)
	if len(bodies) != 0 {
		t.Fatal("local JSX effort queried a model")
	}
	submit("First standing instruction", false)
	submit("Second standing instruction", false)
	submit("/effort high", false)
	submit("After ultracode is off", false)
	for _, row := range []struct {
		text       string
		suppressed bool
	}{{"ultracode: opted in", false}, {"ultracode: explicitly ignored", true}, {"ultracode: fresh prompt", false}, {"explain `ultracode`", false}} {
		submit(row.text, row.suppressed)
	}
	mu.Lock()
	snapshot := append([][]byte{}, bodies...)
	mu.Unlock()
	if len(snapshot) != 7 {
		t.Fatalf("actual model request count%d", len(snapshot))
	}
	lastText := func(body []byte) string {
		var request struct{ Messages []protocol.JSONObject }
		_ = json.Unmarshal(body, &request)
		if len(request.Messages) == 0 {
			return ""
		}
		return string(request.Messages[len(request.Messages)-1].Get("content"))
	}
	if !bytes.Contains(snapshot[0], []byte(`"effort":"xhigh"`)) || !strings.Contains(lastText(snapshot[0]), "Ultracode is on:") || !strings.Contains(lastText(snapshot[1]), "Ultracode is still on") || !strings.Contains(lastText(snapshot[2]), "Ultracode is off") {
		t.Fatalf("actual enter/short/exit API reminders lost %.2000s %.2000s %.2000s", snapshot[0], snapshot[1], snapshot[2])
	}
	for i, want := range map[int]bool{3: true, 4: false, 5: true, 6: false} {
		if strings.Contains(lastText(snapshot[i]), "The user included the keyword") != want {
			t.Fatalf("turn keyword%d changed actual model input%s", i, lastText(snapshot[i]))
		}
	}
	data, e := os.ReadFile(filepath.Join(config, "settings.json"))
	if e != nil || bytes.Contains(data, []byte("ultracode")) || !bytes.Contains(data, []byte(`"future"`)) || !bytes.Contains(data, []byte(`"high"`)) {
		t.Fatal("session opt-in persisted or settings were lost", e, string(data))
	}
	submit("/effort ultracode", false)
	submit("Standing instruction before close", false)
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	// Source sessionStorage.isLoggableMessage removes external attachments.
	// A resumed external session must not revive session-only consent or
	// reconstruct a reminder from a raw instruction that was not persisted.
	resume := o
	resume.Arguments.SessionID = ""
	resume.Arguments.Resume = s.process.Controller.SessionID()
	resumed, e := NewSession(ctx, resume)
	if e != nil {
		t.Fatal(e)
	}
	defer resumed.Close(ctx)
	if e = resumed.Submit(ctx, tui.Input{Mode: "prompt", Text: "Resume the same private conversation"}, nil); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	last := append([]byte(nil), bodies[len(bodies)-1]...)
	mu.Unlock()
	if strings.Contains(lastText(last), "Ultracode is on:") || strings.Contains(lastText(last), "Ultracode is still on") || strings.Contains(lastText(last), "Ultracode is off") {
		t.Fatalf("resumed external session reconstructed an unpersisted flag %.2000s", last)
	}
}
