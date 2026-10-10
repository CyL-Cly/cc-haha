package requestretry

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

const responseStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"retry-message\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-flash\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n"
const responseWire = responseStart + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"independent success\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type fixtureServer struct {
	origin           string
	requests, closed atomic.Int32
	listener         net.Listener
	connections      sync.Map
	seen             chan struct{}
	done             chan struct{}
	once             sync.Once
	bodies           chan string
	headers          chan http.Header
}

func startFixtureServer(t *testing.T, mode string, override func(int) (int, string)) *fixtureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fixtureServer{origin: "http://" + listener.Addr().String(), listener: listener, seen: make(chan struct{}), done: make(chan struct{}), bodies: make(chan string, 16), headers: make(chan http.Header, 16)}
	t.Cleanup(func() {
		close(server.done)
		_ = listener.Close()
		server.connections.Range(func(key, value any) bool { _ = key.(net.Conn).Close(); return true })
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.connections.Store(conn, true)
			go func() {
				defer server.connections.Delete(conn)
				defer conn.Close()
				request, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				defer request.Body.Close()
				payload, _ := io.ReadAll(request.Body)
				if request.Method == "GET" {
					if request.URL.Path == "/__fixture/request-seen" {
						select {
						case <-server.seen:
						case <-server.done:
							return
						}
					}
					body := fmt.Sprintf(`{"requests":%d,"closedModelConnections":%d}`, server.requests.Load(), server.closed.Load())
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
					return
				}
				server.bodies <- string(payload)
				server.headers <- request.Header.Clone()
				number := int(server.requests.Add(1))
				defer server.closed.Add(1)
				server.once.Do(func() { close(server.seen) })
				if request.Method != "POST" || request.URL.RequestURI() != "/v1/messages?beta=true" {
					return
				}
				if mode == "abort-headers" {
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				if mode == "stop-stream" {
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: response-1\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%x\r\n%s\r\n", len(responseStart), responseStart)
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				status := 500
				if number == 2 {
					status = 429
				} else if number > 2 {
					status = 200
				}
				if mode == "overflow" {
					if number == 1 {
						status = 400
					} else {
						status = 200
					}
				}
				body := fmt.Sprintf(`{"message":"creation retry %d","future":9007199254740993,"text":"<>&\ud800"}`, status)
				if status == 400 {
					body = "{\"message\":\"input length and `max_tokens` exceed context limit: 20000 + 32000 > 50000\"}"
				}
				if status == 200 {
					body = responseWire
				}
				if override != nil {
					status, body = override(number)
				}
				contentType := "application/json"
				if status == 200 {
					contentType = "text/event-stream"
				}
				retry := ""
				if status == 429 {
					retry = "Retry-After: 1\r\nretry-after: 2\r\n"
				}
				_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d Fixture\r\nX-Uncommon: first\r\nContent-Type: %s\r\nSet-Cookie: a=1\r\nRequest-Id: response-%d\r\nX-Uncommon: last\r\nSet-Cookie: b=2\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, contentType, number, retry, len(body), body)
			}()
		}
	}()
	return server
}
func (server *fixtureServer) state() map[string]any {
	for count := 0; count < 100 && server.closed.Load() != server.requests.Load(); count++ {
		time.Sleep(5 * time.Millisecond)
	}
	return map[string]any{"requests": float64(server.requests.Load()), "closedModelConnections": float64(server.closed.Load())}
}
func TestActualSourcePhysicalRequestCreationGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Bun, Revision, Source string
		Cases                 []struct {
			Input    fixtureInput
			Expected json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Bun != "1.3.14" || contract.Revision != "0d9b296af33f2b851fcbf4df3e9ec89751734ba4" || contract.Source == "" || len(contract.Cases) != 12 {
		t.Fatal("missing pinned actual Source evidence")
	}
	for _, item := range contract.Cases {
		t.Run(item.Input.Name, func(t *testing.T) {
			server := startFixtureServer(t, item.Input.Mode, nil)
			input := item.Input
			input.Env["ANTHROPIC_BASE_URL"] = server.origin
			actual, err := observeFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var actualValue, expectedValue map[string]any
			_ = json.Unmarshal(encoded, &actualValue)
			_ = json.Unmarshal(item.Expected, &expectedValue)
			actualValue["server"] = server.state()
			if !reflect.DeepEqual(actualValue, expectedValue) {
				a, _ := json.Marshal(actualValue)
				e, _ := json.Marshal(expectedValue)
				t.Fatalf("actual independent request trace differs\nactual %s\nSource %s", a, e)
			}
		})
	}
}
func standardInput() Input {
	var history protocol.JSONObject
	_ = json.Unmarshal([]byte(`{"type":"user","message":{"role":"user","content":"01234567 original request"},"uuid":"original"}`), &history)
	max := 32000.0
	return Input{Context: requestcontext.Context{History: []protocol.JSONObject{history}, SystemPrompt: []json.RawMessage{json.RawMessage(`"private fixture system"`)}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "deepseek-flash", QuerySource: "sdk", IsNonInteractiveSession: true, MaxOutputTokensOverride: &max}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "offline-original-env-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_MAX_RETRIES": "2"}, DeviceID: "device", SessionID: "session"}}, Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}}
}
func standardDeps() Dependencies {
	return Dependencies{UUID: func() string { return "id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return nil }, Notice: func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) { return Resume, nil }}
}
func TestHostCapabilitiesFailBeforePhysicalSend(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Input, *Dependencies)
	}{
		{"nilUUID", func(i *Input, d *Dependencies) { d.UUID = nil }}, {"nilNow", func(i *Input, d *Dependencies) { d.Now = nil }}, {"nilRandom", func(i *Input, d *Dependencies) { d.Random = nil }}, {"nilSleep", func(i *Input, d *Dependencies) { d.Sleep = nil }}, {"nilNotice", func(i *Input, d *Dependencies) { d.Notice = nil }},
		{"interactive", func(i *Input, d *Dependencies) { i.Context.Options.IsNonInteractiveSession = false }}, {"thinking", func(i *Input, d *Dependencies) { i.Context.Thinking.Type = "adaptive" }},
		{"nativeTools", func(i *Input, d *Dependencies) { i.Context.Tools = []protocol.JSONObject{protocol.NewJSONObject()} }}, {"extraSchemas", func(i *Input, d *Dependencies) {
			i.Context.Options.ExtraToolSchemas = []json.RawMessage{json.RawMessage(`{}`)}
		}},
		{"fastHost", func(i *Input, d *Dependencies) { delete(i.Context.Host.Env, "CLAUDE_CODE_DISABLE_FAST_MODE") }}, {"cloudCredentials", func(i *Input, d *Dependencies) { i.Context.Host.Env["CLAUDE_CODE_USE_BEDROCK"] = "1" }},
		{"systemHistory", func(i *Input, d *Dependencies) { i.Context.History[0].Set("type", json.RawMessage(`"system"`)) }},
		{"attachmentWithoutPayload", func(i *Input, d *Dependencies) { i.Context.History[0].Set("type", json.RawMessage(`"attachment"`)) }},
		{"attachmentNullPayload", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("type", json.RawMessage(`"attachment"`))
			i.Context.History[0].Set("attachment", json.RawMessage(`null`))
		}},
		{"attachmentUnportedKind", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("type", json.RawMessage(`"attachment"`))
			i.Context.History[0].Set("attachment", json.RawMessage(`{"type":"unported_kind"}`))
		}}, {"apiErrorHistory", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("isApiErrorMessage", json.RawMessage(`true`))
		}}, {"virtualHistory", func(i *Input, d *Dependencies) { i.Context.History[0].Set("isVirtual", json.RawMessage(`false`)) }},
		{"badMessage", func(i *Input, d *Dependencies) { i.Context.History[0].Set("message", json.RawMessage(`"bad"`)) }}, {"nullMessage", func(i *Input, d *Dependencies) { i.Context.History[0].Set("message", json.RawMessage(`null`)) }},
		{"nullContent", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":null}`))
		}},
		{"badContent", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":42}`))
		}},
		{"imageContent", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"image"}]}`))
		}}, {"scalarContentBlock", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[7]}`))
		}}, {"badText", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"text","text":42}]}`))
		}},
		{"badHistoricalToolID", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("type", json.RawMessage(`"assistant"`))
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_use","id":null,"name":"Future"}]}`))
		}},
		{"badHistoricalToolName", func(i *Input, d *Dependencies) {
			i.Context.History[0].Set("type", json.RawMessage(`"assistant"`))
			i.Context.History[0].Set("message", json.RawMessage(`{"content":[{"type":"tool_use","id":"old","name":42}]}`))
		}},
		{"missingKey", func(i *Input, d *Dependencies) { i.Context.Host.Env["ANTHROPIC_API_KEY"] = "" }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			item.mutate(&input, &deps)
			sends := 0
			deps.ObserveRequest = func(string, http.Header, json.RawMessage) { sends++ }
			_, err := Open(context.Background(), input, deps)
			var cap *CapabilityError
			var preparationCap *requestcontext.CapabilityError
			if !errors.As(err, &cap) && !(item.name == "nativeTools" && errors.As(err, &preparationCap)) || sends != 0 {
				t.Fatalf("wanted explicit capability before network, got %v sends%d", err, sends)
			}
		})
	}
	_, err := Open(nil, standardInput(), standardDeps())
	var cap *CapabilityError
	if !errors.As(err, &cap) {
		t.Fatalf("nil caller context %v", err)
	}
}
func TestCreationNoticeOwnsResumeStopAndFailure(t *testing.T) {
	for _, mode := range []string{"resume", "stop", "invalid", "callback-error"} {
		t.Run(mode, func(t *testing.T) {
			server := startFixtureServer(t, "sequence", nil)
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			sleep, notices, clients, keys, attempts, preparations := 0, 0, 0, 0, 0, 0
			marker := errors.New("notice consumer failure")
			deps.OnClient = func() { clients++ }
			deps.OnKeyLookup = func() { keys++ }
			deps.OnAttempt = func(float64) { attempts++ }
			deps.OnPreparation = func([]protocol.JSONObject) { preparations++ }
			deps.Sleep = func(context.Context, float64) error { sleep++; return nil }
			deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) {
				notices++
				if server.requests.Load() != 1 && mode != "resume" {
					t.Fatal("sent before consumer advanced")
				}
				if sleep != 0 && mode != "resume" {
					t.Fatal("slept before consumer advanced")
				}
				switch mode {
				case "stop":
					return Stop, nil
				case "invalid":
					return 99, nil
				case "callback-error":
					return 0, marker
				}
				return Resume, nil
			}
			stream, err := Open(context.Background(), input, deps)
			if stream != nil {
				defer stream.Close()
			}
			switch mode {
			case "resume":
				if err != nil || stream == nil || notices != 2 || sleep != 2 || attempts != 3 {
					t.Fatalf("resume failed %v N%d S%d A%d", err, notices, sleep, attempts)
				}
			case "stop":
				if !errors.Is(err, ErrConsumerStopped) || IsCallerAbort(err) {
					t.Fatalf("stop conflated with abort/APIerror %v", err)
				}
			case "invalid":
				var cap *CapabilityError
				if !errors.As(err, &cap) {
					t.Fatal(err)
				}
			case "callback-error":
				if !errors.Is(err, marker) {
					t.Fatal(err)
				}
			}
			if clients != 1 || keys != 1 || preparations != 1 {
				t.Fatalf("repeated ownership clients%d keys%d prepare%d", clients, keys, preparations)
			}
		})
	}
}
func TestPreparationFailurePrecedesAuthAndNativeNetwork(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Profile = "unknown"
	deps.OnClient = func() { t.Fatal("auth ran before preparation failure") }
	_, err := Open(context.Background(), input, deps)
	var cap *requestcontext.CapabilityError
	if !errors.As(err, &cap) {
		t.Fatalf("lost preparation boundary %v", err)
	}
}

