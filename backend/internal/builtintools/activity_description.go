package builtintools

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

var activityTaskID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,20}$`)

func activityTruncate(raw json.RawMessage) (json.RawMessage, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil, e
	}
	text := string(utf16.Decode(units))
	if ansi.StringWidth(text) <= 50 {
		return append(json.RawMessage(nil), raw...), nil
	}
	g := uniseg.NewGraphemes(text)
	width, offset := 0, 0
	for g.Next() {
		segment := g.Str()
		w := ansi.StringWidth(segment)
		if width+w > 49 {
			break
		}
		width += w
		offset += len(utf16.Encode([]rune(segment)))
	}
	return protocol.QuoteJavaScriptUTF16(append(append([]uint16{}, units[:offset]...), 0x2026)), nil
}

func activityPrefix(prefix string, raw json.RawMessage) (json.RawMessage, error) {
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote(prefix), raw}, "")
}

func activityDisplayPath(in protocol.JSONObject, key, home, cwd string) (json.RawMessage, error) {
	value := in.String(key)
	expanded := strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	if expanded == "~" {
		expanded = home
	} else if strings.HasPrefix(expanded, "~/") {
		expanded = filepath.Join(home, expanded[2:])
	} else if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(cwd, expanded)
	}
	expanded = norm.NFC.String(expanded)
	if relative, e := filepath.Rel(cwd, expanded); e == nil && relative != "." && !strings.HasPrefix(relative, "..") {
		return quote(relative), nil
	}
	if strings.HasPrefix(value, home+string(filepath.Separator)) {
		return quote("~" + value[len(home):]), nil
	}
	return append(json.RawMessage(nil), in.Get(key)...), nil
}

// NativeActivityDescription is the original built-in metadata producer used by
// foreground/background Agent trackers. Unknown or MCP tools have no descriptor.
// Callers first select from their actual executable catalogue.
func NativeActivityDescription(name string, in protocol.JSONObject, home, cwd, taskDirectory string) (json.RawMessage, bool, error) {
	var key, prefix, fallback string
	switch name {
	case "Agent":
		if in.Get("description") == nil || string(in.Get("description")) == "null" {
			return quote("Running task"), true, nil
		}
		return append(json.RawMessage(nil), in.Get("description")...), true, nil
	case "Bash", "PowerShell":
		if in.String("command") == "" {
			return quote("Running command"), true, nil
		}
		description := in.Get("description")
		var e error
		if description == nil || string(description) == "null" {
			description, e = activityTruncate(in.Get("command"))
			if e != nil {
				return nil, true, e
			}
		}
		description, e = activityPrefix("Running ", description)
		return description, true, e
	case "Read":
		key, prefix, fallback = "file_path", "Reading ", "Reading file"
	case "Write":
		key, prefix, fallback = "file_path", "Writing ", "Writing file"
	case "Edit":
		key, prefix, fallback = "file_path", "Editing ", "Editing file"
	case "NotebookEdit":
		key, prefix, fallback = "notebook_path", "Editing notebook ", "Editing notebook"
	case "Glob":
		key, prefix, fallback = "pattern", "Finding ", "Finding files"
	case "Grep":
		key, prefix, fallback = "pattern", "Searching for ", "Searching"
	case "WebFetch":
		key, prefix, fallback = "url", "Fetching ", "Fetching web page"
	case "WebSearch":
		key, prefix, fallback = "query", "Searching for ", "Searching the web"
	default:
		return nil, false, nil
	}
	if in.String(key) == "" {
		return quote(fallback), true, nil
	}
	var summary json.RawMessage
	var e error
	if key == "file_path" || key == "notebook_path" {
		value := in.String(key)
		if name == "Read" && strings.HasPrefix(value, taskDirectory+string(filepath.Separator)) && strings.HasSuffix(value, ".output") {
			id := strings.TrimSuffix(strings.TrimPrefix(value, taskDirectory+string(filepath.Separator)), ".output")
			if activityTaskID.MatchString(id) {
				summary = quote(id)
			}
		}
		if summary == nil {
			summary, e = activityDisplayPath(in, key, home, cwd)
		}
	} else {
		summary, e = activityTruncate(in.Get(key))
	}
	if e != nil {
		return nil, true, e
	}
	summary, e = activityPrefix(prefix, summary)
	return summary, true, e
}
