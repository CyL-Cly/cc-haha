package imruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const WechatBaseURL = "https://ilinkai.weixin.qq.com"
const WechatChannelVersion = "2.1.7"
const WechatAppClientVersion = "131335"
const WechatTextLimit = 3500

func wechatTruthy(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || string(v) == "null" || string(v) == "false" {
		return false
	}
	if v[0] == '"' {
		return len(TextJSON(v)) > 0
	}
	if v[0] == '-' || v[0] >= '0' && v[0] <= '9' {
		n, e := strconv.ParseFloat(string(v), 64)
		return (e == nil || math.IsInf(n, 0)) && n != 0
	}
	return true
}
func wechatProperty(v json.RawMessage, key string) json.RawMessage { return object(v).Get(key) }
func wechatString(v json.RawMessage) (Text, error) {
	q, e := protocol.ExactJavaScriptStringJSON(v)
	if e != nil {
		return nil, e
	}
	return TextJSON(q), nil
}
func wechatStringOnly(v json.RawMessage, message string) (Text, error) {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || v[0] != '"' {
		return nil, fmt.Errorf("%s", message)
	}
	return TextJSON(v), nil
}
func wechatFields(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func wechatField(name string, v any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(v)}
}
func wechatNumeric(v json.RawMessage) (float64, bool) {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || v[0] == '"' || v[0] == '{' || v[0] == '[' || string(v) == "null" || string(v) == "true" || string(v) == "false" {
		return 0, false
	}
	n, e := strconv.ParseFloat(string(v), 64)
	return n, e == nil || math.IsInf(n, 0)
}

// ExtractWechatText retains Source's first text item, recursive quote and raw
// voice result. It never adds a supported message type or coerces voice text.
func WechatExtractText(items json.RawMessage) (json.RawMessage, error) {
	if len(items) == 0 || string(bytes.TrimSpace(items)) == "null" {
		return raw(""), nil
	}
	var values []json.RawMessage
	if json.Unmarshal(items, &values) != nil {
		return raw(""), nil
	}
	for _, item := range values {
		if string(bytes.TrimSpace(item)) == "null" {
			return nil, fmt.Errorf("null is not an object (evaluating 'item.type')")
		}
		kind := wechatProperty(item, "type")
		n, isNumber := wechatNumeric(kind)
		if isNumber && n == 1 {
			body := wechatProperty(wechatProperty(item, "text_item"), "text")
			if len(body) > 0 && string(body) != "null" {
				text, e := wechatString(body)
				if e != nil {
					return nil, e
				}
				ref := wechatProperty(item, "ref_msg")
				if !wechatTruthy(ref) {
					return raw(text), nil
				}
				parts := []Text{}
				if title := wechatProperty(ref, "title"); wechatTruthy(title) {
					part, e := wechatString(title)
					if e != nil {
						return nil, e
					}
					parts = append(parts, part)
				}
				if child := wechatProperty(ref, "message_item"); wechatTruthy(child) {
					v, e := WechatExtractText(append(append([]byte{'['}, child...), ']'))
					if e != nil {
						return nil, e
					}
					if wechatTruthy(v) {
						part, e := wechatString(v)
						if e != nil {
							return nil, e
						}
						parts = append(parts, part)
					}
				}
				if len(parts) > 0 {
					text = NewText("[引用: ").Join(JoinText(parts, NewText(" | "))).Join(NewText("]\n")).Join(text)
				}
				return raw(text), nil
			}
		}
		if isNumber && n == 3 {
			voice := wechatProperty(wechatProperty(item, "voice_item"), "text")
			if wechatTruthy(voice) {
				return append(json.RawMessage(nil), voice...), nil
			}
		}
	}
	return raw(""), nil
}
func wechatAPIOk(body []byte, label string) error {
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return nil
	}
	value := object(bytes.TrimSpace(body))
	if !value.IsObject() {
		return nil
	}
	code, ok := wechatNumeric(value.Get("ret"))
	if !ok {
		code, _ = wechatNumeric(value.Get("errcode"))
	}
	if code == 0 {
		return nil
	}
	message := string(body)
	if v := value.Get("errmsg"); len(v) > 0 && v[0] == '"' {
		message = TextJSON(v).String()
	}
	codeRaw := value.Get("ret")
	if _, ok := wechatNumeric(codeRaw); !ok {
		codeRaw = value.Get("errcode")
	}
	codeText, e := wechatString(codeRaw)
	if e != nil {
		return e
	}
	return fmt.Errorf("%s returned %s: %s", label, codeText.String(), message)
}

