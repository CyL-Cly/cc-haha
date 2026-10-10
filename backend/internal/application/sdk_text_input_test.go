package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeTextInputRetainsSourceBlocksUUIDAndPermissionMode(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	for _, p := range []string{filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	type row struct {
		Content string `json:"content"`
		UUID    string `json:"uuid"`
		Mode    string `json:"mode"`
	}
	rows := []row{}
	for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions"} {
		for _, content := range []string{`"ordinary"`, `[{"type":"text","text":"scheduled prompt"}]`, `[{"type":"text","text":"preceding"},{"type":"text","text":"last 😀\ud800","future":true}]`, `[]`} {
			rows = append(rows, row{content, "source-retained-user-uuid", mode})
		}
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	sourcePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-text-prompt.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", sourcePath)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TEXT_PROMPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source%v %.2400s", e, stderr.String())
	}
	var expected struct {
		Rows []struct {
			ShouldQuery bool
			Messages    []protocol.JSONObject
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &expected); e != nil || len(expected.Rows) != len(rows) || len(expected.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped/wrong shape%v %.1800s", e, out)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response(w, "source-text-response", "", nil, "SOURCE_TEXT_OK")
	}))
	defer provider.Close()
	for i, row := range rows {
		o := nativeSessionOptions(t, provider.URL)
		o.Arguments.PermissionMode = row.Mode
		s, e := NewSession(ctx, o)
		if e != nil {
			t.Fatal(e)
		}
		message := protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: json.RawMessage(row.Content)}, protocol.JSONField{Name: "role", Value: raw("user")})
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user")}, protocol.JSONField{Name: "uuid", Value: raw(row.UUID)}, protocol.JSONField{Name: "message", Value: raw(message)})
		if e = s.SubmitSDK(ctx, input); e != nil {
			t.Fatal(e)
		}
		e = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			history := s.process.Controller.History()
			if len(history) == 0 {
				t.Fatal("input was not admitted")
			}
			actual := history[0].Clone()
			actual.Delete("timestamp")
			want := expected.Rows[i].Messages[0]
			var a, b any
			_ = json.Unmarshal(raw(actual), &a)
			_ = json.Unmarshal(raw(want), &b)
			if !expected.Rows[i].ShouldQuery || !reflect.DeepEqual(a, b) {
				t.Errorf("case%d actual%s Source%s", i, raw(actual), raw(want))
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
}

func TestStructuredSDKTextBlocksReachActualProviderAndPersistedConversation(t *testing.T) {
	var request []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ = io.ReadAll(r.Body)
		response(w, "actual-sdk-text-result", "", nil, "ACTUAL_STRUCTURED_INPUT_OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.PermissionMode = "acceptEdits"
	var sdkRows []json.RawMessage
	o.OnSDK = func(row json.RawMessage) error {
		sdkRows = append(sdkRows, append(json.RawMessage(nil), row...))
		return nil
	}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	input := obj([]byte(`{"type":"user","uuid":"actual-structured-uuid","message":{"role":"user","content":[{"type":"text","text":"ACTUAL_STRUCTURED_PROMPT_2D7A"}]},"parent_tool_use_id":null,"session_id":""}`))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = s.SubmitSDK(ctx, input); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(request, []byte("ACTUAL_STRUCTURED_PROMPT_2D7A")) {
		t.Fatalf("actual provider input lost%s", request)
	}
	var history []protocol.JSONObject
	if e = s.process.Owner.Run(ctx, func(*cooperative.Scope) error { history = s.process.Controller.History(); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(history) < 2 || history[0].String("uuid") != "actual-structured-uuid" || history[0].String("permissionMode") != "acceptEdits" || string(obj(history[0].Get("message")).Get("content")) != `[{"type":"text","text":"ACTUAL_STRUCTURED_PROMPT_2D7A"}]` {
		t.Fatalf("shared history lost exact input%s", raw(history))
	}
	if len(sdkRows) < 2 || obj(sdkRows[len(sdkRows)-1]).String("result") != "ACTUAL_STRUCTURED_INPUT_OK" {
		t.Fatalf("original SDK output missing%s", raw(sdkRows))
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	transcript := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects")
	files, e := filepath.Glob(filepath.Join(transcript, "*", "*.jsonl"))
	if e != nil || len(files) == 0 {
		t.Fatal("actual transcript missing", e)
	}
	data, e := os.ReadFile(files[0])
	if e != nil || !bytes.Contains(data, []byte(`"permissionMode":"acceptEdits"`)) || !bytes.Contains(data, []byte("actual-structured-uuid")) {
		t.Fatalf("actual structured transcript%s %v", data, e)
	}
}
