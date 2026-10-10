package messages

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrderedUserOriginalFactoryEnvelopePreservesSourceOrderAndUTF16(t *testing.T) {
	factory, calls := fixtureFactory(t)
	input := options(`{"content":"\\ud800","isMeta":false,"isVisibleInTranscriptOnly":true,"isVirtual":true,"isCompactSummary":true,"summarizeMetadata":{"count":2},"uuid":"original-id","timestamp":"","toolUseResult":{"future":"\\udfff"},"mcpMeta":{"x":1},"imagePasteIds":[],"sourceToolAssistantUUID":"assistant","permissionMode":"plan","origin":{"type":"fixture"}}`)
	input["content"] = []byte(`"\ud800"`)
	input["toolUseResult"] = []byte(`{"future":"\udfff"}`)
	result := OrderedUser(factory.CreateUser(input))
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	// Literal key order in src/utils/messages.ts:createUserMessage, including
	// role before content and the original metadata slots.
	want := `{"type":"user","message":{"role":"user","content":"\ud800"},"isMeta":false,"isVisibleInTranscriptOnly":true,"isVirtual":true,"isCompactSummary":true,"summarizeMetadata":{"count":2},"uuid":"original-id","timestamp":"","toolUseResult":{"future":"\udfff"},"mcpMeta":{"x":1},"imagePasteIds":[],"sourceToolAssistantUUID":"assistant","permissionMode":"plan","origin":{"type":"fixture"}}`
	if string(raw) != want || *calls != 0 {
		t.Fatal("Source envelope changed", string(raw), *calls)
	}
	result = OrderedUser(factory.CreateUser(options(`{"content":"original"}`)))
	fields := result.OwnProperties()
	if len(fields) != 15 || fields[2].Value != nil || string(fields[2].NameJSON) != `"isMeta"` || string(fields[7].NameJSON) != `"uuid"` {
		t.Fatal("own undefined Source slots lost", fields)
	}
	raw, _ = result.MarshalJSON()
	if strings.Contains(string(raw), "isMeta") || !strings.HasPrefix(string(raw), `{"type":"user","message":{"role":"user","content":"original"},"uuid":`) {
		t.Fatal("undefined serialized or original order changed", string(raw))
	}
}
