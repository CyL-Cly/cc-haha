// Package projectpath retains the pinned Source sanitizePath boundary. Length
// and replacement use UTF-16 units; the hash uses the original UTF-8 byte view.
package projectpath

import (
	"encoding/json"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxSanitizedLength = 200

// Sanitize receives an original Go host string. Native bootstrap callers have
// already applied their own NFC setter policy. This function does not normalize.
func Sanitize(name string) string { return SanitizeUTF16(utf16.Encode([]rune(name))) }

// SanitizeJSON uses the existing protocol decoder, so an original JSON string's
// lone surrogate survives sanitization even though its UTF-8 hash uses U+FFFD.
func SanitizeJSON(raw json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	return SanitizeUTF16(units), nil
}

// SanitizeUTF16 never changes the caller's units. The Source regex has no u flag:
// a paired astral character therefore produces two replacement hyphens.
func SanitizeUTF16(units []uint16) string {
	output := make([]byte, len(units))
	for index, unit := range units {
		output[index] = '-'
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' {
			output[index] = byte(unit)
		}
	}
	if len(output) <= MaxSanitizedLength {
		return string(output)
	}
	// utf16.Decode replaces each unpaired unit while joining valid pairs. This
	// produces the same original string byte view sampled by pinned Bun.hash.
	bytes := []byte(string(utf16.Decode(units)))
	return string(output[:MaxSanitizedLength]) + "-" + strconv.FormatUint(wyhash(0, bytes), 36)
}
