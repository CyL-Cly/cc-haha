package sdkhost

import (
	"encoding/json"
	"slices"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// QueryEngine acknowledges a non-sensitive local command before awaiting its
// work. The server uses this input marker to admit output after replacing a
// stopped turn; status and completion alone cannot acknowledge replacement.
func localDispatchContent(content json.RawMessage, commands []ControlCommand, skipSlash bool) json.RawMessage {
	if skipSlash {
		return nil
	}
	units, err := protocol.DecodeJavaScriptJSONString(content)
	if err != nil {
		var blocks []protocol.JSONObject
		if json.Unmarshal(content, &blocks) != nil || len(blocks) == 0 || blocks[len(blocks)-1].String("type") != "text" {
			return nil
		}
		units, err = protocol.DecodeJavaScriptJSONString(blocks[len(blocks)-1].Get("text"))
	}
	if err != nil || len(units) == 0 || units[0] != '/' {
		return nil
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	words := [][]uint16{{}}
	for _, unit := range units[1:] {
		if unit == ' ' {
			words = append(words, nil)
		} else {
			words[len(words)-1] = append(words[len(words)-1], unit)
		}
	}
	name := string(utf16.Decode(words[0]))
	if name == "" {
		return nil
	}
	start := 1
	if len(words) > 1 && string(utf16.Decode(words[1])) == "(MCP)" {
		name += " (MCP)"
		start = 2
	}
	args := []uint16{}
	for index := start; index < len(words); index++ {
		if index != start {
			args = append(args, ' ')
		}
		args = append(args, words[index]...)
	}
	for _, command := range commands {
		if (command.Name == name || slices.Contains(command.Aliases, name)) && command.Kind == "local" && !command.Sensitive {
			tagged, _ := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("<command-name>/" + name + "</command-name>\n            <command-message>" + name + "</command-message>\n            <command-args>"), protocol.QuoteJavaScriptUTF16(args), quote("</command-args>")}, "")
			return tagged
		}
	}
	return nil
}

func (h *Host) acknowledgeNativeLocalDispatch(scope *cooperative.Scope, content, uuid json.RawMessage, skipSlash bool) error {
	tagged := localDispatchContent(content, h.options.Print.Registry.ControlCommands(), skipSlash)
	if tagged == nil {
		return nil
	}
	if len(uuid) == 0 || string(uuid) == "null" {
		uuid = quote(h.options.UUID())
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("local_command")}, protocol.JSONField{Name: "content", Value: tagged}, protocol.JSONField{Name: "uuid", Value: uuid}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())})
	_, err := h.enqueue(scope, message)
	return err
}
