package autoquestion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func actualSource(t *testing.T, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	v := object(encode(input))
	v.Set("homeDir", encode(home))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-auto-question.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AUTO_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encode(v))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	cap := object(object(out).Get("capabilities"))
	var blocked []string
	_ = json.Unmarshal(cap.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal(blocked)
	}
	return out
}
func questionInput(multi bool) json.RawMessage {
	return encode(map[string]any{"questions": []any{map[string]any{"question": "Which format?", "options": []any{map[string]string{"label": "JSON", "description": "建议采用此格式"}, map[string]string{"label": "Markdown"}}, "multiSelect": multi}}})
}
func TestAutomaticQuestionPromptParserAndContextMatchActualSource(t *testing.T) {
	type scenario struct {
		Input, Settings   json.RawMessage
		Context, Response string
		Messages          []json.RawMessage
		IncludeSidechain  bool
	}
	base := scenario{Input: questionInput(false), Settings: []byte(`{"enabled":true,"timeoutMinutes":1}`), Context: strings.Repeat("😀", 3500) + "User wants JSON.", Response: `{"answers":[{"questionIndex":0,"optionLabels":["JSON"]}]}`, Messages: []json.RawMessage{[]byte(`{"type":"user","content":""}`), []byte(`{"type":"assistant","isSidechain":true,"content":"child"}`), []byte(`{"type":"assistant","content":[{"type":"text","text":"hello"},{"type":"tool_use"},{"type":"text","text":""},{"type":"text","text":"world"}]}`)}}
	cases := []scenario{base}
	for _, response := range []string{`{"answers":[]}`, `{"answers":[{"questionIndex":0,"optionLabels":["Other"]}]}`, `{"answers":[{"questionIndex":0,"optionLabels":["JSON","Markdown"]}]}`, `{"answers":[{"questionIndex":0,"optionLabels":["JSON","JSON"]}]}`, `{"answers":[{"questionIndex":0.5,"optionLabels":["JSON"]}]}`, "```json\n{}\n```", `{"answers":[{"questionIndex":0,"optionLabels":["JSON"],"extra":true}]}`, `{"answers":[{"questionIndex":0,"optionLabels":["JSON"]}],"extra":true}`} {
		c := base
		c.Response = response
		cases = append(cases, c)
	}
	c := base
	c.Input = questionInput(true)
	c.Response = `{"answers":[{"questionIndex":0,"optionLabels":["Markdown","JSON"]}]}`
	c.IncludeSidechain = true
	cases = append(cases, c)
	for _, input := range []string{`{"questions":[]}`, `{"questions":[null]}`, `{"questions":[{"question":"  ","options":[{"label":"A"},{"label":"B"}]}]}`, `{"questions":[{"question":"q","multiSelect":true,"options":[{"label":"A,B"},{"label":"C"}]}]}`, `{"questions":[{"question":"q","options":[{"label":"A"},{"label":"A"}]}]}`, `{"questions":[{"question":"q","options":[{"label":"A","description":42},{"label":"B"}]}]}`} {
		c := base
		c.Input = []byte(input)
		cases = append(cases, c)
	}
	for _, settings := range []string{`null`, `[]`, `{"enabled":1,"timeoutMinutes":"1"}`, `{"enabled":true,"timeoutMinutes":30}`, `{"enabled":true,"timeoutMinutes":-1}`} {
		c := base
		c.Settings = []byte(settings)
		cases = append(cases, c)
	}
	inputCases := []map[string]any{}
	for _, c := range cases {
		inputCases = append(inputCases, map[string]any{"input": c.Input, "settings": c.Settings, "context": c.Context, "response": c.Response, "messages": c.Messages, "includeSidechain": c.IncludeSidechain})
	}
	out := actualSource(t, map[string]any{"cases": inputCases})
	var rows []json.RawMessage
	_ = json.Unmarshal(object(out).Get("rows"), &rows)
	if len(rows) != len(cases) {
		t.Fatal(string(out))
	}
	for i, c := range cases {
		q := ParseQuestions(c.Input)
		row := object(rows[i])
		got := NormalizeSettings(c.Settings)
		want := object(row.Get("settings"))
		if got.Enabled != (string(want.Get("enabled")) == "true") || got.TimeoutMinutes != intNumber(want.Get("timeoutMinutes")) {
			t.Fatalf("settings %d %v %v", i, got, want)
		}
		if validQuestions(q) != (string(row.Get("valid")) == "true") {
			t.Fatalf("valid %d", i)
		}
		if q != nil {
			var a, b any
			_ = json.Unmarshal(BuildPrompt(q, encode(c.Context)), &a)
			_ = json.Unmarshal(row.Get("prompt"), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("prompt %d %.300s %.300s", i, encode(a), encode(b))
			}
			answers := ParseAnswers(c.Response, q)
			_ = json.Unmarshal(encode(answers), &a)
			_ = json.Unmarshal(row.Get("answers"), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("answer %d %s %s", i, encode(a), encode(b))
			}
		}
		if string(ConversationText(c.Messages, c.IncludeSidechain)) != string(row.Get("text")) {
			t.Fatalf("context %d %s %s", i, ConversationText(c.Messages, c.IncludeSidechain), row.Get("text"))
		}
	}
}
func intNumber(data json.RawMessage) int { var n float64; _ = json.Unmarshal(data, &n); return int(n) }
func TestAutomaticQuestionActualHTTPThinkingFallbackMatchesSource(t *testing.T) {
	for _, status := range []int{200, 400, 422, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var mu sync.Mutex
			bodies := []json.RawMessage{}
			headers := []http.Header{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var v json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&v)
				mu.Lock()
				bodies = append(bodies, v)
				headers = append(headers, r.Header.Clone())
				mu.Unlock()
				if object(v).Get("thinking") != nil && status != 200 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"answers\":[{\"questionIndex\":0,\"optionLabels\":[\"JSON\"]}]}"}]}`))
			}))
			defer server.Close()
			q := ParseQuestions(questionInput(false))
			questions := object(questionInput(false)).Get("questions")
			out := actualSource(t, map[string]any{"loopbackFetchOrigin": server.URL, "cases": []any{}, "http": []any{map[string]any{"provider": map[string]any{"presetId": "custom", "name": "fixture", "apiKey": "fake-key", "authStrategy": "api_key", "baseUrl": server.URL + "/anthropic/", "apiFormat": "anthropic", "models": map[string]string{"main": "large", "haiku": "small", "sonnet": "large", "opus": "large"}}, "questions": questions, "context": "User wants JSON."}}})
			mu.Lock()
			sourceBodies := append([]json.RawMessage(nil), bodies...)
			sourceHeaders := append([]http.Header(nil), headers...)
			bodies = nil
			headers = nil
			mu.Unlock()
			id := "fixture"
			result, e := (Decider{Resolve: func(context.Context, *string, string) (*Target, error) {
				return &Target{Model: "small", URL: server.URL + "/anthropic/v1/messages", Headers: http.Header{"Content-Type": []string{"application/json"}, "Anthropic-Version": []string{"2023-06-01"}, "X-Api-Key": []string{"fake-key"}}}, nil
			}}).Decide(context.Background(), DecisionInput{Questions: q, Conversation: encode("User wants JSON."), ProviderID: &id, SessionID: "private-fixture"})
			if e != nil {
				t.Fatal(e)
			}
			var source []json.RawMessage
			_ = json.Unmarshal(object(out).Get("http"), &source)
			if len(source) != 1 {
				t.Fatal(string(out))
			}
			var a, b any
			_ = json.Unmarshal(encode(result), &a)
			_ = json.Unmarshal(source[0], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatal(a, b)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != len(sourceBodies) {
				t.Fatal(len(bodies), len(sourceBodies))
			}
			for i := range bodies {
				_ = json.Unmarshal(bodies[i], &a)
				_ = json.Unmarshal(sourceBodies[i], &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatal(string(bodies[i]), string(sourceBodies[i]))
				}
				for _, key := range []string{"Content-Type", "Anthropic-Version", "X-Api-Key", "Authorization"} {
					if headers[i].Get(key) != sourceHeaders[i].Get(key) {
						t.Fatal(key)
					}
				}
			}
		})
	}
}
