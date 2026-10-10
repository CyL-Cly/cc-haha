package messages

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func fixtureFactory(t *testing.T) (*Factory, *int) {
	t.Helper()
	calls := 0
	factory, err := NewFactory(func() string { calls++; return fmt.Sprintf("fixture-id-%d", calls) }, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		t.Fatal(err)
	}
	return factory, &calls
}
func options(raw string) storage.Object {
	value, _, err := storage.NormalizeObject(json.RawMessage(raw))
	if err != nil {
		panic(err)
	}
	return value
}
func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func TestUserEmptyContentOptionalMetadataAndExplicitIdentity(t *testing.T) {
	for _, raw := range []string{`{}`, `{"content":""}`, `{"content":null}`, `{"content":false}`, `{"content":0}`, `{"content":-0}`, `{"content":1e-400}`} {
		factory, calls := fixtureFactory(t)
		message := factory.CreateUser(options(raw))
		api := options(string(message["message"]))
		if rawString(api["content"]) != NoContent || rawString(message["uuid"]) != "fixture-id-1" || rawString(message["timestamp"]) != "2023-11-14T22:13:20.000Z" || *calls != 1 {
			t.Fatalf("raw=%s message=%s calls=%d", raw, objectJSON(message), *calls)
		}
	}
	factory, calls := fixtureFactory(t)
	message := factory.CreateUser(options(`{"content":[],"uuid":"custom","timestamp":"","isMeta":false,"isVirtual":true,"mcpMeta":{"future":{"high":"\ud800"}},"toolUseResult":{"integer":9007199254740993},"origin":{"type":"fixture"},"imagePasteIds":[],"sourceToolAssistantUUID":"assistant-id","permissionMode":"plan","summarizeMetadata":{"messagesSummarized":2},"isCompactSummary":true,"isVisibleInTranscriptOnly":true,"ignored":"no"}`))
	if *calls != 0 || string(options(string(message["message"]))["content"]) != "[]" || string(message["isMeta"]) != "false" || string(message["timestamp"]) != `""` || message["ignored"] != nil || !strings.Contains(string(message["mcpMeta"]), `\ud800`) {
		t.Fatalf("message=%s calls=%d", objectJSON(message), *calls)
	}
	for _, raw := range []string{`{"content":{}}`, `{"content":true}`, `{"content":1e400}`, `{"content":"\ud800"}`} {
		result := factory.CreateUser(options(raw))
		if string(options(string(result["message"]))["content"]) != string(options(raw)["content"]) {
			t.Fatal("truthy content was rewritten", raw)
		}
	}
	message = factory.CreateUser(options(`{"content":"hello","uuid":"","timestamp":null}`))
	if rawString(message["uuid"]) == "" || rawString(message["timestamp"]) == "" {
		t.Fatal("fallback not applied")
	}
	if _, err := NewFactory(nil, time.Now); err == nil {
		t.Fatal("missing UUID source accepted")
	}
	if _, err := NewFactory(func() string { return "id" }, nil); err == nil {
		t.Fatal("missing clock accepted")
	}
}

func TestSyntheticAssistantIdentityUsageAndContent(t *testing.T) {
	factory, calls := fixtureFactory(t)
	for _, raw := range []string{`{"content":""}`, `{"content":"中文 😀 \ud800"}`, `{"content":[]}`, `{"content":null}`, `{}`} {
		before := *calls
		message := factory.CreateAssistant(options(raw))
		api := options(string(message["message"]))
		if *calls != before+2 || rawString(message["uuid"]) != fmt.Sprintf("fixture-id-%d", before+1) || rawString(api["id"]) != fmt.Sprintf("fixture-id-%d", before+2) || rawString(api["model"]) != SyntheticModel || string(message["isApiErrorMessage"]) != "false" || string(api["usage"]) != string(defaultUsage) {
			t.Fatalf("raw=%s message=%s", raw, objectJSON(message))
		}
	}
	message := factory.CreateAssistant(options(`{"content":[{"type":"tool_use","id":"call","name":"fixture","input":{}}],"usage":null,"isVirtual":true,"apiError":"ignored"}`))
	api := options(string(message["message"]))
	if string(api["usage"]) != "null" || string(message["isVirtual"]) != "true" || message["apiError"] != nil {
		t.Fatal(string(objectJSON(message)))
	}
	message = factory.CreateAssistantAPIError(options(`{"content":"","apiError":"rate_limit","error":"fixture","errorDetails":"detail","businessErrorCode":"code","sourceModel":"fixture-model","usage":{"ignored":1},"isVirtual":true}`))
	api = options(string(message["message"]))
	if string(message["isApiErrorMessage"]) != "true" || message["isVirtual"] != nil || string(api["usage"]) != string(defaultUsage) || rawString(message["sourceModel"]) != "fixture-model" {
		t.Fatal(string(objectJSON(message)))
	}
}

func TestPreparationAndInterruptionPreserveOrderedContent(t *testing.T) {
	input := json.RawMessage(`"中文\ud800"`)
	if string(PrepareUserContent(input, nil)) != string(input) {
		t.Fatal("plain input changed")
	}
	preceding := []json.RawMessage{json.RawMessage(`{"type":"image","source":{"type":"base64","data":"fixture"}}`)}
	actual := PrepareUserContent(input, preceding)
	var blocks []json.RawMessage
	if json.Unmarshal(actual, &blocks) != nil || len(blocks) != 2 || string(blocks[0]) != string(preceding[0]) || string(options(string(blocks[1]))["text"]) != string(input) {
		t.Fatal(string(actual))
	}
	factory, _ := fixtureFactory(t)
	for _, toolUse := range []bool{false, true} {
		message := factory.CreateUserInterruption(toolUse)
		api := options(string(message["message"]))
		var content []storage.Object
		_ = json.Unmarshal(api["content"], &content)
		want := Interrupt
		if toolUse {
			want = InterruptForToolUse
		}
		if len(content) != 1 || rawString(content[0]["text"]) != want {
			t.Fatal(string(objectJSON(message)))
		}
	}
}
