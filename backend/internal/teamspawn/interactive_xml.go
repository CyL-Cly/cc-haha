package teamspawn

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"regexp"
)

var teammateMessagePattern = regexp.MustCompile(`<teammate-message\s+teammate_id="([^"]+)"(?:\s+color="([^"]+)")?(?:\s+summary="([^"]+)")?>\n?([\s\S]*?)\n?<\/teammate-message>`)

// ParseInteractiveTeammateXML is UserTeammateMessage's presentation parser.
// The API renderer remains responsible for the original raw XML content.
func ParseInteractiveTeammateXML(text string) []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	for _, match := range teammateMessagePattern.FindAllStringSubmatch(text, -1) {
		if match[1] == "" || match[4] == "" {
			continue
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "teammateId", Value: quoted(match[1])})
		if match[2] != "" {
			row.Set("color", quoted(match[2]))
		}
		if match[3] != "" {
			row.Set("summary", quoted(match[3]))
		}
		row.Set("content", quoted(protocol.TrimJavaScriptString(match[4])))
		rows = append(rows, row)
	}
	return rows
}
