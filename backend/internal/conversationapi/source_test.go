package conversationapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestLegacyConversationRESTMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	type scenario struct {
		Path, Method, Body            string
		Waiting, Running, Interrupted bool
	}
	cases := []scenario{{Path: "/api/conversations", Method: "GET"}, {Path: "/api/conversations/existing", Method: "GET"}, {Path: "/api/conversations/missing", Method: "POST", Body: `{"content":"hello"}`}, {Path: "/api/conversations/existing", Method: "POST", Body: `invalid`}, {Path: "/api/conversations/existing", Method: "POST", Body: `null`}, {Path: "/api/conversations/existing", Method: "POST", Body: `[]`}, {Path: "/api/conversations/existing", Method: "POST", Body: `{"content":0}`}, {Path: "/api/conversations/existing", Method: "POST", Body: `{"content":""}`}, {Path: "/api/conversations/existing", Method: "POST", Body: `{"content":" "}`}, {Path: "/api/conversations/existing/status", Method: "GET"}, {Path: "/api/conversations/existing/status", Method: "GET", Waiting: true}, {Path: "/api/conversations/existing/stop", Method: "POST", Running: true}, {Path: "/api/conversations/existing/status", Method: "GET", Running: true}, {Path: "/api/sessions/existing/chat", Method: "POST", Body: `{"content":"actual"}`}, {Path: "/api/sessions/existing/chat/status", Method: "GET", Interrupted: true}, {Path: "/api/conversations/existing/status", Method: "POST"}, {Path: "/api/conversations/existing/extra", Method: "GET"}, {Path: "/api/conversations/unknown/status", Method: "GET"}, {Path: "/api/conversations/unknown/stop", Method: "POST"}}
	// JSON field names are the original lowercase caller contract.
	rows := []map[string]any{}
	for _, r := range cases {
		rows = append(rows, map[string]any{"path": r.Path, "method": r.Method, "body": r.Body, "waiting": r.Waiting, "running": r.Running, "interrupted": r.Interrupted})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-conversations.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONVERSATIONS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Status int
			Body   json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(string(out))
	}
	var activity Activity
	s, e := New(Options{Exists: func(_ context.Context, id string) (bool, error) { return id == "existing", nil }, Activity: func(context.Context, string) (Activity, error) { return activity, nil }, UUID: func() (string, error) { return "11111111-1111-4111-8111-111111111111", nil }})
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range cases {
		activity = Activity{row.Waiting, row.Running, row.Interrupted}
		status, body, e := s.Handle(ctx, row.Method, strings.Split(strings.Trim(row.Path, "/"), "/"), strings.NewReader(row.Body))
		var own *Error
		if errors.As(e, &own) {
			status = own.Status
			body = obj(field("error", own.Code), field("message", own.Message))
		} else if e != nil {
			t.Fatal(e)
		}
		actual, _ := body.MarshalJSON()
		if status != source.Rows[i].Status || string(protocol.StringifyValidatedJavaScriptJSON(actual)) != string(protocol.StringifyValidatedJavaScriptJSON(source.Rows[i].Body)) {
			t.Errorf("case%d Source%d%s Go%d%s", i, source.Rows[i].Status, source.Rows[i].Body, status, actual)
		}
	}
}
