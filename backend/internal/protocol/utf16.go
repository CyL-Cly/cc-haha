package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
)

// DecodeJavaScriptJSONString retains the UTF-16 code units of a JSON string,
// including lone surrogates. It reuses the protocol's JS string decoder.
func DecodeJavaScriptJSONString(raw []byte) ([]uint16, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' || !json.Valid(raw) {
		return nil, errors.New("UTF-16 decoding requires a valid JSON string")
	}
	return decodeJSString(raw), nil
}

// QuoteJavaScriptUTF16 uses JSON.stringify's existing well-formed quoting.
func QuoteJavaScriptUTF16(units []uint16) []byte { return []byte(quoteUTF16(units)) }
