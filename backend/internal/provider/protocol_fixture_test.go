package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This fixture exercises the actual HTTP transport and shared commit codec.
// Request generation remains the resolved-params lane's separate proof; this
// cannot stand in for a complete Agent loop or its assistant envelopes.
func TestProtocolTransportParityFixture(t *testing.T) {
	path := os.Getenv("CC_HAHA_PROTOCOL_FIXTURE")
	if path == "" {
		t.Skip("only used by the hermetic TS/Go HTTP streaming differential lane")
	}
	home := os.Getenv("HOME")
	if home == "" || filepath.Dir(path) != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		t.Fatal("Protocol fixture requires explicit disposable HOME/config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Wire      *string `json:"wire"`
		WireBytes []byte  `json:"wireBytes"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if (fixture.Wire != nil) == (fixture.WireBytes != nil) {
		t.Fatal("Protocol fixture requires exactly one valid wire or wireBytes input")
	}
	wire := fixture.WireBytes
	if fixture.Wire != nil {
		wire = []byte(*fixture.Wire)
	}
	var mu sync.Mutex
	httpTrace := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		httpTrace = append(httpTrace, map[string]any{
			"method": request.Method, "path": request.URL.RequestURI(),
			"apiKey": observedHeader(request.Header, "x-api-key"), "authorization": observedHeader(request.Header, "authorization"),
			"version": observedHeader(request.Header, "anthropic-version"),
		})
		mu.Unlock()
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write(wire)
	}))
	defer server.Close()
	client := NewAnthropic(AnthropicConfig{APIKey: "fixture-key", BaseURL: server.URL, Timeout: time.Second})
	message, decodeErr := client.StreamMessage(context.Background(), &MessagesRequest{Path: "/v1/messages?beta=true", Body: []byte(`{"model":"fixture-model","stream":true}`)}, nil)
	content := []protocol.ContentBlock{}
	var id, stop any = "", ""
	var usage any
	if message != nil {
		content = message.Blocks()
		if value := (*message)["id"]; len(value) > 0 {
			id = value
		}
		if value := (*message)["stop_reason"]; len(value) > 0 && string(value) != "null" {
			stop = value
		}
		if len(content) > 0 {
			usage = map[string]int64{
				"input_tokens": message.Usage().Int64("input_tokens"), "output_tokens": message.Usage().Int64("output_tokens"),
				"cache_creation_input_tokens": message.Usage().Int64("cache_creation_input_tokens"), "cache_read_input_tokens": message.Usage().Int64("cache_read_input_tokens"),
			}
		}
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	contentJSON, err = protocol.ParseAndStringifyJavaScriptJSON(contentJSON)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	encoded, err := json.Marshal(map[string]any{"messageID": id, "stopReason": stop, "content": content, "contentBytes": string(contentJSON), "failed": decodeErr != nil, "usage": usage, "http": httpTrace})
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("GO_PROTOCOL_RESULT:%s\n", encoded)
}

func observedHeader(headers http.Header, name string) any {
	if len(headers.Values(name)) == 0 {
		return nil
	}
	return headers.Get(name)
}
