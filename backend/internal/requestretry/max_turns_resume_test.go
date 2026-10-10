package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// The sentinel is produced by the existing Factory used by conversation recovery.
// Source API normalization keeps false/absent No-response, filters true API errors.
func TestMaxTurnsResumeOriginalNoResponseFactoryReachesActualModelWire(t *testing.T) {
	for _, flag := range []string{"false", "absent", "true"} {
		t.Run(flag, func(t *testing.T) {
			server := startFixtureServer(t, "sequence", nil)
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			f, _ := messages.NewFactory(func() string { return "actual-no-response-factory-id" }, func() time.Time { return time.UnixMilli(1700000000000) })
			original := f.CreateAssistant(storage.Object{"content": json.RawMessage(`"No response requested."`)})
			if flag == "absent" {
				delete(original, "isApiErrorMessage")
			}
			if flag == "true" {
				original["isApiErrorMessage"] = json.RawMessage("true")
			}
			raw, _ := json.Marshal(original)
			var record protocol.JSONObject
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			record.Set("future", json.RawMessage(`{"text":"\ud800<&>"}`))
			before, _ := record.MarshalJSON()
			input.Context.History = append(input.Context.History, record, originalHistoryRecord(t, `{"type":"user","message":{"role":"user","content":"original own-session resume request"}}`))
			stream, err := Open(context.Background(), input, deps)
			if err != nil {
				t.Fatal("original Factory resume refused", err)
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
			body := <-server.bodies
			present := strings.Contains(body, "No response requested.")
			if present != (flag != "true") || !strings.Contains(body, "original own-session resume request") {
				t.Fatalf("false/absent/true classification changed: %s %s", flag, body)
			}
			after, _ := record.MarshalJSON()
			if string(before) != string(after) {
				t.Fatal("request admission mutated retained recovery envelope")
			}
		})
	}
}
