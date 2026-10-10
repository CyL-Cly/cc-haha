package workflowcommands

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/javascriptparse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxScriptBytes = 524288
const firstStatementError = "`export const meta = { name, description, phases }` must be the FIRST statement in the script"
const plainJSHint = "Workflow scripts must be plain JavaScript — common causes are TypeScript syntax (type annotations, interfaces, generics) and broken string quoting or escaping."

type ParsedScript struct {
	Meta                         protocol.JSONObject
	ScriptBody                   string
	ScriptBodyUnits              []uint16
	StartByte, EndByte, EndUTF16 int
	Error                        string
	ErrorUnits                   []uint16
}

// Metadata owns a pure-language parser on the host's existing owner. Workflow
// validation, literal evaluation, source slicing and diagnostics remain Go.
type Metadata struct {
	owner  *cooperative.Owner
	parser *javascriptparse.Parser
}

func NewMetadata(scope *cooperative.Scope, owner *cooperative.Owner) (*Metadata, error) {
	parser, err := javascriptparse.New(scope, owner)
	if err != nil {
		return nil, err
	}
	return &Metadata{owner, parser}, nil
}
func (m *Metadata) check(scope *cooperative.Scope) error {
	if m == nil || !scope.BelongsTo(m.owner) {
		return cooperative.ErrRestricted
	}
	return nil
}
func (m *Metadata) ParseScript(scope *cooperative.Scope, script string) (ParsedScript, error) {
	if !utf8.ValidString(script) {
		return ParsedScript{}, &CapabilityError{"native UTF8 script decoder required"}
	}
	return m.Parse(scope, utf16.Encode([]rune(script)))
}
func units(s string) []uint16 { return utf16.Encode([]rune(s)) }
func text(u []uint16) string  { return string(utf16.Decode(u)) }
func utf8Length(u []uint16) int {
	n := 0
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c < 128:
			n++
		case c < 2048:
			n += 2
		case c >= 0xd800 && c <= 0xdbff && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] <= 0xdfff:
			n += 4
			i++
		default:
			n += 3
		}
	}
	return n
}

