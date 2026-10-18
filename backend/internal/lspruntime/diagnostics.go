package lspruntime

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type diagnosticSet struct {
	server string
	files  []protocol.JSONObject
}
type diagnosticRegistry struct {
	mu        sync.Mutex
	pending   []diagnosticSet
	delivered map[string]map[string]bool
	lru       []string
}

func field(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func (d *diagnosticRegistry) register(server string, params json.RawMessage) {
	value := object(params)
	uri := value.String("uri")
	var diagnostics []protocol.JSONObject
	if uri == "" || json.Unmarshal(value.Get("diagnostics"), &diagnostics) != nil || len(diagnostics) == 0 {
		return
	}
	if strings.HasPrefix(uri, "file://") {
		if parsed, err := url.Parse(uri); err == nil && parsed.Scheme == "file" && (parsed.Host == "" || parsed.Host == "localhost") {
			uri = parsed.Path
		}
	}
	rows := []protocol.JSONObject{}
	for _, row := range diagnostics {
		rangeValue := object(row.Get("range"))
		start, end := object(rangeValue.Get("start")), object(rangeValue.Get("end"))
		if !rangeValue.IsObject() || !start.IsObject() || !end.IsObject() {
			return
		}
		var severity int
		_ = json.Unmarshal(row.Get("severity"), &severity)
		label := map[int]string{1: "Error", 2: "Warning", 3: "Info", 4: "Hint"}[severity]
		if label == "" {
			label = "Error"
		}
		next := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: row.Get("message")}, field("severity", label), field("range", protocol.NewJSONObject(field("start", protocol.NewJSONObject(protocol.JSONField{Name: "line", Value: start.Get("line")}, protocol.JSONField{Name: "character", Value: start.Get("character")})), field("end", protocol.NewJSONObject(protocol.JSONField{Name: "line", Value: end.Get("line")}, protocol.JSONField{Name: "character", Value: end.Get("character")})))))
		if row.Get("source") != nil {
			next.Set("source", row.Get("source"))
		}
		if code := row.Get("code"); code != nil && string(code) != "null" {
			value, err := protocol.JavaScriptStringJSON(code)
			if err != nil {
				return
			}
			next.Set("code", value)
		}
		rows = append(rows, next)
	}
	file := protocol.NewJSONObject(field("uri", uri), field("diagnostics", rows))
	d.mu.Lock()
	d.pending = append(d.pending, diagnosticSet{server, []protocol.JSONObject{file}})
	d.mu.Unlock()
}
func diagnosticKey(row protocol.JSONObject) string {
	source, code := row.Get("source"), row.Get("code")
	if len(source) == 0 || string(source) == `""` || string(source) == "false" || string(source) == "0" {
		source = json.RawMessage("null")
	}
	if len(code) == 0 || string(code) == `""` || string(code) == "false" || string(code) == "0" {
		code = json.RawMessage("null")
	}
	key := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: row.Get("message")}, protocol.JSONField{Name: "severity", Value: row.Get("severity")}, protocol.JSONField{Name: "range", Value: row.Get("range")}, protocol.JSONField{Name: "source", Value: source}, protocol.JSONField{Name: "code", Value: code})
	return string(raw(key))
}
func (d *diagnosticRegistry) touch(uri string) {
	for i, value := range d.lru {
		if value == uri {
			d.lru = append(d.lru[:i], d.lru[i+1:]...)
			break
		}
	}
	d.lru = append(d.lru, uri)
	for len(d.lru) > 500 {
		delete(d.delivered, d.lru[0])
		d.lru = d.lru[1:]
	}
}
func (d *diagnosticRegistry) clearDelivered(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.delivered, uri)
	for i, value := range d.lru {
		if value == uri {
			d.lru = append(d.lru[:i], d.lru[i+1:]...)
			break
		}
	}
}
func (m *Manager) ResetDiagnostics() {
	if m == nil {
		return
	}
	d := &m.diagnostics
	d.mu.Lock()
	d.pending = nil
	d.delivered = nil
	d.lru = nil
	d.mu.Unlock()
}
func (m *Manager) TakeDiagnostics() []protocol.JSONObject {
	if m == nil {
		return nil
	}
	d := &m.diagnostics
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.pending) == 0 {
		return []protocol.JSONObject{}
	}
	files := []string{}
	rows := map[string][]protocol.JSONObject{}
	seen := map[string]map[string]bool{}
	for _, set := range d.pending {
		for _, file := range set.files {
			uri := file.String("uri")
			if _, exists := rows[uri]; !exists {
				files = append(files, uri)
				rows[uri] = []protocol.JSONObject{}
				seen[uri] = map[string]bool{}
			}
			if d.delivered[uri] != nil {
				d.touch(uri)
			}
			var diagnostics []protocol.JSONObject
			_ = json.Unmarshal(file.Get("diagnostics"), &diagnostics)
			for _, row := range diagnostics {
				key := diagnosticKey(row)
				if !seen[uri][key] && !d.delivered[uri][key] {
					seen[uri][key] = true
					rows[uri] = append(rows[uri], row.Clone())
				}
			}
		}
	}
	d.pending = nil
	result := []protocol.JSONObject{}
	total := 0
	if d.delivered == nil {
		d.delivered = map[string]map[string]bool{}
	}
	severity := func(value string) int {
		switch value {
		case "Error":
			return 1
		case "Warning":
			return 2
		case "Info":
			return 3
		}
		return 4
	}
	for _, uri := range files {
		values := rows[uri]
		sort.SliceStable(values, func(i, j int) bool {
			return severity(values[i].String("severity")) < severity(values[j].String("severity"))
		})
		limit := min(10, 30-total, len(values))
		values = values[:limit]
		if len(values) == 0 {
			continue
		}
		total += len(values)
		if d.delivered[uri] == nil {
			d.delivered[uri] = map[string]bool{}
		}
		d.touch(uri)
		for _, row := range values {
			d.delivered[uri][diagnosticKey(row)] = true
		}
		result = append(result, protocol.NewJSONObject(field("uri", uri), field("diagnostics", values)))
	}
	if len(result) == 0 {
		return []protocol.JSONObject{}
	}
	return []protocol.JSONObject{protocol.NewJSONObject(field("type", "diagnostics"), field("files", result), field("isNew", true))}
}
