package sidequestion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceEphemeralSideServiceLifecycle(t *testing.T) {
	profiles := []string{"success", "failure", "api-error", "invalid", "pre-aborted", "tombstone", "cancel-ensure", "cancel-control", "conflict", "timeout", "reuse"}
	outcomes := []any{}
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			var mu sync.Mutex
			events := []any{}
			ensured, releaseEnsure := make(chan struct{}), make(chan struct{})
			controlled, releaseControl := make(chan struct{}), make(chan struct{})
			timeout := time.Second
			if profile == "timeout" {
				timeout = 15 * time.Millisecond
			}
			service, e := New(Options{Lifetime: context.Background(), Timeout: timeout, Ensure: func(ctx context.Context, _ string, _ *url.URL) error {
				mu.Lock()
				events = append(events, "ensure")
				mu.Unlock()
				close(ensured)
				if profile == "cancel-ensure" {
					<-releaseEnsure
				}
				return nil
			}, Control: func(ctx context.Context, _ string, request protocol.JSONObject, duration time.Duration) (protocol.JSONObject, error) {
				mu.Lock()
				events = append(events, map[string]any{"request": request, "timeout": duration.Milliseconds(), "signal": request.String("subtype") == "side_question"})
				mu.Unlock()
				if request.String("subtype") == "cancel_side_question" {
					return object(quote(map[string]any{"cancelled": true})), nil
				}
				close(controlled)
				if profile == "cancel-control" || profile == "conflict" || profile == "timeout" {
					<-releaseControl
				}
				switch profile {
				case "failure":
					return protocol.JSONObject{}, errors.New("upstream failure")
				case "api-error":
					return protocol.JSONObject{}, &Error{Status: 409, Code: "CONFLICT", Message: "specific conflict"}
				case "invalid":
					return object([]byte(`{"response":1}`)), nil
				}
				return object([]byte(`{"response":" response \ud800 "}`)), nil
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer service.Close(context.Background())
			input, _ := Parse([]byte(`{"questionId":"11111111-1111-4111-8111-111111111111","question":" current ","history":[]}`))
			address, _ := url.Parse("http://127.0.0.1/")
			signal, abort := context.WithCancel(context.Background())
			defer abort()
			outcome := func(value protocol.JSONObject, e error) any {
				if e == nil {
					return map[string]any{"value": value}
				}
				var failure *Error
				if !errors.As(e, &failure) {
					t.Error("unexpected service failure", e)
					return nil
				}
				return map[string]any{"status": failure.Status, "code": failure.Code, "message": failure.Message}
			}
			ask := func(in Input) any { v, e := service.Ask(signal, "session", in, address); return outcome(v, e) }
			var result any
			if profile == "pre-aborted" {
				abort()
				result = ask(input)
			} else if profile == "tombstone" {
				cancelled, e := service.Cancel("session", input.QuestionID)
				if e != nil {
					t.Fatal(e)
				}
				result = map[string]any{"cancelled": cancelled, "ask": ask(input)}
			} else {
				first := make(chan any, 1)
				go func() { first <- ask(input) }()
				<-ensured
				if profile == "cancel-ensure" {
					cancelled, e := service.Cancel("session", input.QuestionID)
					if e != nil {
						t.Fatal(e)
					}
					result = map[string]any{"cancelled": cancelled, "ask": <-first}
					close(releaseEnsure)
				} else {
					<-controlled
					switch profile {
					case "cancel-control":
						cancelled, e := service.Cancel("session", input.QuestionID)
						if e != nil {
							t.Fatal(e)
						}
						result = map[string]any{"cancelled": cancelled, "ask": <-first}
						close(releaseControl)
					case "conflict":
						second := input
						second.QuestionID = "22222222-2222-4222-8222-222222222222"
						next := ask(second)
						close(releaseControl)
						result = map[string]any{"second": next, "ask": <-first}
					case "timeout":
						result = <-first
						close(releaseControl)
					default:
						result = <-first
						if profile == "reuse" {
							result = map[string]any{"first": result, "second": ask(input)}
						}
					}
				}
			}
			if e := service.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			copyEvents := append([]any{}, events...)
			mu.Unlock()
			outcomes = append(outcomes, map[string]any{"profile": profile, "result": result, "events": copyEvents})
		})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/side-question-service.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"profiles": profiles})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	var expected, actual any
	if e = json.Unmarshal(source, &expected); e != nil {
		t.Fatal(e, string(source))
	}
	data, _ := json.Marshal(outcomes)
	_ = json.Unmarshal(data, &actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("actual Source %s Go %s", source, data)
	}
}
