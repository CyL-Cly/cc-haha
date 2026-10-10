package protocol

import (
	"encoding/json"
	"fmt"
)

// NormalizeToolArgumentUTF16 shares the provider's parsed-input contract with
// assistant envelope assembly. Local malformed input remains a validation
// marker; malformed upstream server-tool input becomes the source empty object.
func NormalizeToolArgumentUTF16(units []uint16, local bool) json.RawMessage {
	return normalizeToolArgumentUnits(units, local)
}

// CreateUnparsedToolInput keeps invalid arguments distinguishable from an
// executable empty object, bounded by the same UTF-16 prefix as the TS marker.
func CreateUnparsedToolInput(units []uint16) json.RawMessage {
	preview := units
	if len(preview) > 2048 {
		preview = preview[:2048]
	}
	return json.RawMessage(fmt.Sprintf(`{"__unparsedToolInput":{"raw":%s,"len":%d}}`, quoteUTF16(preview), len(units)))
}
