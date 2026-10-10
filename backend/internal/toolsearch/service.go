// Package toolsearch implements the original catalogue search and discovery
// state. It owns no tools, model loop, connection, or transcript writer.
package toolsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

const Name = "ToolSearch"

type Tool struct {
	Name        json.RawMessage
	Aliases     []json.RawMessage
	IsMCP       bool
	AlwaysLoad  bool
	ShouldDefer bool
	SearchHint  json.RawMessage
	Prompt      func(context.Context) (json.RawMessage, error)
	Schema      func(context.Context) (json.RawMessage, error)
}

func IsDeferred(t Tool) bool {
	if t.AlwaysLoad {
		return false
	}
	if t.IsMCP {
		return true
	}
	return !same(t.Name, quoted(Name)) && t.ShouldDefer
}

type description struct {
	value []uint16
	err   error
}
type Service struct {
	mu      sync.Mutex
	key     string
	cache   map[string]description
	vm      *goja.Runtime
	lowerFn goja.Callable
	vmMu    sync.Mutex
	tokenMu sync.Mutex
	tokens  map[string]*tokenCount
}

func New() *Service {
	vm := goja.New()
	// This language primitive is the sole JavaScript executed here. Search,
	// scoring, catalogue mutation and permissions are native Go code.
	v, e := vm.RunString(`(function(s) { return s.toLowerCase() })`)
	if e != nil {
		panic(e)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		panic("language lowercase primitive unavailable")
	}
	return &Service{cache: map[string]description{}, tokens: map[string]*tokenCount{}, vm: vm, lowerFn: fn}
}

