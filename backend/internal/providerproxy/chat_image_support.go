package providerproxy

import (
	"regexp"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const chatTextOnlyTTL = int64(30 * 60 * 1000)
const chatTextOnlyMaximum = 256

type ChatImageSupport struct {
	mu    sync.Mutex
	until map[string]int64
	order []string
}

func NewChatImageSupport() *ChatImageSupport { return &ChatImageSupport{until: map[string]int64{}} }
func chatImageKey(endpoint, model string) string {
	return strings.ToLower(strings.TrimRight(endpoint, "/")) + "\n" + strings.ToLower(model)
}
func (cache *ChatImageSupport) Mode(endpoint, model string, now int64) string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := chatImageKey(endpoint, model)
	if until, found := cache.until[key]; found && until > now {
		return "text_only"
	}
	delete(cache.until, key)
	return "vision"
}
func (cache *ChatImageSupport) Remember(endpoint, model string, now int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := chatImageKey(endpoint, model)
	filtered := cache.order[:0]
	for _, old := range cache.order {
		if old != key {
			if _, exists := cache.until[old]; exists {
				filtered = append(filtered, old)
			}
		}
	}
	cache.order = filtered
	delete(cache.until, key)
	if len(cache.until) >= chatTextOnlyMaximum {
		delete(cache.until, cache.order[0])
		cache.order = cache.order[1:]
	}
	cache.until[key] = now + chatTextOnlyTTL
	cache.order = append(cache.order, key)
}

var nonMultimodalModel = regexp.MustCompile(`\bnot an? (multimodal|multi-modal|vision) model\b`)
var imageContentProblem = regexp.MustCompile(`(?i)\bmime\b|\bformats?\b|\bgif\b|animated|image type|multiple images|too many images|number of images|too large|exceed|\bsize\b|dimension|resolution|pixel|corrupt|decod|base64|download|fetch|could not process|unable to process|region`)

func UnsupportedImageInput(message string) bool {
	raw := strings.ToLower(message)
	if nonMultimodalModel.MatchString(raw) {
		return true
	}
	if !strings.Contains(raw, "image") {
		return false
	}
	if strings.Contains(raw, "image_url") {
		for _, phrase := range []string{"not allowed", "not permitted", "disallowed", "forbidden", "only supported by certain models"} {
			if strings.Contains(raw, phrase) {
				return true
			}
		}
		if strings.Contains(raw, "text") {
			for _, phrase := range []string{"expected", "input should be", "not one of", "permitted", "received", "unknown variant", "invalid value", "invalid type", "valid enumeration", "only text"} {
				if strings.Contains(raw, phrase) {
					return true
				}
			}
		}
	}
	for _, phrase := range []string{"not support", "not supported", "unsupported", "vision", "multimodal", "multi-modal", "modality"} {
		if strings.Contains(raw, phrase) {
			return true
		}
	}
	return false
}

func OpenAIChatImageRejection(status int, text string, request protocol.JSONObject) bool {
	if status != 400 && status != 422 || !UnsupportedImageInput(text) || imageContentProblem.MatchString(text) {
		return false
	}
	for _, raw := range requestArray(request.Get("messages")) {
		message := responseObject(raw)
		for _, part := range requestArray(message.Get("content")) {
			if responseObject(part).String("type") == "image_url" {
				return true
			}
		}
	}
	return false
}