func TestOriginalRetainedAttachmentsReachPhysicalRetryOncePrepared(t *testing.T) {
	for _, payload := range []string{
		`{"type":"skill_listing","content":"original retained body"}`,
		`{"type":"invoked_skills","skills":[{"name":"original","path":"/fixture","content":"original retained body"}]}`,
		`{"type":"invoked_skills","skills":null}`,
		`{"type":"command_permissions","allowedTools":["Read"],"model":"original-model"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			server := startFixtureServer(t, "sequence", nil)
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			var attachment protocol.JSONObject
			if err := json.Unmarshal([]byte(`{"type":"attachment","attachment":`+payload+`}`), &attachment); err != nil {
				t.Fatal(err)
			}
			input.Context.History = append(input.Context.History, attachment)
			before, _ := json.Marshal(input.Context.History)
			prepared, notices := 0, 0
			deps.OnPreparation = func([]protocol.JSONObject) { prepared++ }
			deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) { notices++; return Resume, nil }
			stream, err := Open(context.Background(), input, deps)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for {
				_, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if prepared != 1 || notices != 2 || server.requests.Load() != 3 {
				t.Fatalf("retained history changed preparation/send ownership: prepare%d notices%d sends%d", prepared, notices, server.requests.Load())
			}
			wantContent := strings.Contains(payload, "original retained body")
			for attempt := 0; attempt < 3; attempt++ {
				body := <-server.bodies
				if strings.Contains(body, "original retained body") != wantContent {
					t.Fatalf("attempt %d lost retained content or sent malformed renderer payload: %s", attempt, body)
				}
			}
			after, _ := json.Marshal(input.Context.History)
			if string(before) != string(after) {
				t.Fatal("physical retry mutated original session history")
			}
		})
	}
}
func TestSuccessfulStreamFailuresNeverEnterCreationRetry(t *testing.T) {
	server := startFixtureServer(t, "sequence", func(int) (int, string) { return 200, "event: error\ndata: {\"message\":\"stream failure\"}\n\n" })
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	deps.Notice = func(context.Context, retrypolicy.RetryNotice) (NoticeAction, error) {
		t.Fatal("successful headers treated as creation failure")
		return 0, nil
	}
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Next()
	var sdk *provider.SDKStreamError
	if !errors.As(err, &sdk) || sdk.Kind != "sse_api" || server.requests.Load() != 1 {
		t.Fatalf("stream failure lost/moved into retry: %v sends%d", err, server.requests.Load())
	}
}
func TestSleepFailureHasNoSecondPhysicalSend(t *testing.T) {
	server := startFixtureServer(t, "sequence", nil)
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	marker := errors.New("sleep host failure")
	deps.Sleep = func(context.Context, float64) error { return marker }
	_, err := Open(context.Background(), input, deps)
	if !errors.Is(err, marker) || server.requests.Load() != 1 {
		t.Fatalf("sleep failure lost %v sends%d", err, server.requests.Load())
	}
}
func TestObservedTextBlockAndAbortedPrepare(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	input.Context.History[0].Set("message", json.RawMessage(`{"role":"user","content":[{"type":"text","text":"01234567 explicit block"}]}`))
	calls := 0
	deps.OnPreparation = func([]protocol.JSONObject) { calls++ }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Open(ctx, input, deps)
	if !IsCallerAbort(err) || calls != 1 {
		t.Fatalf("canceled before prepare %v count%d", err, calls)
	}
}
func TestErrorIdentity(t *testing.T) {
	if IsCallerAbort(nil) || IsCallerAbort((*provider.SDKStreamError)(nil)) || IsCallerAbort(errors.New("Request was aborted.")) {
		t.Fatal("ordinary failure became caller abort")
	}
	if !IsCallerAbort(fmt.Errorf("wrapped: %w", &provider.SDKStreamError{Class: "APIUserAbortError"})) {
		t.Fatal("wrapped SDK abort lost")
	}
	if !strings.Contains((&CapabilityError{Boundary: "host"}).Error(), "host") {
		t.Fatal("missing diagnostic")
	}
}

func TestObservationCannotInjectHeadersBodyOrPreparedHistory(t *testing.T) {
	server := startFixtureServer(t, "sequence", func(int) (int, string) { return 200, responseWire })
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	deps.ObserveRequest = func(_ string, headers http.Header, body json.RawMessage) {
		headers.Set("x-api-key", "injected")
		body[0] = '!'
	}
	deps.OnPreparation = func(history []protocol.JSONObject) {
		history[0].Set("message", json.RawMessage(`{"role":"user","content":"injected"}`))
	}
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	body, headers := <-server.bodies, <-server.headers
	if !json.Valid([]byte(body)) || strings.Contains(body, "injected") || headers.Get("x-api-key") != "offline-original-env-key" {
		t.Fatalf("observer changed physical request: %s %v", body, headers)
	}
}