func units(v json.RawMessage) []uint16 { out, _ := protocol.DecodeJavaScriptJSONString(v); return out }
func quoted(s string) json.RawMessage  { return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(s))) }
func raw(v any) json.RawMessage        { out, _ := json.Marshal(v); return out }
func same(a, b json.RawMessage) bool   { return equal(units(a), units(b)) }
func equal(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func less(a, b []uint16) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
func has(a, b []uint16) bool {
	for i := 0; i+len(b) <= len(a); i++ {
		if equal(a[i:i+len(b)], b) {
			return true
		}
	}
	return false
}
func whitespace(u uint16) bool {
	return u >= 9 && u <= 13 || u == ' ' || u == 0xa0 || u == 0x1680 || u >= 0x2000 && u <= 0x200a || u == 0x2028 || u == 0x2029 || u == 0x202f || u == 0x205f || u == 0x3000 || u == 0xfeff
}
func trim(u []uint16) []uint16 {
	for len(u) > 0 && whitespace(u[0]) {
		u = u[1:]
	}
	for len(u) > 0 && whitespace(u[len(u)-1]) {
		u = u[:len(u)-1]
	}
	return u
}
func split(u []uint16, separator func(uint16) bool) [][]uint16 {
	parts := [][]uint16{}
	start := 0
	for i := 0; i <= len(u); i++ {
		if i == len(u) || separator(u[i]) {
			if i > start {
				parts = append(parts, u[start:i])
			}
			start = i + 1
		}
	}
	return parts
}
func (s *Service) lower(u []uint16) ([]uint16, error) {
	s.vmMu.Lock()
	defer s.vmMu.Unlock()
	v, e := s.lowerFn(goja.Undefined(), goja.StringFromUTF16(u))
	if e != nil {
		return nil, e
	}
	str, ok := v.(goja.String)
	if !ok {
		return nil, fmt.Errorf("language lowercase returned non-string")
	}
	out := make([]uint16, str.Length())
	for i := range out {
		out[i] = str.CharAt(i)
	}
	return out, nil
}

func (s *Service) invalidate(tools []Tool) {
	names := [][]uint16{}
	for _, t := range tools {
		names = append(names, units(t.Name))
	}
	sort.SliceStable(names, func(i, j int) bool { return less(names[i], names[j]) })
	keyUnits := []uint16{}
	for i, name := range names {
		if i > 0 {
			keyUnits = append(keyUnits, ',')
		}
		keyUnits = append(keyUnits, name...)
	}
	key := string(protocol.QuoteJavaScriptUTF16(keyUnits))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != key {
		s.key = key
		s.cache = map[string]description{}
	}
}
func (s *Service) description(ctx context.Context, tool Tool) ([]uint16, error) {
	key := string(protocol.StringifyValidatedJavaScriptJSON(tool.Name))
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok {
		return cached.value, cached.err
	}
	if tool.Prompt == nil {
		return nil, &CapabilityError{"actual tool prompt producer"}
	}
	text, e := tool.Prompt(ctx)
	value := units(text)
	s.mu.Lock()
	s.cache[key] = description{value, e}
	s.mu.Unlock()
	return value, e
}

func find(tools []Tool, name []uint16) *Tool {
	for i := range tools {
		if equal(units(tools[i].Name), name) {
			return &tools[i]
		}
		for _, alias := range tools[i].Aliases {
			if equal(units(alias), name) {
				return &tools[i]
			}
		}
	}
	return nil
}
func ToolMatchesName(tool Tool, name string) bool {
	return find([]Tool{tool}, units(quoted(name))) != nil
}
func sliceLimit(length int, limit float64) int {
	if math.IsNaN(limit) {
		return 0
	}
	limit = math.Trunc(limit)
	if limit < 0 {
		limit = float64(length) + limit
	}
	if limit < 0 {
		return 0
	}
	if limit > float64(length) {
		return length
	}
	return int(limit)
}
func word(u uint16) bool {
	return u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || u == '_'
}
func wordMatch(text, term []uint16) bool {
	if len(term) == 0 {
		return false
	}
	for i := 0; i+len(term) <= len(text); i++ {
		if !equal(text[i:i+len(term)], term) {
			continue
		}
		before := i > 0 && word(text[i-1])
		after := i+len(term) < len(text) && word(text[i+len(term)])
		if before != word(term[0]) && after != word(term[len(term)-1]) {
			return true
		}
	}
	return false
}
func (s *Service) parseName(name []uint16) ([][]uint16, []uint16, bool, error) {
	mcp := len(name) >= 5 && equal(name[:5], []uint16{'m', 'c', 'p', '_', '_'})
	if mcp {
		lower, e := s.lower(name[5:])
		if e != nil {
			return nil, nil, mcp, e
		}
		parts := split(lower, func(u uint16) bool { return u == '_' })
		full := []uint16{}
		for i := 0; i < len(lower); i++ {
			u := lower[i]
			if u == '_' {
				u = ' '
				if i+1 < len(lower) && lower[i+1] == '_' {
					i++
				}
			}
			full = append(full, u)
		}
		return parts, full, mcp, nil
	}
	separated := []uint16{}
	for i, u := range name {
		if !mcp && i > 0 && name[i-1] >= 'a' && name[i-1] <= 'z' && u >= 'A' && u <= 'Z' {
			separated = append(separated, ' ')
		}
		if u == '_' {
			u = ' '
		}
		separated = append(separated, u)
	}
	lower, e := s.lower(separated)
	if e != nil {
		return nil, nil, mcp, e
	}
	parts := split(lower, whitespace)
	full := lower
	if !mcp {
		full = []uint16{}
		for i, p := range parts {
			if i > 0 {
				full = append(full, ' ')
			}
			full = append(full, p...)
		}
	}
	return parts, full, mcp, nil
}

func (s *Service) Call(ctx context.Context, input protocol.JSONObject, tools []Tool, pending []json.RawMessage) (protocol.JSONObject, error) {
	query := units(input.Get("query"))
	max := 5.0
	if input.Get("max_results") != nil {
		if e := json.Unmarshal(input.Get("max_results"), &max); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	deferred := []Tool{}
	for _, t := range tools {
		if IsDeferred(t) {
			deferred = append(deferred, t)
		}
	}
	s.invalidate(deferred)
	result := func(matches []json.RawMessage) protocol.JSONObject {
		out := protocol.NewJSONObject(protocol.JSONField{Name: "matches", Value: raw(matches)}, protocol.JSONField{Name: "query", Value: input.Get("query")}, protocol.JSONField{Name: "total_deferred_tools", Value: raw(len(deferred))})
		if len(matches) == 0 && len(pending) > 0 {
			out.Set("pending_mcp_servers", raw(pending))
		}
		return out
	}
	lower, e := s.lower(query)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if len(lower) > 7 && equal(lower[:7], []uint16{'s', 'e', 'l', 'e', 'c', 't', ':'}) {
		// The original non-multiline expression excludes line terminators.
		selected := query[7:]
		selectable := len(selected) > 0
		for _, u := range selected {
			if u == '\n' || u == '\r' || u == 0x2028 || u == 0x2029 {
				selectable = false
			}
		}
		if selectable {
			found := []json.RawMessage{}
			seen := map[string]bool{}
			for _, requested := range split(selected, func(u uint16) bool { return u == ',' }) {
				requested = trim(requested)
				if len(requested) == 0 {
					continue
				}
				t := find(deferred, requested)
				if t == nil {
					t = find(tools, requested)
				}
				if t != nil {
					key := string(t.Name)
					if !seen[key] {
						seen[key] = true
						found = append(found, t.Name)
					}
				}
			}
			return result(found), nil
		}
	}
	lower = trim(lower)
	for _, pool := range [][]Tool{deferred, tools} {
		for _, t := range pool {
			n, e := s.lower(units(t.Name))
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if equal(n, lower) {
				return result([]json.RawMessage{t.Name}), nil
			}
		}
	}
	if len(lower) > 5 && equal(lower[:5], []uint16{'m', 'c', 'p', '_', '_'}) {
		matches := []json.RawMessage{}
		for _, t := range deferred {
			n, e := s.lower(units(t.Name))
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if len(n) >= len(lower) && equal(n[:len(lower)], lower) {
				matches = append(matches, t.Name)
			}
		}
		matches = matches[:sliceLimit(len(matches), max)]
		if len(matches) > 0 {
			return result(matches), nil
		}
	}
	terms := split(lower, whitespace)
	required, optional := [][]uint16{}, [][]uint16{}
	for _, term := range terms {
		if len(term) > 1 && term[0] == '+' {
			required = append(required, term[1:])
		} else {
			optional = append(optional, term)
		}
	}
	scoring := terms
	if len(required) > 0 {
		scoring = append(append([][]uint16{}, required...), optional...)
	}
	type score struct {
		name  json.RawMessage
		value int
	}
	scores := []score{}
	for _, t := range deferred {
		if e := context.Cause(ctx); e != nil {
			return protocol.JSONObject{}, e
		}
		parts, full, mcp, e := s.parseName(units(t.Name))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		desc, e := s.description(ctx, t)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		desc, e = s.lower(desc)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		hint, e := s.lower(units(t.SearchHint))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		partMatch := func(term []uint16) (exact, partial bool) {
			for _, part := range parts {
				exact = exact || equal(part, term)
				partial = partial || has(part, term)
			}
			return
		}
		eligible := true
		for _, term := range required {
			a, b := partMatch(term)
			if !a && !b && !wordMatch(desc, term) && !wordMatch(hint, term) {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}
		value := 0
		for _, term := range scoring {
			a, b := partMatch(term)
			if a {
				if mcp {
					value += 12
				} else {
					value += 10
				}
			} else if b {
				if mcp {
					value += 6
				} else {
					value += 5
				}
			}
			if has(full, term) && value == 0 {
				value += 3
			}
			if wordMatch(hint, term) {
				value += 4
			}
			if wordMatch(desc, term) {
				value += 2
			}
		}
		if value > 0 {
			scores = append(scores, score{t.Name, value})
		}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].value > scores[j].value })
	scores = scores[:sliceLimit(len(scores), max)]
	matches := []json.RawMessage{}
	for _, item := range scores {
		matches = append(matches, item.name)
	}
	return result(matches), nil
}

func MapResult(content protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	var matches []json.RawMessage
	if e := json.Unmarshal(content.Get("matches"), &matches); e != nil {
		return protocol.JSONObject{}, e
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id})
	if len(matches) == 0 {
		text := quoted("No matching deferred tools found")
		var pending []json.RawMessage
		_ = json.Unmarshal(content.Get("pending_mcp_servers"), &pending)
		if len(pending) > 0 {
			joined, e := protocol.JoinJavaScriptValuesJSON(pending, ", ")
			if e != nil {
				return protocol.JSONObject{}, e
			}
			text, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{text, quoted(". Some MCP servers are still connecting: "), joined, quoted(". Their tools will become available shortly — try searching again.")}, "")
			if e != nil {
				return protocol.JSONObject{}, e
			}
		}
		out.Set("content", text)
	} else {
		blocks := []protocol.JSONObject{}
		for _, name := range matches {
			blocks = append(blocks, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_reference")}, protocol.JSONField{Name: "tool_name", Value: name}))
		}
		out.Set("content", raw(blocks))
	}
	return out, nil
}

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "native tool search capability is not migrated: " + e.Boundary
}