// The candidates and local attachment keep display names as raw UTF-16 JSON;
// filesystem operations use their ordinary Node UTF-8 replacement spelling.
type WechatMediaCandidate struct {
	Kind              string          `json:"kind"`
	Name              json.RawMessage `json:"name"`
	MIME              string          `json:"mimeType,omitempty"`
	URL               json.RawMessage `json:"url,omitempty"`
	EncryptQueryParam json.RawMessage `json:"encryptQueryParam,omitempty"`
	AESKey            json.RawMessage `json:"aesKey,omitempty"`
}

func wechatFileMime(name json.RawMessage) (string, error) {
	if len(name) == 0 || string(name) == "null" {
		return "", nil
	}
	text, e := wechatStringOnly(name, "fileName?.split is not a function")
	if e != nil {
		return "", e
	}
	parts := strings.Split(text.String(), ".")
	ext := strings.ToLower(parts[len(parts)-1])
	switch ext {
	case "png":
		return "image/png", nil
	case "jpg", "jpeg":
		return "image/jpeg", nil
	case "gif":
		return "image/gif", nil
	case "webp":
		return "image/webp", nil
	case "pdf":
		return "application/pdf", nil
	case "txt":
		return "text/plain", nil
	}
	return "", nil
}
func WechatCollectMediaCandidates(items json.RawMessage, now func() time.Time) ([]WechatMediaCandidate, error) {
	if now == nil {
		now = time.Now
	}
	out := []WechatMediaCandidate{}
	if len(items) == 0 || string(bytes.TrimSpace(items)) == "null" {
		return out, nil
	}
	var values []json.RawMessage
	if e := json.Unmarshal(items, &values); e != nil {
		return nil, fmt.Errorf("items is not iterable")
	}
	for _, item := range values {
		if string(bytes.TrimSpace(item)) == "null" {
			return nil, fmt.Errorf("null is not an object (evaluating 'item.type')")
		}
		n, ok := wechatNumeric(wechatProperty(item, "type"))
		if !ok {
			continue
		}
		image, file := wechatProperty(item, "image_item"), wechatProperty(item, "file_item")
		kind, part := "", json.RawMessage(nil)
		if n == 2 && wechatTruthy(wechatProperty(image, "media")) {
			kind, part = "image", image
		} else if n == 4 && wechatTruthy(wechatProperty(file, "media")) {
			kind, part = "file", file
		}
		if kind == "" {
			continue
		}
		media := wechatProperty(part, "media")
		id := wechatProperty(item, "msg_id")
		if len(id) == 0 || string(id) == "null" {
			id = raw(now().UnixMilli())
		}
		idText, e := wechatString(id)
		if e != nil {
			return nil, e
		}
		name := raw(NewText("wechat-" + kind + "-").Join(idText))
		url := wechatProperty(media, "full_url")
		aes := wechatProperty(media, "aes_key")
		mime := ""
		if kind == "image" {
			name = raw(TextJSON(name).Join(NewText(".jpg")))
			mime = "image/jpeg"
			if !wechatTruthy(url) {
				url = wechatProperty(part, "url")
			}
			if hex := wechatProperty(part, "aeskey"); wechatTruthy(hex) {
				key, e := wechatStringOnly(hex, "The first argument must be of type string or an instance of Buffer")
				if e != nil {
					return nil, e
				}
				aes = raw(wechatHexBase64(key.String()))
			}
		} else {
			fileName := wechatProperty(part, "file_name")
			if wechatTruthy(fileName) {
				name = fileName
			}
			mime, e = wechatFileMime(fileName)
			if e != nil {
				return nil, e
			}
		}
		out = append(out, WechatMediaCandidate{kind, name, mime, url, wechatProperty(media, "encrypt_query_param"), aes})
	}
	return out, nil
}
