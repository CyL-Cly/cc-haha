package providerproxy

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestSyncOpenAIFastRequestTierOnlyExactEnabled(t *testing.T) {
	original := protocol.NewJSONObject(requestField("model", "gpt-6-sol"), requestField("max_tokens", 64), requestField("messages", []any{map[string]any{"role": "user", "content": "private task"}}))
	for _, flag := range []string{"", "0", "true", "1"} {
		got, e := PrepareOpenAIRequestBody(original, map[string]string{"CC_HAHA_OPENAI_FAST_MODE": flag}, requestEncode("private-session"))
		if e != nil {
			t.Fatal(e)
		}
		if flag == "1" && got.Body.String("service_tier") != "priority" {
			t.Fatal(string(requestEncode(got.Body)))
		}
		if flag != "1" && got.Body.Get("service_tier") != nil {
			t.Fatal("priority leaked", flag)
		}
	}
	if original.Get("service_tier") != nil {
		t.Fatal("request input mutated")
	}
}
