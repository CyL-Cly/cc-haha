package lspruntime

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// All server prose stays as JavaScript UTF-16, including legal unpaired units.
// Native paths alone cross the host string boundary.
type jsText []uint16

func js(parts ...any) jsText {
	var out jsText
	for _, p := range parts {
		switch v := p.(type) {
		case jsText:
			out = append(out, v...)
		case json.RawMessage:
			encoded, _ := protocol.JavaScriptStringJSON(v)
			u, _ := protocol.DecodeJavaScriptJSONString(encoded)
			out = append(out, u...)
		default:
			out = append(out, utf16.Encode([]rune(fmt.Sprint(v)))...)
		}
	}
	return out
}
func joined(lines []jsText, separator string) jsText {
	var out jsText
	for i, line := range lines {
		if i > 0 {
			out = append(out, js(separator)...)
		}
		out = append(out, line...)
	}
	return out
}
func obj(value json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(value)
	return o
}
func rows(value json.RawMessage) []json.RawMessage {
	var r []json.RawMessage
	_ = json.Unmarshal(value, &r)
	return r
}
func count(value json.RawMessage) int                 { var n int; _ = json.Unmarshal(value, &n); return n }
func start(value json.RawMessage) protocol.JSONObject { return obj(obj(value).Get("start")) }
func line(value json.RawMessage) int                  { return count(start(value).Get("line")) + 1 }
func position(value json.RawMessage) string {
	s := start(value)
	return fmt.Sprintf("%d:%d", count(s.Get("line"))+1, count(s.Get("character"))+1)
}
func truth(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	s := string(value)
	return s != "null" && s != "false" && s != "0" && s != `""`
}
func URIPath(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if u, e := url.PathUnescape(p); e == nil {
		p = u
	}
	return p
}
func displayURI(uri, cwd string) string {
	if uri == "" {
		return "<unknown location>"
	}
	p := URIPath(uri)
	if cwd != "" {
		if rel, e := filepath.Rel(cwd, p); e == nil {
			rel = strings.ReplaceAll(rel, `\`, "/")
			if len(utf16.Encode([]rune(rel))) < len(utf16.Encode([]rune(p))) && !strings.HasPrefix(rel, "../../") {
				return rel
			}
		}
	}
	return strings.ReplaceAll(p, `\`, "/")
}
func location(value json.RawMessage) protocol.JSONObject {
	o := obj(value)
	if o.Get("targetUri") != nil {
		r := o.Get("targetSelectionRange")
		if !truth(r) {
			r = o.Get("targetRange")
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "uri", Value: o.Get("targetUri")}, protocol.JSONField{Name: "range", Value: r})
	}
	return o
}
func kind(value json.RawMessage) string {
	k := []string{"Unknown", "File", "Module", "Namespace", "Package", "Class", "Method", "Property", "Field", "Constructor", "Enum", "Interface", "Function", "Variable", "Constant", "String", "Number", "Boolean", "Array", "Object", "Key", "Null", "EnumMember", "Struct", "Event", "Operator", "TypeParameter"}
	i := count(value)
	if i > 0 && i < len(k) {
		return k[i]
	}
	return "Unknown"
}
func validLocations(result json.RawMessage) []protocol.JSONObject {
	rs := rows(result)
	if rs == nil && truth(result) {
		rs = []json.RawMessage{result}
	}
	out := []protocol.JSONObject{}
	for _, r := range rs {
		l := location(r)
		if l.String("uri") != "" {
			out = append(out, l)
		}
	}
	return out
}
func distinct(items []protocol.JSONObject, key string) int {
	s := map[string]bool{}
	for _, o := range items {
		s[string(o.Get(key))] = true
	}
	return len(s)
}
func group(items []protocol.JSONObject, uri func(protocol.JSONObject) string, cwd string) ([]string, map[string][]protocol.JSONObject) {
	order := []string{}
	groups := map[string][]protocol.JSONObject{}
	for _, o := range items {
		k := displayURI(uri(o), cwd)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], o)
	}
	return order, groups
}
func symbolCount(rs []json.RawMessage) int {
	n := len(rs)
	for _, r := range rs {
		n += symbolCount(rows(obj(r).Get("children")))
	}
	return n
}
func symbolNode(o protocol.JSONObject, depth int) []jsText {
	out := js(strings.Repeat("  ", depth), o.Get("name"), " (", kind(o.Get("kind")), ")")
	if truth(o.Get("detail")) {
		out = js(out, " ", o.Get("detail"))
	}
	lines := []jsText{js(out, " - Line ", line(o.Get("range")))}
	for _, r := range rows(o.Get("children")) {
		lines = append(lines, symbolNode(obj(r), depth+1)...)
	}
	return lines
}
func callItem(o protocol.JSONObject, cwd string) jsText {
	out := js(o.Get("name"), " (", kind(o.Get("kind")), ") - ", displayURI(o.String("uri"), cwd))
	if o.String("uri") == "" {
		return out
	}
	out = js(out, ":", line(o.Get("range")))
	if truth(o.Get("detail")) {
		out = js(out, " [", o.Get("detail"), "]")
	}
	return out
}

const noDefinition = "No definition found. This may occur if the cursor is not on a symbol, or if the definition is in an external library not indexed by the LSP server."
const noReferences = "No references found. This may occur if the symbol has no usages, or if the LSP server has not fully indexed the workspace."
const noWorkspaceSymbols = "No symbols found in workspace. This may occur if the workspace is empty, or if the LSP server has not finished indexing the project."

// FormatResult is the original LSP tool's presentation and its result/file counts.
func FormatResult(operation string, result json.RawMessage, cwd string) (json.RawMessage, int, int) {
	var out jsText
	n, f := 0, 0
	rs := rows(result)
	switch operation {
	case "goToDefinition", "goToImplementation", "findReferences":
		ls := validLocations(result)
		n, f = len(ls), distinct(ls, "uri")
		loc := func(l protocol.JSONObject) string {
			return displayURI(l.String("uri"), cwd) + ":" + position(l.Get("range"))
		}
		if operation != "findReferences" {
			if n == 0 {
				out = js(noDefinition)
			} else if n == 1 {
				out = js("Defined in ", loc(ls[0]))
			} else {
				lines := []jsText{js("Found ", n, " definitions:")}
				for _, l := range ls {
					lines = append(lines, js("  ", loc(l)))
				}
				out = joined(lines, "\n")
			}
		} else if n == 0 {
			out = js(noReferences)
		} else if n == 1 {
			out = js("Found 1 reference:\n  ", loc(ls[0]))
		} else {
			order, groups := group(ls, func(o protocol.JSONObject) string { return o.String("uri") }, cwd)
			lines := []jsText{js("Found ", n, " references across ", len(order), " files:")}
			for _, path := range order {
				lines = append(lines, js("\n", path, ":"))
				for _, l := range groups[path] {
					lines = append(lines, js("  Line ", position(l.Get("range"))))
				}
			}
			out = joined(lines, "\n")
		}
	case "hover":
		if !truth(result) {
			out = js("No hover information available. This may occur if the cursor is not on a symbol, or if the LSP server has not fully indexed the file.")
			break
		}
		n, f = 1, 1
		o := obj(result)
		contents := o.Get("contents")
		extract := func(v json.RawMessage) jsText {
			if len(v) > 0 && v[0] == '"' {
				return js(v)
			}
			return js(obj(v).Get("value"))
		}
		if arr := rows(contents); arr != nil {
			lines := []jsText{}
			for _, r := range arr {
				lines = append(lines, extract(r))
			}
			out = joined(lines, "\n\n")
		} else {
			out = extract(contents)
		}
		if truth(o.Get("range")) {
			out = js("Hover info at ", position(o.Get("range")), ":\n\n", out)
		}
	case "documentSymbol", "workspaceSymbol":
		if len(rs) == 0 {
			if operation == "documentSymbol" {
				out = js("No symbols found in document. This may occur if the file is empty, not supported by the LSP server, or if the server has not fully indexed the file.")
			} else {
				out = js(noWorkspaceSymbols)
			}
			break
		}
		if operation == "documentSymbol" && obj(rs[0]).Get("location") == nil {
			n, f = symbolCount(rs), 1
			lines := []jsText{js("Document symbols:")}
			for _, r := range rs {
				lines = append(lines, symbolNode(obj(r), 0)...)
			}
			out = joined(lines, "\n")
			break
		}
		ls := []protocol.JSONObject{}
		for _, r := range rs {
			o := obj(r)
			if obj(o.Get("location")).String("uri") != "" {
				ls = append(ls, o)
			}
		}
		n = len(ls)
		uris := map[string]bool{}
		for _, o := range ls {
			uris[obj(o.Get("location")).String("uri")] = true
		}
		f = len(uris)
		if operation == "documentSymbol" {
			n, f = len(rs), 1
		}
		if len(ls) == 0 {
			out = js(noWorkspaceSymbols)
			break
		}
		plural := "symbols"
		if len(ls) == 1 {
			plural = "symbol"
		}
		lines := []jsText{js("Found ", len(ls), " ", plural, " in workspace:")}
		order, groups := group(ls, func(o protocol.JSONObject) string { return obj(o.Get("location")).String("uri") }, cwd)
		for _, path := range order {
			lines = append(lines, js("\n", path, ":"))
			for _, o := range groups[path] {
				l := js("  ", o.Get("name"), " (", kind(o.Get("kind")), ") - Line ", line(obj(o.Get("location")).Get("range")))
				if truth(o.Get("containerName")) {
					l = js(l, " in ", o.Get("containerName"))
				}
				lines = append(lines, l)
			}
		}
		out = joined(lines, "\n")
	case "prepareCallHierarchy":
		n = len(rs)
		ls := []protocol.JSONObject{}
		for _, r := range rs {
			ls = append(ls, obj(r))
		}
		f = distinct(ls, "uri")
		if n == 0 {
			out = js("No call hierarchy item found at this position")
		} else if n == 1 {
			out = js("Call hierarchy item: ", callItem(ls[0], cwd))
		} else {
			lines := []jsText{js("Found ", n, " call hierarchy items:")}
			for _, o := range ls {
				lines = append(lines, js("  ", callItem(o, cwd)))
			}
			out = joined(lines, "\n")
		}
	case "incomingCalls", "outgoingCalls":
		n = len(rs)
		direction, key, label, empty := "incoming", "from", "calls at", "No incoming calls found (nothing calls this function)"
		if operation == "outgoingCalls" {
			direction, key, label, empty = "outgoing", "to", "called from", "No outgoing calls found (this function calls nothing)"
		}
		if n == 0 {
			out = js(empty)
			break
		}
		ls := []protocol.JSONObject{}
		uris := map[string]bool{}
		for _, r := range rs {
			o := obj(r)
			item := obj(o.Get(key))
			if truth(o.Get(key)) {
				ls = append(ls, o)
				if item.String("uri") != "" {
					uris[item.String("uri")] = true
				}
			}
		}
		f = len(uris)
		plural := "calls"
		if n == 1 {
			plural = "call"
		}
		lines := []jsText{js("Found ", n, " ", direction, " ", plural, ":")}
		order, groups := group(ls, func(o protocol.JSONObject) string { return obj(o.Get(key)).String("uri") }, cwd)
		for _, path := range order {
			lines = append(lines, js("\n", path, ":"))
			for _, o := range groups[path] {
				item := obj(o.Get(key))
				l := js("  ", item.Get("name"), " (", kind(item.Get("kind")), ") - Line ", line(item.Get("range")))
				ranges := rows(o.Get("fromRanges"))
				if len(ranges) > 0 {
					positions := []jsText{}
					for _, r := range ranges {
						positions = append(positions, js(position(r)))
					}
					l = js(l, " [", label, ": ", joined(positions, ", "), "]")
				}
				lines = append(lines, l)
			}
		}
		out = joined(lines, "\n")
	}
	return protocol.QuoteJavaScriptUTF16(out), n, f
}
