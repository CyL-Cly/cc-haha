package requestretry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func originalHistoryRecord(t *testing.T, source string) protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	if err := json.Unmarshal([]byte(source), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// These two classes already enter the actual Source normalizer and existing Go
// Prepare. Admission must not discard the original marker or protected blocks.
func TestOriginalCompactAndAssistantProtectedHistoryReachPrepare(t *testing.T) {
	for _, original := range []string{
		`{"type":"system","uuid":"original-boundary","subtype":"compact_boundary","compactMetadata":{"trigger":"manual"},"future":"<>&\ud800"}`,
		`{"type":"assistant","uuid":"original-protected","message":{"id":"bound-response","role":"assistant","model":"deepseek-flash","content":[{"type":"thinking","thinking":"original secret","signature":"original signature"},{"type":"redacted_thinking","data":"original redacted"},{"type":"text","text":"original visible"}]}}`,
	} {
		t.Run(original, func(t *testing.T) {
			server := startFixtureServer(t, "sequence", nil)
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Env["ANTHROPIC_BASE_URL"] = server.origin
			record := originalHistoryRecord(t, original)
			input.Context.History = append([]protocol.JSONObject{record}, input.Context.History...)
			before, _ := record.MarshalJSON()
			preparations, clients := 0, 0
			deps.OnPreparation = func(history []protocol.JSONObject) {
				preparations++
				for _, envelope := range history {
					if envelope.String("type") == "system" {
						t.Fatal("compact marker leaked into normalized model request")
					}
				}
			}
			deps.OnClient = func() { clients++ }
			stream, err := Open(context.Background(), input, deps)
			if err != nil {
				t.Fatalf("already migrated original history was refused: %v", err)
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
			after, _ := record.MarshalJSON()
			if string(before) != string(after) || preparations != 1 || clients != 1 || server.requests.Load() != 3 {
				t.Fatalf("history or ownership changed: preparations%d clients%d physical%d original%s", preparations, clients, server.requests.Load(), after)
			}
			if strings.Contains(original, `"thinking"`) {
				body := <-server.bodies
				if !strings.Contains(body, `"thinking":"original secret"`) || !strings.Contains(body, `"signature":"original signature"`) || !strings.Contains(body, `"data":"original redacted"`) {
					t.Fatalf("original protected payload lost from actual prepared wire: %s", body)
				}
			}
		})
	}
}

func TestOriginalHistoryAdmissionStillRejectsUnmigratedDomains(t *testing.T) {
	for _, item := range []struct {
		name     string
		record   string
		boundary string
	}{
		{"unknown-system", `{"type":"system","subtype":"unknown"}`, "unported history envelope classification"},
		{"local-command", `{"type":"system","subtype":"local_command"}`, "unported history envelope classification"},
		{"compact-virtual-presence", `{"type":"system","subtype":"compact_boundary","isVirtual":false}`, "unported history envelope classification"},
		{"compact-api-error-presence", `{"type":"system","subtype":"compact_boundary","isApiErrorMessage":false}`, "unported history envelope classification"},
		{"user-thinking", `{"type":"user","message":{"content":[{"type":"thinking","thinking":"secret","signature":"sig"}]}}`, "unported history media/tool result"},
		{"user-redacted", `{"type":"user","message":{"content":[{"type":"redacted_thinking","data":"opaque"}]}}`, "unported history media/tool result"},
		{"assistant-image", `{"type":"assistant","message":{"content":[{"type":"image","source":{"type":"base64","data":"private"}}]}}`, "unported history media/tool result"},
		{"assistant-unknown", `{"type":"assistant","message":{"content":[{"type":"future_block","thinking":"not a supported block"}]}}`, "unported history media/tool result"},
	} {
		t.Run(item.name, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			input.Context.History = []protocol.JSONObject{originalHistoryRecord(t, item.record)}
			deps.OnPreparation = func([]protocol.JSONObject) { t.Fatal("unmigrated history prepared") }
			deps.OnClient = func() { t.Fatal("unmigrated history reached auth/client") }
			deps.ObserveRequest = func(string, http.Header, json.RawMessage) { t.Fatal("unmigrated history sent") }
			_, err := Open(context.Background(), input, deps)
			var cap *CapabilityError
			if !errors.As(err, &cap) || cap.Boundary != item.boundary {
				t.Fatalf("wrong exact guard: %v", err)
			}
		})
	}
}
