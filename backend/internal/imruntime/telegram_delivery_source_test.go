package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func runIMSource(t *testing.T, fixture, gate string, input any, home string, extraEnv ...map[string]string) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	if home == "" {
		home = t.TempDir()
	}
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/" + fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), gate + "=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	for _, env := range extraEnv {
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	if !json.Valid(out) {
		t.Fatal("invalid Source output", string(out), stderr.String())
	}
	return out
}
func exactJSON(t *testing.T, got, want json.RawMessage, path string) {
	t.Helper()
	got = bytes.TrimSpace(got)
	want = bytes.TrimSpace(want)
	if len(got) == 0 || len(want) == 0 {
		if !bytes.Equal(got, want) {
			t.Fatalf("%s missing got%s want%s", path, got, want)
		}
		return
	}
	if got[0] == '"' && want[0] == '"' {
		a, _ := protocol.DecodeJavaScriptJSONString(got)
		b, _ := protocol.DecodeJavaScriptJSONString(want)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s UTF16 got%s want%s", path, got, want)
		}
		return
	}
	if got[0] == '{' && want[0] == '{' {
		var a, b map[string]json.RawMessage
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(want, &b)
		if len(a) != len(b) {
			t.Fatalf("%s object got%s want%s", path, got, want)
		}
		for key, value := range a {
			exactJSON(t, value, b[key], path+"."+key)
		}
		return
	}
	if got[0] == '[' && want[0] == '[' {
		var a, b []json.RawMessage
		_ = json.Unmarshal(got, &a)
		_ = json.Unmarshal(want, &b)
		if len(a) != len(b) {
			t.Fatalf("%s array got%s want%s", path, got, want)
		}
		for i := range a {
			exactJSON(t, a[i], b[i], path+"["+stringNumber(i)+"]")
		}
		return
	}
	var a, b any
	_ = json.Unmarshal(got, &a)
	_ = json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%s got%s want%s", path, got, want)
	}
}

type privateDelivery struct {
	mu       sync.Mutex
	calls    []any
	failures []protocol.JSONObject
	next     int64
}

