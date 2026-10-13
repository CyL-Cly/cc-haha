package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These helpers operate on JavaScript UTF-16 strings. Notebook cells may contain
// lone surrogates, including inside a source array or an error traceback.
func notebookString(value json.RawMessage) (json.RawMessage, error) {
	return protocol.ExactJavaScriptStringJSON(value)
}
func notebookJoin(value json.RawMessage, separator string) (json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(value, &values); err != nil {
		return nil, err
	}
	var out taskText
	for i, item := range values {
		if i > 0 {
			out.add(separator)
		}
		if string(item) == "null" {
			continue
		}
		text, err := notebookString(item)
		if err != nil {
			return nil, err
		}
		out.value(text)
	}
	return protocol.QuoteJavaScriptUTF16(out), nil
}
func notebookText(value json.RawMessage) (json.RawMessage, error) {
	if !jsonTruthy(value) {
		return quote(""), nil
	}
	if len(value) > 0 && value[0] == '[' {
		return notebookJoin(value, "")
	}
	if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
		return nil, capability("Notebook nonstring output formatOutput")
	}
	return value, nil
}
func notebookIntEnvironment(value string, fallback, upper int) int {
	trimmed := strings.TrimLeft(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
	match := regexp.MustCompile(`^[+-]?[0-9]+`).FindString(trimmed)
	n, err := strconv.ParseFloat(match, 64)
	if (err != nil && !math.IsInf(n, 1)) || n <= 0 {
		return fallback
	}
	if upper > 0 && n > float64(upper) {
		return upper
	}
	if n >= float64(math.MaxInt) {
		return math.MaxInt
	}
	return int(n)
}

var notebookImageOutput = regexp.MustCompile(`(?i)^data:image/[a-z0-9.+_-]+;base64,`)

func (s *Service) notebookOutputText(value json.RawMessage) (json.RawMessage, error) {
	text, err := notebookText(value)
	if err != nil {
		return nil, err
	}
	u, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return nil, err
	}
	max := notebookIntEnvironment(s.options.Environment["BASH_MAX_OUTPUT_LENGTH"], 30000, 150000)
	if len(u) <= max || notebookImageOutput.MatchString(string(utf16.Decode(u))) {
		return text, nil
	}
	remainingLines := 1
	for _, c := range u[max:] {
		if c == '\n' {
			remainingLines++
		}
	}
	out := taskText(append([]uint16(nil), u[:max]...))
	out.add(fmt.Sprintf("\n\n... [%d lines truncated] ...", remainingLines))
	return protocol.QuoteJavaScriptUTF16(out), nil
}
func notebookImage(data protocol.JSONObject) json.RawMessage {
	for _, media := range []string{"image/png", "image/jpeg"} {
		value := data.Get(media)
		u, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			continue
		}
		clean := make([]uint16, 0, len(u))
		for _, c := range u {
			if strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", rune(c)) {
				continue
			}
			clean = append(clean, c)
		}
		return object(protocol.JSONField{Name: "image_data", Value: protocol.QuoteJavaScriptUTF16(clean)}, field("media_type", media))
	}
	return nil
}
func (s *Service) notebookOutput(output protocol.JSONObject) (json.RawMessage, error) {
	kind := output.String("output_type")
	var value, image json.RawMessage
	switch kind {
	case "stream":
		value = output.Get("text")
	case "execute_result", "display_data":
		var data protocol.JSONObject
		if output.Get("data") != nil && string(output.Get("data")) != "null" {
			if err := data.UnmarshalJSON(output.Get("data")); err != nil {
				return nil, capability("Notebook output data property access")
			}
			value, image = data.Get("text/plain"), notebookImage(data)
		}
	case "error":
		name, err := notebookString(output.Get("ename"))
		if err != nil {
			return nil, err
		}
		message, err := notebookString(output.Get("evalue"))
		if err != nil {
			return nil, err
		}
		trace, err := notebookJoin(output.Get("traceback"), "\n")
		if err != nil {
			return nil, capability("Notebook error traceback join")
		}
		var all taskText
		all.value(name)
		all.add(": ")
		all.value(message)
		all.add("\n")
		all.value(trace)
		value = protocol.QuoteJavaScriptUTF16(all)
	default:
		return json.RawMessage("null"), nil
	}
	text, err := s.notebookOutputText(value)
	if err != nil {
		return nil, err
	}
	return object(field("output_type", kind), protocol.JSONField{Name: "text", Value: text}, protocol.JSONField{Name: "image", Value: image}), nil
}
func (s *Service) notebookReadCells(notebook protocol.JSONObject) (json.RawMessage, error) {
	var metadata, info protocol.JSONObject
	if notebook.Get("metadata") == nil || string(notebook.Get("metadata")) == "null" {
		return nil, capability("Notebook Read metadata property access")
	}
	if err := metadata.UnmarshalJSON(notebook.Get("metadata")); err != nil {
		return nil, err
	}
	_ = info.UnmarshalJSON(metadata.Get("language_info"))
	language := info.Get("name")
	if language == nil || string(language) == "null" {
		language = quote("python")
	}
	var cells []protocol.JSONObject
	if err := json.Unmarshal(notebook.Get("cells"), &cells); err != nil {
		return nil, capability("Notebook Read cells map")
	}
	if string(notebook.Get("cells")) == "null" {
		return nil, capability("Notebook Read cells map")
	}
	processed := make([]json.RawMessage, 0, len(cells))
	for index, cell := range cells {
		id := cell.Get("id")
		if id == nil || string(id) == "null" {
			id = quote(fmt.Sprintf("cell-%d", index))
		}
		source := cell.Get("source")
		if len(source) > 0 && source[0] == '[' {
			var err error
			source, err = notebookJoin(source, "")
			if err != nil {
				return nil, err
			}
		}
		var execution json.RawMessage
		if cell.String("cell_type") == "code" && jsonTruthy(cell.Get("execution_count")) {
			execution = cell.Get("execution_count")
		}
		result := protocol.NewJSONObject(protocol.JSONField{Name: "cellType", Value: cell.Get("cell_type")}, protocol.JSONField{Name: "source", Value: source}, protocol.JSONField{Name: "execution_count", Value: execution}, protocol.JSONField{Name: "cell_id", Value: id})
		if cell.String("cell_type") == "code" {
			result.Set("language", language)
			if cell.Get("outputs") != nil && string(cell.Get("outputs")) != "null" {
				var outputs []protocol.JSONObject
				if err := json.Unmarshal(cell.Get("outputs"), &outputs); err != nil {
					return nil, capability("Notebook Read outputs map")
				}
				if len(outputs) > 0 {
					out := make([]json.RawMessage, 0, len(outputs))
					size := 0
					for _, output := range outputs {
						value, err := s.notebookOutput(output)
						if err != nil {
							return nil, err
						}
						out = append(out, value)
						if string(value) != "null" {
							var record, image protocol.JSONObject
							_ = record.UnmarshalJSON(value)
							u, _ := protocol.DecodeJavaScriptJSONString(record.Get("text"))
							size += len(u)
							_ = image.UnmarshalJSON(record.Get("image"))
							u, _ = protocol.DecodeJavaScriptJSONString(image.Get("image_data"))
							size += len(u)
						}
					}
					if size > 10000 {
						out = []json.RawMessage{object(field("output_type", "stream"), field("text", fmt.Sprintf("Outputs are too large to include. Use Bash with: cat <notebook_path> | jq '.cells[%d].outputs'", index)))}
					}
					result.Set("outputs", raw(out))
				}
			}
		}
		processed = append(processed, raw(result))
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw(processed))
}

