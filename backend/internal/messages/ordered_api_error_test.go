package messages

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestOrderedAPIErrorUsesSharedAllocationSequenceAndPropertySlots(t *testing.T) {
	var order []string
	factory, _ := NewFactory(func() string { order = append(order, "uuid"); return "id" }, func() time.Time { order = append(order, "date"); return time.UnixMilli(1700000000000) })
	record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": json.RawMessage(`"\ud800 terminal \udc00"`), "error": json.RawMessage(`"unknown"`), "errorDetails": json.RawMessage(`null`), "usage": json.RawMessage(`{"ignored":1}`)})
	if err != nil || strings.Join(order, ",") != "uuid,date,uuid" {
		t.Fatal(order, err)
	}
	raw, err := record.MarshalJSON()
	if err == nil {
		raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	}
	if err != nil || !strings.HasPrefix(string(raw), `{"type":"assistant","uuid":"id","timestamp":"2023-11-14T22:13:20.000Z","message":{"id":"id","container":null,"model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"input_tokens":0`) || !strings.Contains(string(raw), `"content":[{"type":"text","text":"\ud800 terminal \udc00"}],"context_management":null},"error":"unknown","errorDetails":null,"isApiErrorMessage":true}`) {
		t.Fatal(string(raw), err)
	}
	// The Source owns an undefined requestId slot before error metadata. A
	// later mutation must occupy that original slot, rather than append it.
	record.Set("requestId", json.RawMessage(`"later"`))
	raw, _ = record.MarshalJSON()
	if !strings.Contains(string(raw), `"requestId":"later","error":"unknown"`) {
		t.Fatal(string(raw))
	}
}

func TestOrderedAPIErrorRejectsUnsupportedInputsBeforeAllocation(t *testing.T) {
	factory, calls := fixtureFactory(t)
	for _, input := range []storage.Object{nil, {"content": json.RawMessage(`null`)}, {"content": json.RawMessage(`4`)}, {"content": json.RawMessage(`"valid"`), "errorDetails": json.RawMessage(`{}`)}} {
		if _, err := factory.CreateOrderedAssistantAPIError(input); err == nil || *calls != 0 {
			t.Fatal(input, err, *calls)
		}
	}
	var absent *Factory
	if _, err := absent.CreateOrderedAssistantAPIError(storage.Object{"content": json.RawMessage(`"valid"`)}); err == nil {
		t.Fatal("nil factory accepted")
	}
	record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": json.RawMessage(`""`), "apiError": json.RawMessage(`"fixture"`), "businessErrorCode": json.RawMessage(`"code"`), "sourceModel": json.RawMessage(`"fixture-model"`)})
	raw, _ := record.MarshalJSON()
	if err != nil || *calls != 2 || !strings.Contains(string(raw), `"text":"(no content)"`) || !strings.Contains(string(raw), `"apiError":"fixture"`) {
		t.Fatal(string(raw), err)
	}
}
