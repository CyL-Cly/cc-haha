// Package markdownfrontmatter parses the original Markdown configuration
// format natively. JSON and JavaScript coercion remain separate, including
// nonfinite YAML numbers used in Skill version/description metadata.
package markdownfrontmatter

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"go.yaml.in/yaml/v3"
)

var delimiter = regexp.MustCompile(`(?m)^---[\t ]*\r?\n([\s\S]*?)^---[\t ]*(?:\r?\n[\t \r\n]*|$)`)
var simpleKey = regexp.MustCompile(`^([a-zA-Z_-]+):\s+(.+)$`)
var special = regexp.MustCompile("[{}\\[\\]*&#!|>%@`]|: ")
var decimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var hexadecimal = regexp.MustCompile(`^[+-]?0[xX][0-9a-fA-F]+$`)
var octal = regexp.MustCompile(`^[+-]?0[oO][0-7]+$`)

type Property struct {
	Key    string `json:"key"`
	Type   string `json:"type"`
	Truthy bool   `json:"truthy"`
	String string `json:"string"`
}
type Parsed struct {
	Frontmatter protocol.JSONObject `json:"frontmatter"`
	Content     string              `json:"-"`
	ContentJSON json.RawMessage     `json:"content"`
	Properties  []Property          `json:"properties"`
	values      map[string]*Value
}
type Value struct {
	Kind    string
	text    string
	number  float64
	boolean bool
	array   []*Value
	fields  map[string]*Value
	order   []string
}

func (v *Value) Truthy() bool {
	if v == nil || v.Kind == "null" {
		return false
	}
	switch v.Kind {
	case "string":
		return v.text != ""
	case "boolean":
		return v.boolean
	case "number":
		return v.number != 0 && !math.IsNaN(v.number)
	}
	return true
}
func (v *Value) String() string {
	if v == nil {
		return "undefined"
	}
	switch v.Kind {
	case "string":
		return v.text
	case "null":
		return "null"
	case "boolean":
		return strconv.FormatBool(v.boolean)
	case "number":
		if math.IsNaN(v.number) {
			return "NaN"
		}
		if math.IsInf(v.number, 1) {
			return "Infinity"
		}
		if math.IsInf(v.number, -1) {
			return "-Infinity"
		}
		raw, _ := v.JSON()
		text, _ := protocol.JavaScriptStringJSON(raw)
		var result string
		_ = json.Unmarshal(text, &result)
		return result
	case "array":
		values := make([]string, len(v.array))
		for i, item := range v.array {
			if item != nil && item.Kind != "null" {
				values[i] = item.String()
			}
		}
		return strings.Join(values, ",")
	}
	return "[object Object]"
}
func (v *Value) JSON() (json.RawMessage, error) { return v.json(map[*Value]bool{}) }
func (v *Value) json(active map[*Value]bool) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	switch v.Kind {
	case "null":
		return json.RawMessage("null"), nil
	case "string":
		return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(v.text))), nil
	case "boolean":
		return json.RawMessage(strconv.FormatBool(v.boolean)), nil
	case "number":
		if math.IsInf(v.number, 0) || math.IsNaN(v.number) {
			return json.RawMessage("null"), nil
		}
		raw := []byte(strconv.FormatFloat(v.number, 'g', -1, 64))
		return protocol.StringifyValidatedJavaScriptJSON(raw), nil
	}
	if active[v] {
		return nil, errors.New("Converting circular structure to JSON")
	}
	active[v] = true
	defer delete(active, v)
	if v.Kind == "array" {
		values := []json.RawMessage{}
		for _, item := range v.array {
			raw, e := item.json(active)
			if e != nil {
				return nil, e
			}
			values = append(values, raw)
		}
		return json.Marshal(values)
	}
	object := protocol.NewJSONObject()
	for _, key := range v.order {
		raw, e := v.fields[key].json(active)
		if e != nil {
			return nil, e
		}
		object.Set(key, raw)
	}
	return object.MarshalJSON()
}
func (p Parsed) Get(name string) *Value { return p.values[name] }

