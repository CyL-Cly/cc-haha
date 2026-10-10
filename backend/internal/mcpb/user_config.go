package mcpb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func truthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	if value[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		return err == nil && len(units) > 0
	}
	if n, err := strconv.ParseFloat(string(value), 64); err == nil {
		return n != 0 && !math.IsNaN(n)
	}
	return true
}
func object(value json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(value)
	return result
}
func text(value json.RawMessage) string {
	raw, err := protocol.JavaScriptStringJSON(value)
	if err != nil {
		return ""
	}
	var result string
	_ = json.Unmarshal(raw, &result)
	return result
}

// ValidateUserConfig preserves the original validation order and JS type
// checks. Defaults are resolved by the manifest config producer afterwards.
func ValidateUserConfig(values, schema protocol.JSONObject) (bool, []string) {
	errors := []string{}
	for _, property := range schema.OwnProperties() {
		var key string
		_ = json.Unmarshal(property.NameJSON, &key)
		field := object(property.Value)
		value := rawProperty(values, property.NameJSON)
		title := key
		if truthy(field.Get("title")) {
			title = text(field.Get("title"))
		}
		missing := value == nil || bytes.Equal(value, []byte(`""`))
		if truthy(field.Get("required")) && missing {
			errors = append(errors, title+" is required but not provided")
			continue
		}
		if missing {
			continue
		}
		isString := len(value) > 0 && value[0] == '"'
		isNumber := len(value) > 0 && (value[0] >= '0' && value[0] <= '9' || value[0] == '-')
		switch field.String("type") {
		case "string":
			if len(value) > 0 && value[0] == '[' {
				if !truthy(field.Get("multiple")) {
					errors = append(errors, title+" must be a string, not an array")
				} else {
					var elements []json.RawMessage
					_ = json.Unmarshal(value, &elements)
					for _, element := range elements {
						if _, err := protocol.DecodeJavaScriptJSONString(element); err != nil {
							errors = append(errors, title+" must be an array of strings")
							break
						}
					}
				}
			} else if !isString {
				errors = append(errors, title+" must be a string")
			}
		case "number":
			if !isNumber {
				errors = append(errors, title+" must be a number")
			}
		case "boolean":
			if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
				errors = append(errors, title+" must be a boolean")
			}
		case "file", "directory":
			if !isString {
				errors = append(errors, title+" must be a path string")
			}
		}
		if field.String("type") == "number" && isNumber {
			n, _ := strconv.ParseFloat(string(value), 64)
			for _, rangeField := range []string{"min", "max"} {
				raw := field.Get(rangeField)
				if raw == nil {
					continue
				}
				bound, _ := strconv.ParseFloat(string(raw), 64)
				if rangeField == "min" && n < bound {
					errors = append(errors, fmt.Sprintf("%s must be at least %s", title, text(raw)))
				} else if rangeField == "max" && n > bound {
					errors = append(errors, fmt.Sprintf("%s must be at most %s", title, text(raw)))
				}
			}
		}
	}
	return len(errors) == 0, errors
}
