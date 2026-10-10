package resumestate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

var uuidName = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const liteReadSize = 65536

func decimalPrefix(source string) float64 {
	units := utf16.Encode([]rune(source))
	for len(units) > 0 && jsWhitespace(units[0]) {
		units = units[1:]
	}
	source = string(utf16.Decode(units))
	end := 0
	if len(source) > 0 && (source[0] == '+' || source[0] == '-') {
		end++
	}
	start := end
	for end < len(source) && source[end] >= '0' && source[end] <= '9' {
		end++
	}
	if start == end {
		return 0
	}
	value, _ := strconv.ParseFloat(source[:end], 64)
	return value
}
func fmtEnrichment(scanned, filtered, visible int) string {
	return fmt.Sprintf("/resume: enriched %d sessions, %d filtered out, %d visible (0 remaining on disk)", scanned, filtered, visible)
}

// extract preserves the portable Source helper's pattern-loop priority.
func extract(source, name string, last bool) json.RawMessage {
	var result json.RawMessage
	for _, pattern := range []string{`"` + name + `":"`, `"` + name + `": "`} {
		start := 0
		for start < len(source) {
			relative := strings.Index(source[start:], pattern)
			if relative < 0 {
				break
			}
			begin := start + relative + len(pattern)
			end := begin
			for end < len(source) {
				if source[end] == '\\' {
					end += 2
					continue
				}
				if source[end] == '"' {
					value := source[begin:end]
					raw := json.RawMessage(`"` + value + `"`)
					if !json.Valid(raw) {
						raw = quote(value)
					}
					result = raw
					break
				}
				end++
			}
			if !last {
				if result != nil {
					return result
				}
				break
			}
			start = end + 1
		}
	}
	return result
}

// ReadHeadAndTail implements the portable Source reader's complete failure
// boundary: open/read/close faults clear both fields, and zero head bytes skip
// the tail read. The host supplies real filesystem access.
func ReadHeadAndTail(files sessionstore.FileSystem, path string, size int64) (head, tail string) {
	file, err := files.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() {
		if file.Close() != nil {
			head, tail = "", ""
		}
	}()
	buffer := make([]byte, liteReadSize)
	n, err := file.ReadAt(buffer, 0)
	if n == 0 || err != nil && !errors.Is(err, io.EOF) {
		return "", ""
	}
	head = string(protocol.DecodeJavaScriptUTF8(buffer[:n]))
	tail = head
	if size > liteReadSize {
		n, err = file.ReadAt(buffer, size-liteReadSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", ""
		}
		tail = string(protocol.DecodeJavaScriptUTF8(buffer[:n]))
	}
	return head, tail
}

type candidate struct {
	sid, path string
	stat      Stat
}

func (service *Service) continueLog() (protocol.JSONObject, error) {
	directory := service.projectDir()
	entries, err := service.options.Files.ReadDir(directory)
	if err != nil {
		return protocol.JSONObject{}, nil
	}
	values := []candidate{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !uuidName.MatchString(sid) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		stat, err := service.options.Stat(path)
		if err != nil {
			service.diagnostic("debug", "Failed to stat session file: "+path, nil)
			continue
		}
		values = append(values, candidate{sid, path, stat})
	}
	sort.SliceStable(values, func(left, right int) bool {
		if values[left].stat.Modified != values[right].stat.Modified {
			return values[left].stat.Modified > values[right].stat.Modified
		}
		return values[left].stat.Birth > values[right].stat.Birth
	})
	visible := []protocol.JSONObject{}
	for _, value := range values {
		head, tail := ReadHeadAndTail(service.options.Files, value.path, value.stat.Size)
		side := strings.Contains(head, `"isSidechain":true`) || strings.Contains(head, `"isSidechain": true`)
		team := extract(head, "teamName", false)
		if side {
			service.diagnostic("debug", "Session "+value.sid+" filtered from /resume: isSidechain=true", nil)
			continue
		}
		if truthy(team) {
			service.diagnostic("debug", "Session "+value.sid+" filtered from /resume: teamName="+text(team), nil)
			continue
		}
		log := protocol.NewJSONObject(protocol.JSONField{Name: "messages", Value: []byte("[]")}, protocol.JSONField{Name: "sessionId", Value: quote(value.sid)}, protocol.JSONField{Name: "fullPath", Value: quote(value.path)})
		var title json.RawMessage
		for _, source := range []struct{ data, key string }{{tail, "customTitle"}, {head, "customTitle"}, {tail, "aiTitle"}, {head, "aiTitle"}} {
			if title == nil {
				title = extract(source.data, source.key, true)
			}
		}
		log.Set("customTitle", title)
		for _, name := range []string{"tag", "prUrl", "prRepository"} {
			log.Set(name, extract(tail, name, true))
		}
		log.Set("agentSetting", extract(head, "agentSetting", false))
		var pr json.RawMessage
		prString := text(extract(tail, "prNumber", true))
		if prString != "" {
			if number := decimalPrefix(prString); number != 0 {
				pr = numeric(number)
			}
		}
		if pr == nil {
			index := strings.LastIndex(tail, `"prNumber":`)
			if index >= 0 {
				end := index + 25
				if end > len(tail) {
					end = len(tail)
				}
				prString = tail[index+11 : end]
				if number := decimalPrefix(prString); number > 0 {
					pr = numeric(number)
				}
			}
		}
		log.Set("prNumber", pr)
		visible = append(visible, log)
	}
	if filtered := len(values) - len(visible); filtered > 0 {
		service.diagnostic("debug", fmtEnrichment(len(values), filtered, len(visible)), nil)
	}
	if len(visible) == 0 {
		return protocol.JSONObject{}, nil
	}
	return visible[0], nil
}
