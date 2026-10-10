package sessionresume

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"unicode/utf16"
)

// RelativePath follows the pinned Bun native path boundary: target strings
// become UTF-8 (unpaired UTF-16 units become U+FFFD), and relative arguments
// resolve against the actual process cwd, independently of the bootstrap cwd.
// Only displayPath changes; the original filename/path raw field is retained.
func RelativePath(currentCWD, processCWD string, target json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(target)
	if err != nil {
		return nil, err
	}
	base := currentCWD
	if !filepath.IsAbs(base) {
		base = filepath.Join(processCWD, base)
	}
	destination := string(utf16.Decode(units))
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(processCWD, destination)
	}
	result, err := filepath.Rel(base, destination)
	if err != nil {
		return nil, err
	}
	if result == "." {
		result = ""
	}
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(result))), nil
}
