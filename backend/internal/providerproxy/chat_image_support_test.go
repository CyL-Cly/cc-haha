package providerproxy

import (
	"fmt"
	"testing"
)

func TestLearnedImageRefusalScopeTTLAndBound(t *testing.T) {
	cache := NewChatImageSupport()
	cache.Remember("https://ENDPOINT/", "MODEL", 10)
	if cache.Mode("https://endpoint", "model", 11) != "text_only" || cache.Mode("https://other", "model", 11) != "vision" || cache.Mode("https://endpoint", "other", 11) != "vision" {
		t.Fatal("cross-endpoint/model learning")
	}
	if cache.Mode("https://endpoint", "model", 10+chatTextOnlyTTL) != "vision" {
		t.Fatal("expired refusal retained")
	}
	for i := 0; i < chatTextOnlyMaximum+1; i++ {
		cache.Remember("https://endpoint", fmt.Sprint(i), 10)
	}
	if len(cache.until) != chatTextOnlyMaximum || cache.Mode("https://endpoint", "0", 11) != "vision" {
		t.Fatal("unbounded refusal cache")
	}
}
