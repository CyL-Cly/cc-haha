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

func TestSessionDeleteHTTPAndPhysicalMappingMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun unavailable")
	}
	a, b, missing := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "ffffffff-1111-2222-3333-ffffffffffff", "99999999-1111-2222-3333-999999999999"
	seedIds := []string{a, b}
	mapping := `{"2":{"sessionId":"` + a + `","workDir":"fixture","updatedAt":1,"future":{"opaque":true}},"first":{"sessionId":"` + a + `","workDir":"fixture","updatedAt":2},"second":{"sessionId":"` + b + `","workDir":"fixture","updatedAt":3},"future":{"sessionId":"keep","workDir":"untouched","updatedAt":9007199254740991,"unknown":{"nested":[1,2]}},"opaque😀":{"opaque":true}}` + "\n"
	cases := []map[string]any{
		{"body": `{"sessionIds":["  ` + a + `  ","` + b + `","` + a + `"]}`},
		{"body": `{"sessionIds":["` + a + `","` + missing + `","` + b + `"]}`},
		{"single": a, "method": "DELETE"},
		{"readOnly": true, "body": `{"sessionIds":["` + a + `","` + b + `"]}`},
		{"method": "GET"},
		{"body": `{`}, {"body": `{}`}, {"body": `[]`}, {"body": `null`}, {"body": `{"sessionIds":null}`}, {"body": `{"sessionIds":"x"}`}, {"body": `{"sessionIds":[]}`}, {"body": `{"sessionIds":[" "]}`}, {"body": `{"sessionIds":["` + a + `",null]}`},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-delete.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_DELETE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "TZ=UTC", "NODE_ENV=test", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"home": home, "seedIds": seedIds, "mapping": mapping, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Results      []any
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal(source.Capabilities.Blocked)
	}
	for i, row := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
				t.Fatal("delete must not contact a model")
				return nil, nil
			})
			s, e := New(o)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = s.Close(context.Background()) })
			project := filepath.Join(o.ConfigDir, "projects", "private")
			_ = os.MkdirAll(project, 0700)
			for _, id := range seedIds {
				_ = os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(`{"type":"session-meta","workDir":"fixture"}`+"\n"), 0600)
			}
			_ = os.WriteFile(filepath.Join(o.ConfigDir, "adapter-sessions.json"), []byte(mapping), 0600)
			if row["readOnly"] == true {
				if e := os.Chmod(project, 0555); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = os.Chmod(project, 0700) })
			}
			method := "POST"
			if v, ok := row["method"].(string); ok {
				method = v
			}
			endpoint := "batch-delete"
			if v, ok := row["single"].(string); ok {
				endpoint = v
			}
			body, _ := row["body"].(string)
			request := httptest.NewRequest(method, "http://127.0.0.1/api/sessions/"+endpoint, bytes.NewBufferString(body))
			request.RemoteAddr = "127.0.0.1:12345"
			w := httptest.NewRecorder()
			s.ServeHTTP(w, request)
			var response any
			if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
				t.Fatal(e, w.Body.String())
			}
			exists := []bool{}
			deleted := []bool{}
			for _, id := range seedIds {
				_, e := os.Stat(filepath.Join(project, id+".jsonl"))
				exists = append(exists, e == nil)
				s.mu.Lock()
				deleted = append(deleted, s.deleting[id])
				s.mu.Unlock()
			}
			maps, e := os.ReadFile(filepath.Join(o.ConfigDir, "adapter-sessions.json"))
			if e != nil {
				t.Fatal(e)
			}
			data, _ := json.Marshal(map[string]any{"status": w.Code, "response": response, "exists": exists, "mapping": string(maps), "deleted": deleted})
			var actual any
			_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), o.HomeDir, "$HOME")), &actual)
			if !reflect.DeepEqual(source.Results[i], actual) {
				expected, _ := json.Marshal(source.Results[i])
				t.Errorf("Source %s Go %s", expected, data)
			}
		})
	}
}
