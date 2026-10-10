package websearch

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"regexp"
	"strconv"
	"unicode/utf16"
)

func NativeToolSchema(input protocol.JSONObject) json.RawMessage {
	return raw(protocol.NewJSONObject(field("type", "web_search_20250305"), field("name", "web_search"), protocol.JSONField{Name: "allowed_domains", Value: input.Get("allowed_domains")}, protocol.JSONField{Name: "blocked_domains", Value: input.Get("blocked_domains")}, field("max_uses", 8)))
}
func trimUnits(units []uint16) []uint16 {
	space := func(r uint16) bool { return len(jsTrim(string(rune(r)))) == 0 }
	for len(units) > 0 && space(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && space(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return units
}
func NativeOutput(blocks []protocol.JSONObject, query json.RawMessage, duration float64) Output {
	results := []json.RawMessage{}
	var textAcc []uint16
	inText := true
	for _, block := range blocks {
		switch block.String("type") {
		case "server_tool_use":
			if inText {
				inText = false
				if value := trimUnits(textAcc); len(value) > 0 {
					results = append(results, protocol.QuoteJavaScriptUTF16(value))
				}
				textAcc = nil
			}
			continue
		case "web_search_tool_result":
			var rows []protocol.JSONObject
			content := block.Get("content")
			if len(content) == 0 || content[0] != '[' || json.Unmarshal(content, &rows) != nil {
				var detail protocol.JSONObject
				_ = json.Unmarshal(content, &detail)
				message := "Web search error: " + detail.String("error_code")
				if detail.Get("error_code") == nil {
					message = "Web search error: undefined"
				}
				results = append(results, quote(message))
				continue
			}
			hits := []protocol.JSONObject{}
			for _, row := range rows {
				hits = append(hits, protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: row.Get("title")}, protocol.JSONField{Name: "url", Value: row.Get("url")}))
			}
			results = append(results, raw(protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: block.Get("tool_use_id")}, field("content", hits))))
		case "text":
			units, e := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if e != nil {
				units = utf16.Encode([]rune("undefined"))
			}
			if inText {
				textAcc = append(textAcc, units...)
			} else {
				inText = true
				textAcc = units
			}
		}
	}
	if len(textAcc) > 0 {
		results = append(results, protocol.QuoteJavaScriptUTF16(trimUnits(textAcc)))
	}
	return Output{query, results, duration}
}
func MapResult(output protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	parts := []json.RawMessage{quote("Web search results for query: \""), output.Get("query"), quote("\"\n\n")}
	var results []json.RawMessage
	_ = json.Unmarshal(output.Get("results"), &results)
	for _, result := range results {
		if string(result) == "null" {
			continue
		}
		if _, e := protocol.DecodeJavaScriptJSONString(result); e == nil {
			parts = append(parts, result, quote("\n\n"))
			continue
		}
		var object protocol.JSONObject
		_ = json.Unmarshal(result, &object)
		var links []json.RawMessage
		_ = json.Unmarshal(object.Get("content"), &links)
		if len(links) > 0 {
			serialized, e := protocol.ParseAndStringifyJavaScriptJSON(object.Get("content"))
			if e != nil {
				return protocol.JSONObject{}, e
			}
			parts = append(parts, quote("Links: "+string(serialized)+"\n\n"))
		} else {
			parts = append(parts, quote("No links found.\n\n"))
		}
	}
	parts = append(parts, quote("\nREMINDER: You MUST include the sources above in your response to the user using markdown hyperlinks."))
	joined, e := protocol.JoinJavaScriptValuesJSON(parts, "")
	if e != nil {
		return protocol.JSONObject{}, e
	}
	units, e := protocol.DecodeJavaScriptJSONString(joined)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: protocol.QuoteJavaScriptUTF16(trimUnits(units))}), nil
}

type ProgressReducer struct {
	query     json.RawMessage
	currentID json.RawMessage
	partial   string
	counter   int
	queries   map[string]json.RawMessage
}

func NewProgressReducer(query json.RawMessage) *ProgressReducer {
	return &ProgressReducer{query: query, queries: map[string]json.RawMessage{}}
}

var queryPattern = regexp.MustCompile(`"query"\s*:\s*"((?:[^"\\]|\\.)*)"`)

func (p *ProgressReducer) Observe(event protocol.JSONObject, progress Progress) error {
	if event.String("type") == "content_block_start" {
		var block protocol.JSONObject
		_ = json.Unmarshal(event.Get("content_block"), &block)
		if block.String("type") == "server_tool_use" {
			p.currentID = block.Get("id")
			p.partial = ""
			return nil
		}
		if block.String("type") == "web_search_tool_result" {
			id := block.Get("tool_use_id")
			actual := p.query
			if used := p.queries[string(id)]; used != nil && string(used) != `""` {
				actual = used
			}
			var rows []json.RawMessage
			_ = json.Unmarshal(block.Get("content"), &rows)
			p.counter++
			if progress != nil {
				if id == nil || string(id) == `""` {
					id = quote("search-progress-" + strconv.Itoa(p.counter))
				}
				return progress(id, protocol.NewJSONObject(field("type", "search_results_received"), field("resultCount", len(rows)), protocol.JSONField{Name: "query", Value: actual}))
			}
		}
	}
	if p.currentID != nil && string(p.currentID) != `""` && event.String("type") == "content_block_delta" {
		var delta protocol.JSONObject
		_ = json.Unmarshal(event.Get("delta"), &delta)
		if delta.String("type") == "input_json_delta" && delta.String("partial_json") != "" {
			p.partial += delta.String("partial_json")
			if match := queryPattern.FindStringSubmatch(p.partial); match != nil && match[1] != "" {
				query := json.RawMessage(`"` + match[1] + `"`)
				if json.Valid(query) {
					query = protocol.StringifyValidatedJavaScriptJSON(query)
					if old := p.queries[string(p.currentID)]; old == nil || string(old) != string(query) {
						p.queries[string(p.currentID)] = query
						p.counter++
						if progress != nil {
							return progress(quote("search-progress-"+strconv.Itoa(p.counter)), protocol.NewJSONObject(field("type", "query_update"), protocol.JSONField{Name: "query", Value: query}))
						}
					}
				}
			}
		}
	}
	return nil
}
