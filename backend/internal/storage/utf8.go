package storage

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// DecodeUTF8 matches Node's replacement decoding without changing JSON shape.
func DecodeUTF8(data []byte) []byte { return decodeUTF8(data) }

func decodeUTF8(data []byte) []byte { return protocol.DecodeJavaScriptUTF8(data) }

func jsWhitespace(character rune) bool {
	return character >= '\t' && character <= '\r' ||
		character == ' ' || character == '\u00a0' || character == '\u1680' ||
		character >= '\u2000' && character <= '\u200a' ||
		character == '\u2028' || character == '\u2029' || character == '\u202f' ||
		character == '\u205f' || character == '\u3000' || character == '\ufeff'
}