type notebookReadCompletion struct {
	cells     json.RawMessage
	timestamp float64
	unchanged bool
}

func (s *Service) startNotebookRead(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope, p string) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	offset, limit := number(in, "offset", 1), number(in, "limit", 0)
	hasLimit := in.Get("limit") != nil
	old, ok, err := s.options.Cache.Get(scope, p)
	if err != nil {
		return nil, err
	}
	dedup := ok && !old.PartialView && old.Offset != nil && *old.Offset == float64(offset) && ((old.Limit == nil && !hasLimit) || (old.Limit != nil && hasLimit && *old.Limit == float64(limit)))
	future, err := native(s, scope, func() (notebookReadCompletion, error) {
		if err := context.Cause(ctx); err != nil {
			return notebookReadCompletion{}, err
		}
		if dedup {
			if stamp, err := mtime(p); err == nil && stamp == old.Timestamp {
				return notebookReadCompletion{unchanged: true}, nil
			}
		}
		bytes, err := os.ReadFile(p)
		if err != nil {
			return notebookReadCompletion{}, err
		}
		var notebook protocol.JSONObject
		if err := notebook.UnmarshalJSON([]byte(string(protocol.DecodeJavaScriptUTF8(bytes)))); err != nil {
			return notebookReadCompletion{}, errors.New("Notebook is not valid JSON.")
		}
		cells, err := s.notebookReadCells(notebook)
		if err != nil {
			return notebookReadCompletion{}, err
		}
		if len(cells) > 256*1024 {
			return notebookReadCompletion{}, fmt.Errorf("Notebook content (%s) exceeds maximum allowed size (256KB). Use Bash with jq to read specific portions:\n  cat \"%s\" | jq '.cells[:20]' # First 20 cells\n  cat \"%s\" | jq '.cells[100:120]' # Cells 100-120\n  cat \"%s\" | jq '.cells | length' # Count total cells\n  cat \"%s\" | jq '.cells[] | select(.cell_type==\"code\") | .source' # All code sources", formatSize(int64(len(cells))), in.String("file_path"), in.String("file_path"), in.String("file_path"), in.String("file_path"))
		}
		stamp, err := mtime(p)
		return notebookReadCompletion{cells: cells, timestamp: stamp}, err
	})
	if err != nil {
		return nil, err
	}
	return transform(s, scope, future, func(next *cooperative.Scope, data notebookReadCompletion) (json.RawMessage, error) {
		if data.unchanged {
			if s.options.ReportEvent != nil {
				if e := s.options.ReportEvent(next, analytics.ReadDedupEvent(p)); e != nil {
					return nil, e
				}
			}
			return object(field("type", "file_unchanged"), protocol.JSONField{Name: "file", Value: object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")})}), nil
		}
		maxTokens := notebookIntEnvironment(s.options.Environment["CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS"], 25000, 0)
		if err := s.validateReadTokens(ctx, next, string(data.cells), 4, float64(maxTokens)); err != nil {
			return nil, err
		}
		o := float64(offset)
		var l *float64
		if hasLimit {
			value := float64(limit)
			l = &value
		}
		if err := s.options.Cache.Set(next, p, builtinwrite.FileState{Content: quote(string(data.cells)), Timestamp: data.timestamp, Offset: &o, Limit: l}); err != nil {
			return nil, err
		}
		if s.options.ReportEvent != nil {
			content := utf16.Encode([]rune(string(data.cells)))
			if e := s.options.ReportEvent(next, analytics.FileOperationEvent(analytics.FileOperation{Operation: "read", Tool: "FileReadTool", Path: utf16.Encode([]rune(p)), Content: &content})); e != nil {
				return nil, e
			}
		}
		return object(field("type", "notebook"), protocol.JSONField{Name: "file", Value: object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")}, protocol.JSONField{Name: "cells", Value: data.cells})}), nil
	})
}
func mapNotebookRead(file protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, error) {
	var cells []protocol.JSONObject
	if err := json.Unmarshal(file.Get("cells"), &cells); err != nil {
		return protocol.JSONObject{}, err
	}
	blocks := []protocol.JSONObject{}
	add := func(block protocol.JSONObject) {
		if len(blocks) > 0 && blocks[len(blocks)-1].String("type") == "text" && block.String("type") == "text" {
			var content taskText
			content.value(blocks[len(blocks)-1].Get("text"))
			content.add("\n")
			content.value(block.Get("text"))
			blocks[len(blocks)-1].Set("text", protocol.QuoteJavaScriptUTF16(content))
		} else {
			blocks = append(blocks, block)
		}
	}
	for _, cell := range cells {
		var content taskText
		cellID, err := notebookString(cell.Get("cell_id"))
		if err != nil {
			return protocol.JSONObject{}, err
		}
		content.add("<cell id=\"")
		content.value(cellID)
		content.add("\">")
		if cell.String("cellType") != "code" {
			kind, err := notebookString(cell.Get("cellType"))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			content.add("<cell_type>")
			content.value(kind)
			content.add("</cell_type>")
		} else if !jsonSame(cell.Get("language"), quote("python")) {
			language, err := notebookString(cell.Get("language"))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			content.add("<language>")
			content.value(language)
			content.add("</language>")
		}
		source, err := notebookString(cell.Get("source"))
		if err != nil {
			return protocol.JSONObject{}, err
		}
		content.value(source)
		content.add("</cell id=\"")
		content.value(cellID)
		content.add("\">")
		add(protocol.NewJSONObject(protocol.JSONField{Name: "text", Value: protocol.QuoteJavaScriptUTF16(content)}, field("type", "text")))
		var outputs []json.RawMessage
		_ = json.Unmarshal(cell.Get("outputs"), &outputs)
		for _, output := range outputs {
			if string(output) == "null" {
				continue
			}
			var record protocol.JSONObject
			if err := record.UnmarshalJSON(output); err != nil {
				return protocol.JSONObject{}, err
			}
			if jsonTruthy(record.Get("text")) {
				var out taskText
				out.add("\n")
				out.value(record.Get("text"))
				add(protocol.NewJSONObject(protocol.JSONField{Name: "text", Value: protocol.QuoteJavaScriptUTF16(out)}, field("type", "text")))
			}
			if jsonTruthy(record.Get("image")) {
				var image protocol.JSONObject
				if err := image.UnmarshalJSON(record.Get("image")); err != nil {
					return protocol.JSONObject{}, err
				}
				add(protocol.NewJSONObject(field("type", "image"), protocol.JSONField{Name: "source", Value: object(protocol.JSONField{Name: "data", Value: image.Get("image_data")}, protocol.JSONField{Name: "media_type", Value: image.Get("media_type")}, field("type", "base64"))}))
			}
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: raw(blocks)}), nil
}
