package requestcontext

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"reflect"
	"testing"
)

func TestCachedSubscriberMetadataBetasPreserveCountingDomain(t *testing.T) {
	model := "claude-opus-5-5[1m]"
	for _, origin := range []string{"", "https://api.anthropic.com", "http://127.0.0.1:12345"} {
		env := map[string]string{"ANTHROPIC_BASE_URL": origin, "USER_TYPE": "external"}
		count, err := DirectCountingBetas(model, env, protocol.NewJSONObject())
		if err != nil {
			t.Fatal(err)
		}
		ordinary, err := CachedSubscriberModelBetas(model, env, protocol.NewJSONObject(), false)
		if err != nil || !reflect.DeepEqual(ordinary, count) {
			t.Fatal("non-subscriber changed counting domain", origin, ordinary, count, err)
		}
		subscriber, err := CachedSubscriberModelBetas(model, env, protocol.NewJSONObject(), true)
		expected := []string{"claude-code-20250219", "oauth-2025-04-20", "context-1m-2025-08-07", "interleaved-thinking-2025-05-14", "context-management-2025-06-27", "prompt-caching-scope-2026-01-05"}
		if err != nil || !reflect.DeepEqual(subscriber, expected) {
			t.Fatal("cached subscriber model beta order", origin, subscriber, err)
		}
	}
	for _, env := range []map[string]string{{"USER_TYPE": "ant"}, {"CLAUDE_CODE_USE_BEDROCK": "1"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_REMOTE": "1"}} {
		for _, subscriber := range []bool{false, true} {
			if _, err := CachedSubscriberModelBetas(model, env, protocol.NewJSONObject(), subscriber); err == nil {
				t.Fatal("metadata widened provider/internal domain", env, subscriber)
			}
		}
		if _, err := DirectCountingBetas(model, env, protocol.NewJSONObject()); err == nil {
			t.Fatal("finite counting guard weakened", env)
		}
	}
}
