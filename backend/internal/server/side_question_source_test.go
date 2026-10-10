package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSideQuestionPublicRouteMatchesActualSourceValidation(t *testing.T) {
	steps := []map[string]string{
		{"method": "GET", "path": "/api/sessions/existing/side-question"},
		{"method": "POST", "path": "/api/sessions/existing/side-question", "body": `{}`},
		{"method": "POST", "path": "/api/sessions/existing/side-question", "body": `{"questionId":"11111111-1111-4111-8111-111111111111","question":"ok","unknown":true}`},
		{"method": "DELETE", "path": "/api/sessions/existing/side-question/invalid", "questionId": "invalid"},
		{"method": "DELETE", "path": "/api/sessions/existing/side-question/11111111-1111-4111-8111-111111111111", "questionId": "11111111-1111-4111-8111-111111111111"},
	}
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return nil, errors.New("no model in route validation")
	})
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	id := "existing"
	// A real stored transcript exercises the same surrounding session lookup.
	project := filepath.Join(o.ConfigDir, "projects", "-fixture")
	if e = os.MkdirAll(project, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(`{"type":"user","uuid":"original","sessionId":"existing","cwd":`+string(rawTest(o.WorkDir))+`,"message":{"role":"user","content":"prior"}}`+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	actual := []any{}
	for _, step := range steps {
		r := httptest.NewRequest(step["method"], "http://127.0.0.1"+step["path"], strings.NewReader(step["body"]))
		r.RemoteAddr = "127.0.0.1:49152"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var body any
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e, w.Body.String())
		}
		actual = append(actual, map[string]any{"status": float64(w.Code), "body": body})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/side-question-api.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"steps": steps})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	var expected []any
	if e = json.Unmarshal(data, &expected); e != nil {
		t.Fatal(e, string(data))
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("original Source=%s Go=%s", data, rawTest(actual))
	}
}

func rawTest(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
