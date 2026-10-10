package imruntime

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Protobuf acronym fields in whatsmeow retain their original spelling. Baileys
// uses lower-camel names for those same wire fields. Translate at the socket
// boundary, retaining property order for the original media-content selection.
func whatsAppSocketJSON(value json.RawMessage) json.RawMessage {
	o := object(value)
	if o.IsObject() {
		out := protocol.NewJSONObject()
		for _, field := range o.OwnProperties() {
			name := TextJSON(field.NameJSON).String()
			if canonical := map[string]string{"URL": "url", "fileSHA256": "fileSha256", "fileEncSHA256": "fileEncSha256", "JPEGThumbnail": "jpegThumbnail", "PTT": "ptt", "thumbnailSHA256": "thumbnailSha256", "thumbnailEncSHA256": "thumbnailEncSha256", "GIFPlayback": "gifPlayback", "remoteJID": "remoteJid"}[name]; canonical != "" {
				name = canonical
			}
			out.Set(name, whatsAppSocketJSON(field.Value))
		}
		return raw(out)
	}
	var array []json.RawMessage
	if len(value) > 0 && value[0] == '[' && json.Unmarshal(value, &array) == nil {
		for i := range array {
			array[i] = whatsAppSocketJSON(array[i])
		}
		return raw(array)
	}
	return value
}
