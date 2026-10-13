// Package toolresultstorage preserves Source's per-tool output persistence.
// The session owner supplies its actual transcript directory and file writes.
package toolresultstorage

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type Files struct {
	Directory      string
	MkdirAll       func(string) error
	WriteExclusive func(string, []byte) error
}

var ErrRequiresPersistence = errors.New("large tool output requires the actual session directory and exclusive writer")

func Threshold(name string, declared float64, overrides protocol.JSONObject) float64 {
	// Read's Infinity opt-out precedes any feature override in Source.
	if math.IsInf(declared, 0) || math.IsNaN(declared) {
		return declared
	}
	if raw := overrides.Get(name); raw != nil {
		if v, err := strconv.ParseFloat(string(raw), 64); err == nil && v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			return v
		}
	}
	return math.Min(declared, 50000)
}

func quote(text string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text)))
}

// Process uses JavaScript UTF-16 lengths, retains image-bearing content, and
// keeps the original result if optional file persistence fails.
func Process(block protocol.JSONObject, name string, threshold float64, files Files) (protocol.JSONObject, error) {
	if block.String("type") != "tool_result" || name == "" {
		return protocol.JSONObject{}, errors.New("tool output requires a mapped tool_result and its tool name")
	}
	content := block.Get("content")
	empty, size, isArray, textOnly := true, 0, false, true
	var units []uint16
	if content != nil && !bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		if len(content) > 0 && content[0] == '[' {
			isArray = true
			var parts []protocol.JSONObject
			if err := json.Unmarshal(content, &parts); err != nil {
				return protocol.JSONObject{}, err
			}
			for _, p := range parts {
				if p.String("type") == "image" {
					return block, nil
				}
				if p.String("type") != "text" {
					empty, textOnly = false, false
					continue
				}
				u, err := protocol.DecodeJavaScriptJSONString(p.Get("text"))
				if err != nil {
					continue
				}
				size += len(u)
				for _, c := range u {
					if !protocol.IsJavaScriptWhitespace(rune(c)) {
						empty = false
						break
					}
				}
			}
		} else {
			var err error
			units, err = protocol.DecodeJavaScriptJSONString(content)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			size = len(units)
			for _, c := range units {
				if !protocol.IsJavaScriptWhitespace(rune(c)) {
					empty = false
					break
				}
			}
		}
	}
	if empty {
		result := block.Clone()
		result.Set("content", quote("("+name+" completed with no output)"))
		return result, nil
	}
	if float64(size) <= threshold || !textOnly {
		return block, nil
	}
	if files.Directory == "" || files.MkdirAll == nil || files.WriteExclusive == nil {
		return protocol.JSONObject{}, ErrRequiresPersistence
	}
	ext := ".txt"
	if isArray {
		ext = ".json"
		normal, err := protocol.ParseAndStringifyJavaScriptJSON(content)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var pretty bytes.Buffer
		if err = json.Indent(&pretty, normal, "", "  "); err != nil {
			return protocol.JSONObject{}, err
		}
		units = utf16.Encode([]rune(pretty.String()))
	}
	idUnits, err := protocol.DecodeJavaScriptJSONString(block.Get("tool_use_id"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	path := filepath.Join(files.Directory, string(utf16.Decode(idUnits))+ext)
	// Source ignores mkdir failure and still attempts the exclusive write.
	_ = files.MkdirAll(files.Directory)
	if err = files.WriteExclusive(path, []byte(string(utf16.Decode(units)))); err != nil {
		return block, nil
	}
	cut := len(units)
	more := cut > 2000
	if more {
		cut = 2000
		last := -1
		for i, c := range units[:cut] {
			if c == '\n' {
				last = i
			}
		}
		if last > 1000 {
			cut = last
		}
	}
	suffix := "\n"
	if more {
		suffix = "\n...\n"
	}
	message, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("<persisted-output>\nOutput too large (" + toolschema.FormatFileSize(float64(len(units))) + "). Full output saved to: " + path + "\n\nPreview (first " + toolschema.FormatFileSize(2000) + "):\n"), protocol.QuoteJavaScriptUTF16(units[:cut]), quote(suffix + "</persisted-output>")}, "")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	result := block.Clone()
	result.Set("content", message)
	return result, nil
}