// ScriptByteLength matches the pinned Bun1.3.14 UTF16 SIMD byte-length path.
// It counts each surrogate unit as two bytes, including unpaired units; this is
// a budget observation, not UTF8 encoding or source-position conversion.
func ScriptByteLength(u []uint16) int {
	n := 0
	for _, c := range u {
		switch {
		case c < 128:
			n++
		case c < 2048:
			n += 2
		case c >= 0xd800 && c <= 0xdfff:
			n += 2
		default:
			n += 3
		}
	}
	return n
}
func (r *ParsedScript) fail(u []uint16) {
	r.ErrorUnits = append([]uint16(nil), u...)
	r.Error = text(u)
}
func (r ParsedScript) ResultJSON() json.RawMessage {
	out := protocol.NewJSONObject()
	if r.ErrorUnits != nil {
		out.Set("error", protocol.QuoteJavaScriptUTF16(r.ErrorUnits))
	} else {
		raw, _ := r.Meta.MarshalJSON()
		out.Set("meta", raw)
		out.Set("scriptBody", protocol.QuoteJavaScriptUTF16(r.ScriptBodyUnits))
	}
	raw, _ := out.MarshalJSON()
	return raw
}
func astGet(v *javascriptparse.Value, key string) *javascriptparse.Value {
	if v == nil {
		return nil
	}
	for _, p := range v.Properties {
		if text(p.Key) == key {
			return p.Value
		}
	}
	return nil
}
func astString(v *javascriptparse.Value) string {
	if v == nil || v.Kind != "string" {
		return ""
	}
	return text(v.Units)
}
func astNumber(v *javascriptparse.Value) int {
	if v == nil || v.Kind != "number" {
		return -1
	}
	n, e := strconv.Atoi(v.Text)
	if e != nil {
		return -1
	}
	return n
}
func astBool(v *javascriptparse.Value) bool { return v != nil && v.Bool != nil && *v.Bool }
func astArray(v *javascriptparse.Value) []*javascriptparse.Value {
	if v == nil || v.Kind != "array" {
		return nil
	}
	n := astNumber(astGet(v, "length"))
	out := make([]*javascriptparse.Value, n)
	for i := range out {
		out[i] = astGet(v, strconv.Itoa(i))
	}
	return out
}
func nodeType(v *javascriptparse.Value) string { return astString(astGet(v, "type")) }
func metaInitializer(ast *javascriptparse.Value) *javascriptparse.Value {
	body := astArray(astGet(ast, "body"))
	if len(body) == 0 || nodeType(body[0]) != "ExportNamedDeclaration" {
		return nil
	}
	d := astGet(body[0], "declaration")
	if nodeType(d) != "VariableDeclaration" || astString(astGet(d, "kind")) != "const" {
		return nil
	}
	decls := astArray(astGet(d, "declarations"))
	if len(decls) != 1 {
		return nil
	}
	id := astGet(decls[0], "id")
	init := astGet(decls[0], "init")
	if nodeType(id) != "Identifier" || astString(astGet(id, "name")) != "meta" || nodeType(init) != "ObjectExpression" {
		return nil
	}
	return init
}
func (m *Metadata) Parse(scope *cooperative.Scope, script []uint16) (ParsedScript, error) {
	r := ParsedScript{}
	if e := m.check(scope); e != nil {
		return r, e
	}
	if ScriptByteLength(script) > MaxScriptBytes {
		r.fail(units(fmt.Sprintf("Script exceeds %d bytes", MaxScriptBytes)))
		return r, nil
	}
	parsed, e := m.parser.Parse(scope, script)
	if e != nil {
		return r, e
	}
	if !parsed.Accepted {
		r.fail(formatParseError(parsed.Failure, script))
		return r, nil
	}
	init := metaInitializer(parsed.AST)
	if init == nil {
		r.fail(units(firstStatementError))
		return r, nil
	}
	raw, e := evaluateObject(init)
	if e != nil {
		r.fail(units("meta must be a pure literal: " + e.Error()))
		return r, nil
	}
	meta, validation := validateMeta(raw)
	if validation != nil {
		r.fail(validation)
		return r, nil
	}
	first := astArray(astGet(parsed.AST, "body"))[0]
	start, end := astNumber(astGet(first, "start")), astNumber(astGet(first, "end"))
	if start < 0 || end < start || end > len(script) {
		return r, &CapabilityError{"invalid Acorn original statement span"}
	}
	body := script[end:]
	lastLF := -1
	for i, c := range body {
		if c != ';' && !jsWhitespace(c) {
			break
		}
		if c == '\n' {
			lastLF = i
		}
	}
	if lastLF >= 0 {
		body = body[lastLF+1:]
	}
	for len(body) > 0 && jsWhitespace(body[0]) {
		body = body[1:]
	}
	r.Meta = meta
	r.ScriptBodyUnits = append([]uint16{}, body...)
	r.ScriptBody = text(body)
	r.StartByte = utf8Length(script[:start])
	r.EndByte = utf8Length(script[:end])
	r.EndUTF16 = end
	return r, nil
}
func formatParseError(f *javascriptparse.Failure, script []uint16) []uint16 {
	message := []uint16{}
	if f != nil {
		message = f.Message
	}
	prefix := append(units("Script parse error: "), message...)
	lineNo, column := -1, -1
	if f != nil {
		lineNo = astNumber(astGet(f.Loc, "line"))
		column = astNumber(astGet(f.Loc, "column"))
	}
	lines := [][]uint16{{}}
	for _, c := range script {
		if c == '\n' {
			lines = append(lines, []uint16{})
		} else {
			lines[len(lines)-1] = append(lines[len(lines)-1], c)
		}
	}
	if lineNo < 1 || lineNo > len(lines) || column < 0 {
		return append(prefix, units(". "+plainJSHint)...)
	}
	line := lines[lineNo-1]
	if column > len(line) {
		column = len(line)
	}
	start := column - 40
	if max := len(line) - 80; start > max {
		start = max
	}
	if start < 0 {
		start = 0
	}
	end := start + 80
	if end > len(line) {
		end = len(line)
	}
	out := append(prefix, '\n')
	out = append(out, line[start:end]...)
	out = append(out, '\n')
	out = append(out, units(strings.Repeat(" ", column-start)+"^\n"+plainJSHint)...)
	return out
}