// Parse retains the reference's delimiter/failed-YAML fallback, including its
// slice-by-match-length behavior when a frontmatter block follows a preface.
func Parse(markdown string) (Parsed, error) {
	result := Parsed{Frontmatter: protocol.NewJSONObject(), Content: markdown, ContentJSON: protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(markdown))), Properties: []Property{}, values: map[string]*Value{}}
	match := delimiter.FindStringSubmatchIndex(markdown)
	if match == nil {
		return result, nil
	}
	units := utf16.Encode([]rune(markdown))
	size := len(utf16.Encode([]rune(markdown[match[0]:match[1]])))
	result.ContentJSON = protocol.QuoteJavaScriptUTF16(units[size:])
	result.Content = string(utf16.Decode(units[size:]))
	text := markdown[match[2]:match[3]]
	value, err := parseYAML(text)
	if err != nil {
		value, err = parseYAML(quoteProblematicValues(text))
	}
	if err != nil || value == nil || value.Kind != "object" {
		return result, nil
	}
	raw, err := value.JSON()
	if err != nil {
		return result, err
	}
	if err = result.Frontmatter.UnmarshalJSON(raw); err != nil {
		return result, err
	}
	result.values = value.fields
	for _, field := range result.Frontmatter.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		v := value.fields[key]
		result.Properties = append(result.Properties, Property{key, v.Kind, v.Truthy(), v.String()})
	}
	return result, nil
}
func quoteProblematicValues(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		m := simpleKey.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := m[2]
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			continue
		}
		if special.MatchString(v) {
			v = strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`)
			lines[i] = m[1] + `: "` + v + `"`
		}
	}
	return strings.Join(lines, "\n")
}
func parseYAML(text string) (*Value, error) {
	decoder := yaml.NewDecoder(strings.NewReader(text))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return &Value{Kind: "null"}, nil
		}
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple YAML documents")
	}
	if len(root.Content) == 0 {
		return &Value{Kind: "null"}, nil
	}
	return yamlValue(root.Content[0], map[*yaml.Node]*Value{}, map[*yaml.Node]bool{})
}
func yamlValue(node *yaml.Node, cache map[*yaml.Node]*Value, active map[*yaml.Node]bool) (*Value, error) {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if active[node] {
		return nil, errors.New("recursive YAML alias")
	}
	if value := cache[node]; value != nil {
		return value, nil
	}
	active[node] = true
	defer delete(active, node)
	value := &Value{}
	cache[node] = value
	switch node.Kind {
	case yaml.ScalarNode:
		value.Kind, value.text = "string", node.Value
		if node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 || node.Tag == "!!str" && node.Style&yaml.TaggedStyle != 0 {
			return value, nil
		}
		lower := strings.ToLower(node.Value)
		switch lower {
		case "", "null", "~":
			value.Kind = "null"
		case "true", "false":
			value.Kind, value.boolean = "boolean", lower == "true"
		case ".inf", "+.inf", "-.inf":
			value.Kind, value.number = "number", math.Inf(1)
			if lower == "-.inf" {
				value.number = math.Inf(-1)
			}
		case ".nan":
			value.Kind, value.number = "number", math.NaN()
		default:
			if decimal.MatchString(node.Value) {
				number, e := strconv.ParseFloat(node.Value, 64)
				if e == nil || math.IsInf(number, 0) {
					value.Kind, value.number = "number", number
				}
			}
			if hexadecimal.MatchString(node.Value) || octal.MatchString(node.Value) {
				text := node.Value
				sign := 1.0
				if text[0] == '-' {
					sign = -1
					text = text[1:]
				} else if text[0] == '+' {
					text = text[1:]
				}
				base := 16
				if text[1] == 'o' || text[1] == 'O' {
					base = 8
				}
				n, err := strconv.ParseUint(text[2:], base, 64)
				if err != nil {
					return value, nil
				}
				value.Kind, value.number = "number", sign*float64(n)
			}
		}
	case yaml.SequenceNode:
		value.Kind = "array"
		for _, child := range node.Content {
			item, e := yamlValue(child, cache, active)
			if e != nil {
				return nil, e
			}
			value.array = append(value.array, item)
		}
	case yaml.MappingNode:
		value.Kind, value.fields = "object", map[string]*Value{}
		explicit := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key, e := yamlValue(node.Content[i], cache, active)
			if e != nil {
				return nil, e
			}
			if node.Content[i].Tag != "!!merge" {
				explicit[key.String()] = true
			}
		}
		set := func(key string, v *Value) {
			if _, ok := value.fields[key]; !ok {
				value.order = append(value.order, key)
			}
			value.fields[key] = v
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, e := yamlValue(node.Content[i], cache, active)
			if e != nil {
				return nil, e
			}
			item, e := yamlValue(node.Content[i+1], cache, active)
			if e != nil {
				return nil, e
			}
			if node.Content[i].Tag == "!!merge" {
				items := []*Value{item}
				if item.Kind == "array" {
					items = item.array
				}
				for _, parent := range items {
					if parent.Kind != "object" {
						return nil, errors.New("invalid YAML merge")
					}
					for _, k := range parent.order {
						if _, ok := value.fields[k]; !ok && !explicit[k] {
							set(k, parent.fields[k])
						}
					}
				}
			} else {
				set(key.String(), item)
			}
		}
	default:
		return nil, errors.New("invalid YAML node")
	}
	return value, nil
}
