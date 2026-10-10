package sessioncollaboration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func bounded(value json.RawMessage, fallback, min, max float64) float64 {
	n := fallback
	if value != nil && string(value) != "null" {
		n = num(value)
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
}
func stringifyValue(value json.RawMessage) json.RawMessage {
	if value == nil {
		return raw("")
	}
	b, e := protocol.ParseAndStringifyJavaScriptJSON(value)
	if e != nil {
		return raw("")
	}
	return raw(string(b))
}
func ProjectHistory(value, options protocol.JSONObject) protocol.JSONObject {
	messages := list(value.Get("messages"))
	turns := [][]protocol.JSONObject{}
	for _, m := range messages {
		if m.String("type") == "user" || len(turns) == 0 {
			turns = append(turns, []protocol.JSONObject{})
		}
		turns[len(turns)-1] = append(turns[len(turns)-1], m)
	}
	limit := int(bounded(options.Get("limit"), 1, 1, 10))
	start := len(turns) - limit
	if start < 0 {
		start = 0
	}
	selected := turns[start:]
	itemLimit := int(bounded(options.Get("maxOutputCharsPerItem"), 2000, 100, 8000))
	remaining := int(bounded(options.Get("budgetChars"), 24000, 0, float64(math.MaxInt)))
	truncated := len(selected) < len(turns)
	projected := []protocol.JSONObject{}
	include := isTrue(options.Get("includeOutputs"))
	for _, turn := range selected {
		for _, message := range turn {
			if !include && message.String("type") == "tool_result" {
				continue
			}
			copy := clone(message)
			if !include {
				copy.Delete("toolUseResult")
			}
			if message.String("type") == "tool_result" {
				content := copy.Get("content")
				if !stringField(copy, "content") {
					content = stringifyValue(content)
				}
				if stringLength(content) > itemLimit {
					copy.Set("content", stringSlice(content, 0, itemLimit))
					copy.Set("bodyTruncated", raw(true))
					truncated = true
				}
				copy.Delete("toolUseResult")
			}
			if c := copy.Get("content"); len(c) > 0 && c[0] == '[' {
				content := []protocol.JSONObject{}
				for _, block := range list(c) {
					if !include && block.String("type") == "tool_result" {
						continue
					}
					if block.String("type") != "tool_result" {
						content = append(content, block)
						continue
					}
					text := block.Get("content")
					if !stringField(block, "content") {
						text = stringifyValue(text)
					}
					length := stringLength(text)
					if length > itemLimit {
						truncated = true
					}
					block = clone(block)
					block.Set("content", stringSlice(text, 0, itemLimit))
					if length > itemLimit {
						block.Set("truncated", raw(true))
					}
					content = append(content, block)
				}
				copy.Set("content", raw(content))
			}
			serialized := stringify(copy)
			length := stringLength(serialized)
			if length <= remaining {
				remaining -= length
				projected = append(projected, copy)
				continue
			}
			truncated = true
			if remaining < 100 {
				continue
			}
			fallback := object()
			copyRaw(&fallback, "id", copy.Get("id"))
			copyRaw(&fallback, "type", copy.Get("type"))
			fallback.Set("content", stringSlice(serialized, 0, remaining-100))
			fallback.Set("truncated", raw(true))
			remaining = 0
			projected = append(projected, fallback)
		}
	}
	out := object("messages", projected)
	copyRaw(&out, "page", value.Get("page"))
	out.Set("turnsIncluded", raw(len(selected)))
	out.Set("truncated", raw(truncated))
	out.Set("historyComplete", raw(isTrue(parsed(value.Get("page")).Get("historyComplete")) && !truncated))
	return out
}
func (s *Service) Read(ctx context.Context, id string, options protocol.JSONObject) (protocol.JSONObject, error) {
	cursor := object()
	depth := 0
	if options.String("cursor") != "" {
		b, e := base64.RawURLEncoding.DecodeString(options.String("cursor"))
		if e != nil {
			return protocol.JSONObject{}, bad("Invalid collaboration history cursor")
		}
		if cursor.UnmarshalJSON(b) != nil || num(cursor.Get("version")) != 1 || cursor.String("sessionId") != id {
			return protocol.JSONObject{}, bad("Invalid collaboration history cursor")
		}
		for _, k := range []string{"end", "fragmentEnd", "depth"} {
			if value := cursor.Get(k); value != nil {
				n := num(value)
				if len(value) == 0 || value[0] == '"' || string(value) == "null" || string(value) == "true" || string(value) == "false" || n < 0 || math.Trunc(n) != n {
					return protocol.JSONObject{}, bad("Invalid collaboration history cursor")
				}
			}
		}
		depth = int(num(cursor.Get("depth")))
	}
	if depth >= ReadMaxPages {
		return object("messages", []protocol.JSONObject{}, "turnsIncluded", 0, "truncated", true, "pageLimitReached", true, "page", object("nextCursor", nil, "hasMore", false), "historyComplete", false), nil
	}
	readOptions := object("limit", 100)
	copyRaw(&readOptions, "cursor", cursor.Get("baseCursor"))
	page, e := s.o.Read(ctx, id, readOptions)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	meta := parsed(page.Get("page"))
	if v := cursor.Get("sourceVersion"); v != nil && string(v) != string(meta.Get("sourceVersion")) {
		return protocol.JSONObject{}, conflict("Session history changed during pagination; restart the read")
	}
	messages := list(page.Get("messages"))
	stop := len(messages)
	if cursor.Get("end") != nil && num(cursor.Get("end")) < float64(stop) {
		stop = int(num(cursor.Get("end")))
	}
	start, turns := stop, 0
	limit := bounded(options.Get("limit"), 1, 1, 10)
	for start > 0 {
		start--
		if messages[start].String("type") == "user" {
			turns++
			if float64(turns) >= limit {
				break
			}
		}
	}
	projected := []protocol.JSONObject{}
	remaining, nextEnd := 24000, stop
	fragment := -1
	truncated := false
	for i := stop - 1; i >= start; i-- {
		projectionOptions := clone(options)
		projectionOptions.Set("budgetChars", raw(9007199254740991))
		projection := ProjectHistory(object("messages", []protocol.JSONObject{messages[i]}), projectionOptions)
		items := list(projection.Get("messages"))
		if len(items) == 0 {
			nextEnd = i
			continue
		}
		message := items[0]
		truncated = truncated || isTrue(projection.Get("truncated"))
		serialized := stringify(message)
		total := stringLength(serialized)
		available := total
		if i == stop-1 && cursor.Get("fragmentEnd") != nil && num(cursor.Get("fragmentEnd")) < float64(available) {
			available = int(num(cursor.Get("fragmentEnd")))
		}
		if available > remaining && len(projected) > 0 {
			break
		}
		if available > remaining || i == stop-1 && cursor.Get("fragmentEnd") != nil {
			window := remaining - 300
			if window < 100 {
				window = 100
			}
			from := available - window
			if from < 0 {
				from = 0
			}
			part := object()
			copyRaw(&part, "id", message.Get("id"))
			copyRaw(&part, "type", message.Get("type"))
			part.Set("content", stringSlice(serialized, from, available))
			part.Set("contentEncoding", raw("json-fragment"))
			part.Set("contentPart", raw(object("from", from, "to", available, "total", total)))
			part.Set("truncated", raw(true))
			projected = append([]protocol.JSONObject{part}, projected...)
			truncated = true
			nextEnd = i
			if from > 0 {
				nextEnd = i + 1
				fragment = from
			}
			break
		}
		projected = append([]protocol.JSONObject{message}, projected...)
		remaining -= available
		nextEnd = i
	}
	nextDepth := depth + 1
	older := nextEnd > 0 || truthy(meta.Get("nextCursor"))
	limitReached := nextDepth >= ReadMaxPages && older
	var next json.RawMessage = raw(nil)
	if !limitReached {
		var value protocol.JSONObject
		if nextEnd > 0 {
			value = object("version", 1, "sessionId", id)
			copyRaw(&value, "baseCursor", cursor.Get("baseCursor"))
			value.Set("end", raw(nextEnd))
			copyRaw(&value, "sourceVersion", meta.Get("sourceVersion"))
			if fragment >= 0 {
				value.Set("fragmentEnd", raw(fragment))
			}
			value.Set("depth", raw(nextDepth))
		} else if truthy(meta.Get("nextCursor")) {
			value = object("version", 1, "sessionId", id, "baseCursor", meta.Get("nextCursor"), "depth", nextDepth)
		}
		if value.IsObject() {
			b, _ := value.MarshalJSON()
			next = raw(base64.RawURLEncoding.EncodeToString(b))
		}
	}
	meta = clone(meta)
	meta.Set("nextCursor", next)
	meta.Set("hasMore", raw(string(next) != "null"))
	out := object("messages", projected, "turnsIncluded", turns, "truncated", truncated, "page", meta)
	if limitReached {
		out.Set("pageLimitReached", raw(true))
	}
	out.Set("historyComplete", raw(string(next) == "null" && isTrue(meta.Get("historyComplete")) && !truncated))
	return out, nil
}
