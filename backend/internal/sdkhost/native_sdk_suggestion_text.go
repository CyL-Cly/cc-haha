package sdkhost

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sdkSuggestionText(records []protocol.JSONObject) json.RawMessage {
	for _, record := range records {
		if record.String("type") != "assistant" {
			continue
		}
		message, _ := record.Object("message")
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.String("type") != "text" {
				continue
			}
			// Source selects the first text block of each assistant, even when
			// that block is empty; it then continues to the next assistant.
			units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if err != nil {
				break
			}
			for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
				units = units[1:]
			}
			for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
				units = units[:len(units)-1]
			}
			if len(units) > 0 {
				return protocol.QuoteJavaScriptUTF16(units)
			}
			break
		}
	}
	return nil
}

var sdkSuggestionFilters = []*regexp.Regexp{
	regexp.MustCompile(`\bsilence is\b|\bstay(s|ing)? silent\b`),
	regexp.MustCompile(`^\W*silence\W*$`),
	regexp.MustCompile(`^\([^\n\r\x{2028}\x{2029}]*\)$|^\[[^\n\r\x{2028}\x{2029}]*\]$`),
	regexp.MustCompile(`^\w+:[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`),
	regexp.MustCompile(`[.!?][\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+[A-Z]`),
	regexp.MustCompile(`thanks|thank you|looks good|sounds good|that works|that worked|that's all|nice|great|perfect|makes sense|awesome|excellent`),
	regexp.MustCompile(`^(let me|i'll|i've|i'm|i can|i would|i think|i notice|here's|here is|here are|that's|this is|this will|you can|you should|you could|sure,|of course|certainly)`),
}

func sdkSuggestionFiltered(text string) bool { return sdkSuggestionFilteredJSON(quote(text)) }

func sdkSuggestionFilteredJSON(encoded json.RawMessage) bool {
	units, err := protocol.DecodeJavaScriptJSONString(encoded)
	if err != nil || len(units) == 0 {
		return true
	}
	text := string(utf16.Decode(units))
	// String.toLowerCase has a full lowercase expansion for capital dotted I.
	// The filters otherwise match ASCII text, retaining JavaScript word bounds.
	lower := strings.ToLower(strings.ReplaceAll(text, "İ", "i\u0307"))
	if lower == "done" || lower == "nothing found" || lower == "nothing found." || strings.HasPrefix(lower, "nothing to suggest") || strings.HasPrefix(lower, "no suggestion") {
		return true
	}
	for _, prefix := range []string{"api error:", "prompt is too long", "request timed out", "invalid api key", "image was too large"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	words := strings.FieldsFunc(protocol.TrimJavaScriptString(text), protocol.IsJavaScriptWhitespace)
	if len(words) < 2 && !strings.HasPrefix(text, "/") {
		allowed := false
		for _, single := range []string{"yes", "yeah", "yep", "yea", "yup", "sure", "ok", "okay", "push", "commit", "deploy", "stop", "continue", "check", "exit", "quit", "no"} {
			if lower == single {
				allowed = true
				break
			}
		}
		if !allowed {
			return true
		}
	}
	if len(words) > 12 || len(units) >= 100 || strings.ContainsAny(text, "\n*") {
		return true
	}
	for i, filter := range sdkSuggestionFilters {
		value := text
		if i == 0 || i == 1 || i == 5 {
			value = lower
		}
		if i == 6 {
			value = strings.Map(func(r rune) rune {
				if r >= 'A' && r <= 'Z' {
					return r + ('a' - 'A')
				}
				return r
			}, text)
		}
		if filter.MatchString(value) {
			return true
		}
	}
	return false
}
