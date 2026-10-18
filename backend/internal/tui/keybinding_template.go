package tui

import (
	"bytes"
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func KeybindingsTemplate(platform string, terminalVT bool, features KeybindingFeatures) (string, error) {
	blocks := [][]byte{}
	contexts := []string{}
	groups := map[string]*protocol.JSONObject{}
	defaultKeybindingEntries(platform, terminalVT, features, func(context string, entries ...string) {
		bindings := groups[context]
		if bindings == nil {
			value := protocol.NewJSONObject()
			bindings = &value
			groups[context] = bindings
			contexts = append(contexts, context)
		}
		for i := 0; i < len(entries); i += 2 {
			// Source compares only its three hardcoded NON_REBINDABLE entries.
			if entries[i] == "ctrl+c" || entries[i] == "ctrl+d" || entries[i] == "ctrl+m" {
				continue
			}
			bindings.Set(entries[i], protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(entries[i+1]))))
		}
	})
	for _, context := range contexts {
		bindings := groups[context]
		if len(bindings.OwnProperties()) == 0 {
			continue
		}
		bindingJSON, _ := bindings.MarshalJSON()
		block := protocol.NewJSONObject(protocol.JSONField{Name: "context", Value: protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(context)))}, protocol.JSONField{Name: "bindings", Value: bindingJSON})
		blockJSON, _ := block.MarshalJSON()
		blocks = append(blocks, blockJSON)
	}
	array := append(append([]byte{'['}, bytes.Join(blocks, []byte{','})...), ']')
	value := protocol.NewJSONObject(protocol.JSONField{Name: "$schema", Value: json.RawMessage(`"https://www.schemastore.org/claude-code-keybindings.json"`)}, protocol.JSONField{Name: "$docs", Value: json.RawMessage(`"https://code.claude.com/docs/en/keybindings"`)}, protocol.JSONField{Name: "bindings", Value: array})
	raw, err := value.MarshalJSON()
	if err != nil {
		return "", err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		return "", err
	}
	return pretty.String() + "\n", nil
}
