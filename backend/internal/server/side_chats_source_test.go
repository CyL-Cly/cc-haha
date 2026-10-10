package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSideChatRESTRegistryAndBoundaryMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	parent, cliID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	sideID := "side-" + cliID
	content := `{"type":"session-meta","workDir":"$HOME","permissionMode":"default","runtimeProviderId":null,"runtimeModelId":"fixture-model","effortLevel":"low","repository":{"repoRoot":"$HOME","branch":"main","worktree":true},"future":{"keep":true}}` + "\n" + `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"Parent reference only"}}` + "\n" + `{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + parent + `","timestamp":"2026-10-01T00:00:01Z","message":{"id":"api-a1","role":"assistant","content":[{"type":"text","text":"Stable boundary"}]}}` + "\n"
	ops := []map[string]any{{"body": `{`}, {"body": `null`}, {"body": `{"unknown":1}`}, {"body": `{"sideChatId":"bad"}`}, {"body": `{"sideChatId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`}, {"method": "GET"}, {"body": `{"sideChatId":"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"}`}, {"body": `{"sideChatId":"` + cliID + `"}`}, {"body": `{"sideChatId":"` + cliID + `"}`, "append": `{"type":"user","uuid":"later","parentUuid":"a1","timestamp":"2026-10-01T00:00:02Z","message":{"role":"user","content":"Parent changed after side captured"}}` + "\n"}, {"body": `{"sideChatId":"` + cliID + `"}`, "parentId": "33333333-3333-4333-8333-333333333333"}, {"body": `{}`, "parentId": sideID}, {"method": "GET", "parentId": sideID, "resource": "summary"}, {"method": "GET", "parentId": sideID, "resource": "messages"}, {"method": "GET", "parentId": sideID, "resource": "history-recovery"}, {"method": "GET", "parentId": sideID, "resource": "turn-checkpoints"}, {"method": "GET", "parentId": sideID, "resource": ""}, {"method": "DELETE", "childId": sideID}, {"body": `{"sideChatId":"` + cliID + `"}`}, {"method": "DELETE", "childId": sideID}, {"method": "DELETE", "childId": "side-44444444-4444-4444-8444-444444444444"}}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-side-chats.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_CHATS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	now := "2026-10-07T00:00:00.000Z"
	payload, _ := json.Marshal(map[string]any{"home": home, "parentId": parent, "sideId": sideID, "content": content, "now": now, "operations": ops})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Results      []json.RawMessage
		Boundary     string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	if source.Boundary != sideChatBoundary || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source boundary/capabilities", source.Capabilities.Blocked)
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("REST creation must not start a model")
		return nil, nil
	})
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	project := filepath.Join(o.ConfigDir, "projects", "private")
	_ = os.MkdirAll(project, 0700)
	path := filepath.Join(project, parent+".jsonl")
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(content, "$HOME", o.HomeDir)), 0600)
	for i, op := range ops {
		if data, ok := op["append"].(string); ok {
			f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = f.WriteString(data)
			_ = f.Close()
			s.noteSessionMutation(path)
		}
		parentID := parent
		if value, ok := op["parentId"].(string); ok {
			parentID = value
		}
		method := "POST"
		if value, ok := op["method"].(string); ok {
			method = value
		}
		parts := []string{"api", "sessions", parentID}
		resource := "side-chats"
		if value, ok := op["resource"].(string); ok {
			resource = value
		}
		if resource != "" {
			parts = append(parts, resource)
		}
		if child, ok := op["childId"].(string); ok {
			parts = append(parts, child)
		}
		body, _ := op["body"].(string)
		request := httptest.NewRequest(method, "http://127.0.0.1/"+strings.Join(parts, "/"), strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:43210"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request)
		var response any
		if w.Body.Len() > 0 {
			_ = json.Unmarshal(w.Body.Bytes(), &response)
		}
		// The Source fixture freezes only its Date constructor. Match that clock
		// observation on the live entry; do not alter ordering or API payloads.
		if summary, ok := response.(map[string]any); ok && summary["id"] == sideID {
			summary["createdAt"] = now
			summary["modifiedAt"] = now
		}
		var entry any
		if side := s.sideChats[sideID]; side != nil {
			info := side.info
			info.CreatedAt = now
			info.ModifiedAt = now
			meta := historyRecord(info.Metadata)
			entry = map[string]any{"parentSessionId": side.parentID, "cliSessionId": side.launch.CLISessionID, "resumeAt": side.launch.ResumeAt, "resumePath": side.launch.ResumePath, "started": side.started, "closed": side.closed, "summary": info, "repositoryCleared": meta["repository"] == nil, "worktreeCleared": meta["worktreeSession"] == nil}
		}
		data, _ := json.Marshal(map[string]any{"status": w.Code, "body": response, "entry": entry})
		var actual, want any
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), o.HomeDir, "$HOME")), &actual)
		_ = json.Unmarshal(source.Results[i], &want)
		if !reflect.DeepEqual(want, actual) {
			expected, _ := json.Marshal(want)
			t.Errorf("row%d Source%s Go%s", i, expected, data)
		}
	}
}
