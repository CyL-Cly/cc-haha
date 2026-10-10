package imregistration

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

const QQCreateBindURL = "https://q.qq.com/lite/create_bind_task"
const QQPollBindURL = "https://q.qq.com/lite/poll_bind_result"
const QQPollIntervalMillis = 2000

// qqURIComponent is the vendor's encodeURIComponent, including lone-surrogate
// rejection and its escape alphabet rather than form/query '+' encoding.
func qqURIComponent(raw json.RawMessage) (string, error) {
	text, e := protocol.ExactJavaScriptStringJSON(raw)
	if e != nil {
		return "", e
	}
	units, e := protocol.DecodeJavaScriptJSONString(text)
	if e != nil {
		return "", e
	}
	for i := 0; i < len(units); i++ {
		if units[i] >= 0xd800 && units[i] <= 0xdbff {
			if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", fmt.Errorf("String contained an illegal UTF-16 sequence.")
			}
			i++
		} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
			return "", fmt.Errorf("String contained an illegal UTF-16 sequence.")
		}
	}
	var out strings.Builder
	const digits = "0123456789ABCDEF"
	for _, b := range []byte(string(utf16.Decode(units))) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[b>>4])
			out.WriteByte(digits[b&15])
		}
	}
	return out.String(), nil
}

func QQConnectURL(taskID, source json.RawMessage) (string, error) {
	task, e := qqURIComponent(taskID)
	if e != nil {
		return "", e
	}
	if len(source) == 0 {
		source = encoded("")
	}
	from, e := qqURIComponent(source)
	if e != nil {
		return "", e
	}
	return "https://q.qq.com/qqbot/openclaw/connect.html?task_id=" + task + "&source=" + from + "&_wv=2", nil
}

// Buffer.from(value, 'base64') accepts base64url, missing padding and ignores
// non-alphabet characters. The wire decryptor uses that same byte boundary.
func qqBase64(raw string) []byte {
	var b strings.Builder
	for _, c := range []byte(raw) {
		if c == '=' {
			break
		}
		if c == '-' {
			c = '+'
		}
		if c == '_' {
			c = '/'
		}
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' {
			b.WriteByte(c)
		}
	}
	s := b.String()
	if len(s)%4 == 1 {
		s = s[:len(s)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(s)
	return decoded
}

func QQDecryptSecret(encryptedBase64, keyBase64 string) (string, error) {
	return qqDecryptBytes(qqBase64(encryptedBase64), qqBase64(keyBase64))
}

func qqBufferJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) != 0 && raw[0] == '"' {
		text, e := feishuString(raw)
		return qqBase64(text), e
	}
	var object protocol.JSONObject
	if len(raw) != 0 && raw[0] == '{' && object.UnmarshalJSON(raw) == nil {
		if feishuIsString(object.Get("type"), "Buffer") && len(object.Get("data")) > 0 && object.Get("data")[0] == '[' {
			raw = object.Get("data")
		} else if length := object.Get("length"); len(length) > 0 {
			// Buffer.from(arrayLike) treats a non-number or negative length as
			// empty, while its byte elements use JavaScript ToUint8 coercion.
			if length[0] == '"' || string(length) == "null" || string(length) == "false" || string(length) == "true" {
				return nil, nil
			}
			n, e := strconv.ParseFloat(string(length), 64)
			if e != nil || n <= 0 {
				return nil, nil
			}
		} else {
			raw = nil
		}
	}
	if len(raw) > 0 && (raw[0] == '[' || raw[0] == '{') {
		quoted, _ := json.Marshal(string(raw))
		value, e := goja.New().RunString("new Uint8Array(JSON.parse(" + string(quoted) + "))")
		if e != nil {
			return nil, e
		}
		bytes, ok := value.Export().([]uint8)
		if !ok {
			return nil, fmt.Errorf("QQ byte-array conversion failed")
		}
		return bytes, nil
	}
	received := "an instance of Object"
	if len(raw) == 0 {
		received = "an instance of Object"
	} else if string(raw) == "null" {
		received = "null"
	} else if string(raw) == "false" || string(raw) == "true" {
		received = "type boolean (" + string(raw) + ")"
	} else if raw[0] != '{' {
		text, _ := feishuString(raw)
		received = "type number (" + text + ")"
	}
	return nil, fmt.Errorf("The first argument must be of type string, Buffer, ArrayBuffer, Array, or Array-like Object. Received %s", received)
}

func QQDecryptSecretJSON(encrypted json.RawMessage, keyBase64 string) (string, error) {
	bytes, e := qqBufferJSON(encrypted)
	if e != nil {
		return "", e
	}
	return qqDecryptBytes(bytes, qqBase64(keyBase64))
}

func qqDecryptBytes(encrypted, key []byte) (string, error) {
	if len(encrypted) == 0 {
		return "", fmt.Errorf("Invalid initialization vector")
	}
	if len(key) != 32 {
		return "", fmt.Errorf("Invalid key length")
	}
	// Buffer.subarray converts negative indexes relative to the original length.
	// This matters for malformed short payloads, whose tag overlaps the nonce.
	tagStart := len(encrypted) - 16
	if tagStart < 0 {
		tagStart += len(encrypted)
		if tagStart < 0 {
			tagStart = 0
		}
	}
	tag := encrypted[tagStart:]
	if n := len(tag); n != 4 && n != 8 && (n < 12 || n > 16) {
		return "", fmt.Errorf("Invalid authentication tag length: %d", n)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	nonceSize := min(12, len(encrypted))
	gcm, e := cipher.NewGCMWithNonceSize(block, nonceSize)
	if e != nil {
		return "", e
	}
	if len(encrypted) < 28 {
		// In every short payload the ciphertext subarray is empty. AES-GCM can
		// still authenticate an empty value, including Node's short tag sizes.
		want := gcm.Seal(nil, encrypted[:nonceSize], nil, nil)
		if subtle.ConstantTimeCompare(tag, want[:len(tag)]) == 1 {
			return "", nil
		}
		return "", fmt.Errorf("Unsupported state or unable to authenticate data")
	}
	plain, e := gcm.Open(nil, encrypted[:12], encrypted[12:], nil)
	if e != nil {
		return "", fmt.Errorf("Unsupported state or unable to authenticate data")
	}
	return string(protocol.DecodeJavaScriptUTF8(plain)), nil
}

func qqZero(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "false" || string(raw) == "null" {
		return false
	}
	n, e := strconv.ParseFloat(string(raw), 64)
	return e == nil && n == 0
}

func qqStrictStatus(raw json.RawMessage, status int) bool {
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "false" || string(raw) == "null" {
		return false
	}
	n, e := strconv.ParseFloat(string(raw), 64)
	return e == nil && n == float64(status)
}
