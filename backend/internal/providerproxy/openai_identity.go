package providerproxy

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var openAIUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func openAITrimJSON(raw json.RawMessage) json.RawMessage {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil
	}
	first, end := 0, len(units)
	for first < end && protocol.IsJavaScriptWhitespace(rune(units[first])) {
		first++
	}
	for end > first && protocol.IsJavaScriptWhitespace(rune(units[end-1])) {
		end--
	}
	return protocol.QuoteJavaScriptUTF16(units[first:end])
}

// ResolveOpenAIRequestIdentity uses the root session as the UUIDv5 namespace.
// The raw session field keeps JavaScript UTF-16; only SHA1's UTF8 observation
// replaces lone surrogates as Node's Buffer does.
func ResolveOpenAIRequestIdentity(root, agent json.RawMessage) (protocol.JSONObject, bool) {
	session := openAITrimJSON(root)
	if !requestTruthy(session) {
		return protocol.JSONObject{}, false
	}
	result := requestRecord(requestField("sessionId", session), requestField("threadId", session))
	if !requestTruthy(agent) {
		return result, true
	}
	var sessionText, agentText string
	_ = json.Unmarshal(session, &sessionText)
	_ = json.Unmarshal(agent, &agentText)
	namespace, name := "6ba7b811-9dad-11d1-80b4-00c04fd430c8", ""
	if openAIUUID.MatchString(sessionText) {
		namespace, name = sessionText, agentText
	} else {
		name = string(requestEncode([]json.RawMessage{requestEncode("cc-haha"), session, agent}))
	}
	bytes, _ := hex.DecodeString(strings.ReplaceAll(namespace, "-", ""))
	hash := sha1.New()
	_, _ = hash.Write(bytes)
	_, _ = hash.Write([]byte(name))
	id := hash.Sum(nil)[:16]
	id[6] = id[6]&0x0f | 0x50
	id[8] = id[8]&0x3f | 0x80
	text := hex.EncodeToString(id)
	result.Set("threadId", requestEncode(fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])))
	return result, true
}

func ResolvePromptCacheKey(body protocol.JSONObject, headerSession json.RawMessage) json.RawMessage {
	metadata := requestObject(body.Get("metadata"))
	if user := metadata.Get("user_id"); requestIsString(user) {
		units, _ := protocol.DecodeJavaScriptJSONString(user)
		marker := utf16.Encode([]rune("_session_"))
		for i := 0; i+len(marker) <= len(units); i++ {
			match := true
			for j := range marker {
				match = match && units[i+j] == marker[j]
			}
			if match {
				if rest := units[i+len(marker):]; len(rest) > 0 {
					return protocol.QuoteJavaScriptUTF16(rest)
				}
				break
			}
		}
	}
	if session := metadata.Get("session_id"); requestIsString(session) && requestTruthy(session) {
		return session
	}
	if session := openAITrimJSON(headerSession); requestTruthy(session) {
		return session
	}
	return nil
}
