package attachments

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"runtime"
	"unicode/utf16"
)

func diagnosticsContent(value protocol.JSONObject, options *PlanOptions) (json.RawMessage, bool, error) {
	var files []protocol.JSONObject
	if err := json.Unmarshal(value.Get("files"), &files); err != nil {
		return nil, false, malformed("attachment.files.map is not a function")
	}
	if len(files) == 0 {
		return nil, true, nil
	}
	env := map[string]string{}
	if options != nil && options.Environment != nil {
		env = options.Environment
	}
	unicode := env["TERM"] != "linux"
	if runtime.GOOS == "windows" {
		unicode = env["WT_SESSION"] != "" || env["TERMINUS_SUBLIME"] != "" || env["ConEmuTask"] == "{cmd::Cmder}" || env["TERM_PROGRAM"] == "Terminus-Sublime" || env["TERM_PROGRAM"] == "vscode" || env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm"
		for _, term := range []string{"xterm-256color", "alacritty", "rxvt-unicode", "rxvt-unicode-256color"} {
			unicode = unicode || env["TERM"] == term
		}
	}
	symbols := map[string]string{"Error": "✘", "Warning": "⚠", "Info": "ℹ", "Hint": "★"}
	if !unicode {
		symbols = map[string]string{"Error": "×", "Warning": "‼", "Info": "i", "Hint": "✶"}
	}
	parts := []json.RawMessage{}
	for i, file := range files {
		if i > 0 {
			parts = append(parts, quoted("\n\n"))
		}
		uri, err := exactString(file.Get("uri"))
		if err != nil {
			return nil, false, err
		}
		units, _ := protocol.DecodeJavaScriptJSONString(uri)
		index := -1
		for j, u := range units {
			if u == '/' {
				index = j
			}
		}
		name := units[index+1:]
		if len(name) == 0 {
			name = units
		}
		parts = append(parts, protocol.QuoteJavaScriptUTF16(name), quoted(":\n"))
		var diagnostics []protocol.JSONObject
		if err = json.Unmarshal(file.Get("diagnostics"), &diagnostics); err != nil {
			return nil, false, malformed("file.diagnostics.map is not a function")
		}
		for j, row := range diagnostics {
			if j > 0 {
				parts = append(parts, quoted("\n"))
			}
			symbol := symbols[row.String("severity")]
			if symbol == "" {
				symbol = "●"
			}
			rangeValue, _ := object(row.Get("range"))
			start, _ := object(rangeValue.Get("start"))
			number := func(key string) float64 { var n float64; _ = json.Unmarshal(start.Get(key), &n); return n + 1 }
			message, err := protocol.JavaScriptStringJSON(row.Get("message"))
			if err != nil {
				return nil, false, err
			}
			parts = append(parts, quoted(fmt.Sprintf("  %s [Line %v:%v] ", symbol, number("line"), number("character"))), message)
			if truthy(row.Get("code")) {
				code, e := protocol.JavaScriptStringJSON(row.Get("code"))
				if e != nil {
					return nil, false, e
				}
				parts = append(parts, quoted(" ["), code, quoted("]"))
			}
			if truthy(row.Get("source")) {
				source, e := protocol.JavaScriptStringJSON(row.Get("source"))
				if e != nil {
					return nil, false, e
				}
				parts = append(parts, quoted(" ("), source, quoted(")"))
			}
		}
	}
	summary, err := concatenate(parts...)
	if err != nil {
		return nil, false, err
	}
	units, _ := protocol.DecodeJavaScriptJSONString(summary)
	marker := utf16.Encode([]rune("…[truncated]"))
	if len(units) > 4000 {
		units = append(units[:4000-len(marker)], marker...)
	}
	content, err := concatenate(quoted("<new-diagnostics>The following new diagnostic issues were detected:\n\n"), protocol.QuoteJavaScriptUTF16(units), quoted("</new-diagnostics>"))
	return content, false, err
}
