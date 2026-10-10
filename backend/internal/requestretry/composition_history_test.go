package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"strings"
	"testing"
)

// Original payload is the sealed EOF-first Source profile's synthetic envelope.
// It remains in admission history; only the existing shared Prepare filters it.
func TestCompositionSyntheticTerminalReachesSinglePrepareAndPhysicalRequest(t *testing.T) {
	server := startFixtureServer(t, "sequence", nil)
	input, deps := standardInput(), standardDeps()
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
	terminal := originalHistoryRecord(t, `{"type":"assistant","uuid":"api-terminal","timestamp":"2023-11-14T22:13:20.000Z","message":{"id":"synthetic","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: Provider stream ended before completing the response"}],"stop_reason":"stop_sequence","stop_sequence":""},"isApiErrorMessage":true,"error":"unknown"}`)
	input.Context.History = append(input.Context.History, terminal, originalHistoryRecord(t, `{"type":"user","uuid":"next-user","message":{"role":"user","content":"next original prompt"}}`))
	before, _ := json.Marshal(input.Context.History)
	preparations, clients := 0, 0
	deps.OnClient = func() { clients++ }
	deps.OnPreparation = func(history []protocol.JSONObject) {
		preparations++
		for _, record := range history {
			if record.Get("isApiErrorMessage") != nil {
				t.Error("synthetic leaked through shared Prepare")
			}
		}
	}
	stream, err := Open(context.Background(), input, deps)
	if err != nil {
		t.Fatalf("legal original terminal refused: %v", err)
	}
	defer stream.Close()
	for {
		_, err = stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	after, _ := json.Marshal(input.Context.History)
	if preparations != 1 || clients != 1 || server.requests.Load() != 3 || string(before) != string(after) {
		t.Fatalf("changed original history/single preparation: prepare%d client%d posts%d", preparations, clients, server.requests.Load())
	}
	for n := 0; n < 3; n++ {
		body := <-server.bodies
		if strings.Contains(body, "Provider stream ended") || !strings.Contains(body, "next original prompt") {
			t.Fatalf("wrong actual model body: %s", body)
		}
	}
}