type literal struct {
	kind    string
	str     []uint16
	number  float64
	boolean bool
	bigint  *big.Int
	object  *literalObject
	array   []*literal
}
type literalObject struct {
	fields  map[string]*literal
	proto   *literalObject
	builtin bool
}

var objectPrototype = &literalObject{builtin: true}

func newLiteralObject() *literalObject {
	return &literalObject{fields: map[string]*literal{}, proto: objectPrototype}
}
func propertyKey(u []uint16) string {
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	return string(b)
}
func (o *literalObject) get(name []uint16) *literal {
	for ; o != nil; o = o.proto {
		if value, ok := o.fields[propertyKey(name)]; ok {
			return value
		}
		if o.builtin {
			if text(name) == "__proto__" {
				return &literal{kind: "object", object: objectPrototype}
			}
			switch text(name) {
			case "constructor", "toString", "valueOf", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__":
				return &literal{kind: "function"}
			}
		}
	}
	return &literal{kind: "undefined"}
}
func (o *literalObject) set(name []uint16, value *literal) {
	key := propertyKey(name)
	if text(name) == "__proto__" {
		for p := o; p != nil; p = p.proto {
			if _, own := p.fields[key]; own {
				break
			}
			if p.builtin {
				if value.kind == "object" {
					o.proto = value.object
				} else if value.kind == "null" {
					o.proto = nil
				}
				return
			}
		}
	}
	o.fields[key] = value
}
func evaluateObject(node *javascriptparse.Value) (*literal, error) {
	out := newLiteralObject()
	for _, p := range astArray(astGet(node, "properties")) {
		if nodeType(p) == "SpreadElement" {
			return nil, fmt.Errorf("spread not allowed in meta")
		}
		if astString(astGet(p, "kind")) != "init" || astBool(astGet(p, "method")) {
			return nil, fmt.Errorf("only plain key: value pairs are allowed in meta")
		}
		if astBool(astGet(p, "computed")) {
			return nil, fmt.Errorf("computed keys not allowed in meta")
		}
		key := astGet(p, "key")
		var name []uint16
		switch nodeType(key) {
		case "Identifier":
			name = astGet(key, "name").Units
		case "Literal":
			name = literalString(literalValue(astGet(key, "value")))
		default:
			return nil, fmt.Errorf("unsupported key in meta")
		}
		value, e := evaluateLiteral(astGet(p, "value"))
		if e != nil {
			return nil, e
		}
		out.set(name, value)
	}
	return &literal{kind: "object", object: out}, nil
}
func literalValue(v *javascriptparse.Value) *literal {
	if v == nil {
		return &literal{kind: "undefined"}
	}
	r := &literal{kind: v.Kind, str: append([]uint16(nil), v.Units...)}
	switch v.Kind {
	case "boolean":
		r.boolean = v.Bool != nil && *v.Bool
	case "number":
		r.number, _ = strconv.ParseFloat(v.Text, 64)
	case "bigint":
		r.bigint = new(big.Int)
		r.bigint.SetString(v.Text, 10)
	case "regexp":
		r.str = append(append(append([]uint16{'/'}, v.Pattern...), '/'), v.Flags...)
	}
	return r
}
func evaluateLiteral(node *javascriptparse.Value) (*literal, error) {
	switch nodeType(node) {
	case "Literal":
		return literalValue(astGet(node, "value")), nil
	case "ObjectExpression":
		return evaluateObject(node)
	case "ArrayExpression":
		r := &literal{kind: "array", array: []*literal{}}
		for _, entry := range astArray(astGet(node, "elements")) {
			if entry == nil || entry.Kind == "null" {
				return nil, fmt.Errorf("sparse arrays not allowed")
			}
			if nodeType(entry) == "SpreadElement" {
				return nil, fmt.Errorf("spread not allowed in meta")
			}
			value, e := evaluateLiteral(entry)
			if e != nil {
				return nil, e
			}
			r.array = append(r.array, value)
		}
		return r, nil
	case "TemplateLiteral":
		if len(astArray(astGet(node, "expressions"))) > 0 {
			return nil, fmt.Errorf("template interpolation not allowed in meta")
		}
		out := []uint16{}
		for _, q := range astArray(astGet(node, "quasis")) {
			v := astGet(astGet(q, "value"), "cooked")
			if v != nil && v.Kind != "null" && v.Kind != "undefined" {
				out = append(out, v.Units...)
			}
		}
		return &literal{kind: "string", str: out}, nil
	case "UnaryExpression":
		op := astString(astGet(node, "operator"))
		arg := astGet(node, "argument")
		if op != "-" || nodeType(arg) != "Literal" {
			return nil, fmt.Errorf("unsupported expression: %s", op)
		}
		value := literalValue(astGet(arg, "value"))
		if value.kind == "bigint" {
			return &literal{kind: "bigint", bigint: new(big.Int).Neg(value.bigint)}, nil
		}
		return &literal{kind: "number", number: -literalNumber(value)}, nil
	default:
		return nil, fmt.Errorf("unsupported expression: %s", nodeType(node))
	}
}
func literalString(v *literal) []uint16 {
	switch v.kind {
	case "string", "regexp":
		return v.str
	case "null":
		return units("null")
	case "undefined":
		return units("undefined")
	case "boolean":
		return units(strconv.FormatBool(v.boolean))
	case "bigint":
		return units(v.bigint.String())
	case "number":
		if math.IsNaN(v.number) {
			return units("NaN")
		}
		if math.IsInf(v.number, 1) {
			return units("Infinity")
		}
		if math.IsInf(v.number, -1) {
			return units("-Infinity")
		}
		if v.number == 0 {
			return units("0")
		}
		return units(string(protocol.StringifyValidatedJavaScriptJSON([]byte(strconv.FormatFloat(v.number, 'g', -1, 64)))))
	}
	return units("[object Object]")
}
func literalNumber(v *literal) float64 {
	switch v.kind {
	case "number":
		return v.number
	case "null":
		return 0
	case "boolean":
		if v.boolean {
			return 1
		}
		return 0
	case "string":
		s := text(trimUnits(v.str))
		if s == "" {
			return 0
		}
		switch s {
		case "Infinity", "+Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		if len(s) > 2 && s[0] == '0' {
			base := 0
			switch s[1] {
			case 'x', 'X':
				base = 16
			case 'b', 'B':
				base = 2
			case 'o', 'O':
				base = 8
			}
			if base > 0 {
				value, ok := new(big.Int).SetString(s[2:], base)
				if !ok {
					return math.NaN()
				}
				f, _ := new(big.Float).SetInt(value).Float64()
				return f
			}
		}
		value, e := strconv.ParseFloat(s, 64)
		if e != nil && !math.IsInf(value, 0) {
			return math.NaN()
		}
		return value
	}
	return math.NaN()
}
func validateName(name *literal) []uint16 {
	if name.kind != "string" || len(trimUnits(name.str)) == 0 {
		return units("meta.name must be a non-empty string")
	}
	if !safeName.MatchString(stringUTF16ASCII(name.str)) {
		out := append(units("meta.name '"), name.str...)
		return append(out, units("' must start with a letter or digit and contain only letters, digits, '-' and '_'")...)
	}
	return nil
}
func validateMeta(value *literal) (protocol.JSONObject, []uint16) {
	out := protocol.NewJSONObject()
	if value.kind != "object" {
		return out, units("meta must be an object literal")
	}
	get := func(name string) *literal { return value.object.get(units(name)) }
	name := get("name")
	if e := validateName(name); e != nil {
		return out, e
	}
	description := get("description")
	if description.kind != "string" || len(trimUnits(description.str)) == 0 {
		return out, units("meta.description must be a non-empty string")
	}
	out.Set("name", protocol.QuoteJavaScriptUTF16(name.str))
	out.Set("description", protocol.QuoteJavaScriptUTF16(description.str))
	for _, key := range []string{"whenToUse", "title", "model"} {
		v := get(key)
		if v.kind == "string" {
			out.Set(key, protocol.QuoteJavaScriptUTF16(v.str))
		}
	}
	if phases := get("phases"); phases.kind != "undefined" {
		if phases.kind != "array" {
			return out, units("meta.phases must be an array")
		}
		raw := []byte{'['}
		for i, entry := range phases.array {
			if entry.kind != "object" && entry.kind != "array" && entry.kind != "regexp" {
				return out, units("each meta.phases entry must be an object")
			}
			pget := func(key string) *literal {
				if entry.kind == "object" {
					return entry.object.get(units(key))
				}
				return &literal{kind: "undefined"}
			}
			title := pget("title")
			if title.kind != "string" || len(trimUnits(title.str)) == 0 {
				return out, units("each meta.phases entry needs a non-empty title")
			}
			p := protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: protocol.QuoteJavaScriptUTF16(title.str)})
			for _, key := range []string{"detail", "model"} {
				v := pget(key)
				if v.kind == "string" {
					p.Set(key, protocol.QuoteJavaScriptUTF16(v.str))
				}
			}
			b, _ := p.MarshalJSON()
			if i > 0 {
				raw = append(raw, ',')
			}
			raw = append(raw, b...)
		}
		out.Set("phases", append(raw, ']'))
	}
	return out, nil
}
func (m *Metadata) Rename(scope *cooperative.Scope, script, name []uint16) ([]uint16, ParsedScript, error) {
	parsed, e := m.Parse(scope, script)
	if e != nil || parsed.ErrorUnits != nil {
		return nil, parsed, e
	}
	if error := validateName(&literal{kind: "string", str: name}); error != nil {
		parsed = ParsedScript{}
		parsed.fail(error)
		return nil, parsed, nil
	}
	ast, e := m.parser.Parse(scope, script)
	if e != nil {
		return nil, ParsedScript{}, e
	}
	init := metaInitializer(ast.AST)
	var target *javascriptparse.Value
	for _, p := range astArray(astGet(init, "properties")) {
		if nodeType(p) != "Property" || astBool(astGet(p, "computed")) {
			continue
		}
		key := astGet(p, "key")
		if nodeType(key) == "Identifier" && astString(astGet(key, "name")) == "name" || nodeType(key) == "Literal" && astGet(key, "value").Kind == "string" && astString(astGet(key, "value")) == "name" {
			target = astGet(p, "value")
		}
	}
	if target == nil {
		parsed = ParsedScript{}
		parsed.fail(units("meta.name must be a non-empty string"))
		return nil, parsed, nil
	}
	start, end := astNumber(astGet(target, "start")), astNumber(astGet(target, "end"))
	if start < 0 || end < start || end > len(script) {
		return nil, ParsedScript{}, &CapabilityError{"invalid rename value span"}
	}
	q := uint16('"')
	if start < len(script) && script[start] == '\'' {
		q = '\''
	}
	renamed := append([]uint16{}, script[:start]...)
	renamed = append(renamed, q)
	renamed = append(renamed, name...)
	renamed = append(renamed, q)
	renamed = append(renamed, script[end:]...)
	verified, e := m.Parse(scope, renamed)
	if e != nil || verified.ErrorUnits != nil {
		return nil, verified, e
	}
	return renamed, ParsedScript{}, nil
}
func (m *Metadata) UsesBanned(scope *cooperative.Scope, script []uint16) (bool, error) {
	if e := m.check(scope); e != nil {
		return false, e
	}
	ast, e := m.parser.Parse(scope, script)
	if e != nil {
		return false, e
	}
	if !ast.Accepted {
		return false, nil
	}
	banned := false
	var visit func(*javascriptparse.Value)
	visit = func(node *javascriptparse.Value) {
		if node == nil {
			return
		}
		switch nodeType(node) {
		case "MemberExpression":
			if !astBool(astGet(node, "computed")) && nodeType(astGet(node, "object")) == "Identifier" && nodeType(astGet(node, "property")) == "Identifier" {
				o, p := astString(astGet(astGet(node, "object"), "name")), astString(astGet(astGet(node, "property"), "name"))
				if o == "Date" && p == "now" || o == "Math" && p == "random" {
					banned = true
				}
			}
		case "NewExpression":
			if nodeType(astGet(node, "callee")) == "Identifier" && astString(astGet(astGet(node, "callee"), "name")) == "Date" && len(astArray(astGet(node, "arguments"))) == 0 {
				banned = true
			}
		}
		for _, p := range node.Properties {
			if p.Value.Kind == "object" || p.Value.Kind == "array" {
				visit(p.Value)
			}
		}
	}
	visit(ast.AST)
	return banned, nil
}