func (p *privateDelivery) failure(method string) error {
	if len(p.failures) == 0 || p.failures[0].String("method") != method {
		return nil
	}
	failure := object(p.failures[0].Get("error"))
	p.failures = p.failures[1:]
	if failure.Get("error_code") != nil {
		var code int
		_ = json.Unmarshal(failure.Get("error_code"), &code)
		e := &TelegramAPIError{Code: code, Description: failure.String("description"), Raw: failure}
		params := object(failure.Get("parameters"))
		if value := params.Get("retry_after"); value != nil {
			var seconds float64
			_ = json.Unmarshal(value, &seconds)
			e.RetryAfter = &seconds
		}
		return e
	}
	return &TelegramTransportError{Code: failure.String("code"), Name: failure.String("name"), Message: failure.String("message"), Raw: failure}
}
func (p *privateDelivery) SendMessage(_ context.Context, chat string, text Text) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, map[string]any{"method": "send", "chat": 42, "text": text})
	if err := p.failure("send"); err != nil {
		return 0, err
	}
	p.next++
	return p.next, nil
}
func (p *privateDelivery) EditMessage(_ context.Context, chat string, id int64, text Text) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, map[string]any{"method": "edit", "chat": 42, "id": id, "text": text})
	return p.failure("edit")
}
func TestTelegramActualSourceUTF16TablesPermissionAndDeliveryRetries(t *testing.T) {
	profiles := []protocol.JSONObject{}
	add := func(v any) { profiles = append(profiles, object(raw(v))) }
	for _, text := range []Text{NewText("a 😀 b 😀 c"), TextJSON([]byte(`"\ud83dA\udc00 B"`)), NewText(" first\n\nsecond. third final words"), NewText("| Name | Detail |\n| --- | :---: |\n| A | 你好😀 |\n| B | abc |\nend"), NewText("```\n| A | B |\n| --- | --- |\n| x | y |\n```\ntext"), NewText("| A | | C |\n| --- | --- | --- |\n| x | z | | fourth |\n| | | |")} {
		for _, limit := range []int{2, 8, 20, 1000} {
			add(map[string]any{"kind": "format", "text": text, "current": "  pre😀", "limit": limit, "preview": 4})
		}
	}
	for _, text := range []string{"/ALLOW req extra", "/always\u2028req tail", "/allow-always req", "2", "yes", "不允许", "/allow", "\ufeff3 ", "hello"} {
		for _, pending := range [][]string{{"req"}, {"a", "b"}, {}} {
			add(map[string]any{"kind": "permission", "text": text, "pending": pending, "callback": "permit:req:always", "id": "req", "decision": PermissionDecision{RequestID: "req", Allowed: true, Rule: "always"}})
		}
	}
	for _, callback := range []string{"permit::yes", "permit:a:no", "permit:a:yes:x", "x", "permit:a:YES"} {
		add(map[string]any{"kind": "permission", "text": "/deny req", "pending": []string{}, "callback": callback, "id": "req", "decision": PermissionDecision{}})
	}
	event := func(kind, text string) map[string]any {
		return map[string]any{"type": kind, "blockType": "text", "text": text}
	}
	add(map[string]any{"kind": "delivery", "events": []any{event("content_start", ""), event("content_delta", "hello"), event("message_complete", "")}})
	add(map[string]any{"kind": "delivery", "threshold": 2, "events": []any{event("content_delta", "你好😀\n\n"), event("content_delta", strings.Repeat("a", 4100)), event("message_complete", "")}})
	add(map[string]any{"kind": "delivery", "events": []any{event("content_start", ""), event("content_delta", "hello"), event("message_complete", "")}, "failures": []any{map[string]any{"method": "edit", "error": map[string]any{"error_code": 429, "description": "flood", "parameters": map[string]any{"retry_after": 0.125}}}, map[string]any{"method": "edit", "error": map[string]any{"code": "ECONNRESET", "message": "private reset"}}}})
	add(map[string]any{"kind": "delivery", "events": []any{event("content_start", ""), event("content_delta", strings.Repeat("word ", 1700)), event("message_complete", ""), event("message_complete", "")}, "failures": []any{map[string]any{"method": "edit", "error": map[string]any{"error_code": 400, "description": "permanent"}}, map[string]any{"method": "send", "error": map[string]any{"error_code": 400, "description": "permanent send"}}}})
	add(map[string]any{"kind": "delivery", "threshold": 2, "events": []any{event("content_start", ""), event("content_delta", "abc"), event("content_delta", "def"), event("message_complete", "")}, "failures": []any{map[string]any{"method": "edit", "error": map[string]any{"error_code": 400, "description": "stream failed"}}}})
	add(map[string]any{"kind": "delivery", "events": []any{event("content_start", ""), event("content_delta", "  \ufeff  "), event("message_complete", "")}})
	want := runIMSource(t, "native-telegram-delivery.fixture.ts", "CC_HAHA_TELEGRAM_DELIVERY_FIXTURE", map[string]any{"profiles": profiles}, "")
	rows := []any{}
	for _, profile := range profiles {
		switch profile.String("kind") {
		case "format":
			text, current := TextJSON(profile.Get("text")), TextJSON(profile.Get("current"))
			var limit, preview int
			_ = json.Unmarshal(profile.Get("limit"), &limit)
			_ = json.Unmarshal(profile.Get("preview"), &preview)
			rows = append(rows, map[string]any{"outbound": TelegramOutbound(text), "streaming": TelegramStreaming(text), "split": SplitMessage(text, limit), "chunk": SplitTelegramStreaming(text, limit), "thinking": TelegramThinking(current, text, preview), "plan": PlanTelegramStreaming(current, text, limit)})
		case "permission":
			var pending []string
			_ = json.Unmarshal(profile.Get("pending"), &pending)
			var decision PermissionDecision
			_ = json.Unmarshal(profile.Get("decision"), &decision)
			rows = append(rows, map[string]any{"command": ParsePermissionCommand(profile.String("text"), pending), "callback": ParsePermitCallback(profile.String("callback")), "instruction": PermissionInstructions(profile.String("id")), "status": PermissionDecisionStatus(decision)})
		default:
			api := &privateDelivery{calls: []any{}}
			_ = json.Unmarshal(profile.Get("failures"), &api.failures)
			sleeps := []int64{}
			var threshold int
			_ = json.Unmarshal(profile.Get("threshold"), &threshold)
			delivery := NewTelegramDelivery(context.Background(), api, TelegramDeliveryOptions{Interval: time.Minute, Threshold: threshold, Sleep: func(_ context.Context, delay time.Duration) error {
				sleeps = append(sleeps, delay.Milliseconds())
				return nil
			}})
			var events []protocol.JSONObject
			_ = json.Unmarshal(profile.Get("events"), &events)
			snapshots := []any{}
			for _, event := range events {
				err := delivery.Handle("42", event.String("type"), event.String("blockType"), TextJSON(event.Get("text")))
				if state := delivery.state("42"); state != nil {
					state.buffer.mu.Lock()
					active := state.buffer.active
					state.buffer.mu.Unlock()
					if active != nil {
						<-active
					}
				}
				row := map[string]any{"state": delivery.Snapshot("42")}
				if err != nil {
					switch e := err.(type) {
					case *TelegramAPIError:
						row["error"] = e.Raw
					case *TelegramTransportError:
						row["error"] = e.Raw
					default:
						t.Fatal(err)
					}
				}
				snapshots = append(snapshots, row)
			}
			delivery.Close()
			rows = append(rows, map[string]any{"calls": api.calls, "sleeps": sleeps, "snapshots": snapshots})
		}
	}
	exactJSON(t, raw(rows), want, "profiles")
}
