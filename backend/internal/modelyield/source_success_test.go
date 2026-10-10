package modelyield

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Reuse the independently captured actual Source physical request corpus as
// native model-yield evidence. The raw socket server supplies only responses;
// Run independently prepares history/requests and consumes live envelopes.
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
func TestActualSourcePhysicalModelYieldGolden(t *testing.T) {
	raw, err := os.ReadFile("../requestretry/testdata/source_contract.json")
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
			actual, err := observeModelFixture(modelFixtureInput{fixtureInput: input, Layer: "query"})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(actual.fixtureObservation)
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
