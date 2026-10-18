package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Go strings can name every ordinary field. A lone UTF-16 code unit needs a
// private dialog binding, while the captured schema keeps its original key.
// Avoid ordinary schema names too: channel keys may contain the same prefix.
func terminalPluginFieldBinding(schema protocol.JSONObject, nameJSON json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(nameJSON)
	unpaired := false
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff {
				index++
			} else {
				unpaired = true
			}
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			unpaired = true
		}
	}
	if !unpaired {
		var name string
		_ = json.Unmarshal(nameJSON, &name)
		return name
	}
	var encoded strings.Builder
	encoded.WriteString("$plugin-option:")
	for _, unit := range units {
		_, _ = fmt.Fprintf(&encoded, "%04x", unit)
	}
	name := encoded.String()
	for {
		value, _ := schema.GetPropertyJSON(raw(name))
		if value == nil {
			return name
		}
		name = "$" + name
	}
}

func terminalPluginBoundValues(step protocol.JSONObject, fields map[string]string, encoded map[string]json.RawMessage) protocol.JSONObject {
	schema, present := obj(step.Get("schema")), obj(step.Get("savedSensitive"))
	values := protocol.NewJSONObject()
	for _, property := range schema.OwnProperties() {
		binding := terminalPluginFieldBinding(schema, property.NameJSON)
		input := encoded[binding]
		units, err := protocol.DecodeJavaScriptJSONString(input)
		if err != nil {
			input = raw(fields[binding])
			units, _ = protocol.DecodeJavaScriptJSONString(input)
		}
		field := obj(property.Value)
		saved, _ := present.GetPropertyJSON(property.NameJSON)
		if string(field.Get("sensitive")) == "true" && len(units) == 0 && string(saved) == "true" {
			continue
		}
		var text string
		_ = json.Unmarshal(input, &text)
		value := input
		switch field.String("type") {
		case "number":
			if protocol.TrimJavaScriptString(text) == "" {
				continue
			}
			value = terminalPluginNumberValue(text)
			if len(value) > 0 && value[0] == '"' {
				value = input
			}
		case "boolean":
			flag := false
			switch strings.ToLower(protocol.TrimJavaScriptString(text)) {
			case "1", "true", "yes", "on":
				flag = true
			}
			value = raw(flag)
		}
		_ = values.SetPropertyJSON(property.NameJSON, value)
	}
	return values
}
