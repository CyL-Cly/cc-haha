package sessionrewind

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type transcriptChange struct {
	path, absolute, identity string
	additions, deletions     int
	diff                     string
}
type transcriptEvidence struct {
	confirmed, uncertain []transcriptChange
	unverified           []string
	failure              error
}

func primitiveKey(raw json.RawMessage) string {
	if raw == nil {
		return "undefined"
	}
	if value, ok := stringValue(raw); ok {
		return "string:" + value
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "number:" + strconv.FormatFloat(number, 'g', -1, 64)
	}
	return string(raw)
}

func stringValue(raw json.RawMessage) (string, bool) {
	var result string
	err := json.Unmarshal(raw, &result)
	return result, err == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && raw != nil
}
func nullish(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if v != nil && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return v
		}
	}
	return nil
}
func (s *Service) toolPath(raw json.RawMessage, base string) (transcriptChange, bool) {
	path, ok := stringValue(raw)
	if !ok || strings.TrimSpace(path) == "" {
		return transcriptChange{}, false
	}
	abs := s.absolute(base, path)
	rel, err := filepath.Rel(s.absolute(base, "."), abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		path = strings.TrimLeft(filepath.ToSlash(rel), "/")
	} else {
		path = filepath.ToSlash(abs)
	}
	return transcriptChange{path: path, absolute: abs, identity: s.identity(abs, false)}, true
}

var transcriptLineSplit = regexp.MustCompile(`\r\n|\r|\n`)
var patchHeader = regexp.MustCompile(`^\*\*\* (?:(?:Add|Update|Delete) File:|Move to:) (.+)$`)

func countTranscriptLines(value string) int {
	if value == "" {
		return 0
	}
	lines := transcriptLineSplit.Split(value, -1)
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return len(lines)
}
func transcriptDiff(oldPath, newPath, oldContent, newContent string) string {
	lines := func(value string) []string {
		if value == "" {
			return nil
		}
		result := strings.Split(value, "\n")
		if result[len(result)-1] == "" {
			result = result[:len(result)-1]
		}
		return result
	}
	old, new := lines(oldContent), lines(newContent)
	oldName := "a/" + oldPath
	if oldPath == "/dev/null" {
		oldName = oldPath
	}
	result := []string{"diff --session a/" + oldPath + " b/" + newPath, "--- " + oldName, "+++ b/" + newPath, fmtHunk(len(old), len(new))}
	for _, line := range old {
		result = append(result, "-"+line)
	}
	for _, line := range new {
		result = append(result, "+"+line)
	}
	return strings.Join(result, "\n")
}
func editChange(path transcriptChange, input protocol.JSONObject) transcriptChange {
	old, new := text(input, "old_string"), text(input, "new_string")
	path.additions = countTranscriptLines(new)
	path.deletions = countTranscriptLines(old)
	path.diff = transcriptDiff(path.path, path.path, old, new)
	return path
}
func (s *Service) extract(name string, input protocol.JSONObject, base string) []transcriptChange {
	path, ok := s.toolPath(nullish(input.Get("file_path"), input.Get("path")), base)
	switch strings.ToLower(name) {
	case "write":
		if !ok {
			return nil
		}
		content := text(input, "content")
		path.additions = countTranscriptLines(content)
		path.diff = transcriptDiff("/dev/null", path.path, "", content)
		return []transcriptChange{path}
	case "edit":
		if !ok {
			return nil
		}
		return []transcriptChange{editChange(path, input)}
	case "multiedit":
		if !ok {
			return nil
		}
		var edits []json.RawMessage
		if json.Unmarshal(input.Get("edits"), &edits) != nil {
			return nil
		}
		result := []transcriptChange{}
		for _, raw := range edits {
			item := object(raw)
			if item.IsObject() || strings.HasPrefix(string(raw), "[") {
				result = append(result, editChange(path, item))
			}
		}
		return result
	case "notebookedit":
		path, ok = s.toolPath(nullish(input.Get("notebook_path"), input.Get("file_path"), input.Get("path")), base)
		if !ok {
			return nil
		}
		old, new := text(input, "old_source"), text(input, "new_source")
		path.additions = countTranscriptLines(new)
		path.deletions = countTranscriptLines(old)
		path.diff = transcriptDiff(path.path, path.path, old, new)
		return []transcriptChange{path}
	case "apply_patch":
		patch, ok := stringValue(input.Get("patch"))
		if !ok {
			return nil
		}
		result := []transcriptChange{}
		for _, line := range strings.Split(patch, "\n") {
			m := patchHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			raw, _ := json.Marshal(m[1])
			if path, ok := s.toolPath(raw, base); ok {
				result = append(result, path)
			}
		}
		return result
	}
	return nil
}
func results(messages []protocol.JSONObject) (map[string]bool, map[string]bool) {
	success, failed := map[string]bool{}, map[string]bool{}
	for _, message := range messages {
		if text(message, "type") != "tool_result" {
			continue
		}
		for _, block := range blocks(message) {
			id, ok := stringValue(block.Get("tool_use_id"))
			if text(block, "type") != "tool_result" || !ok {
				continue
			}
			if string(block.Get("is_error")) == "true" {
				failed[id] = true
			} else {
				success[id] = true
			}
		}
	}
	for id := range failed {
		delete(success, id)
	}
	return success, failed
}
func knownMutation(name string) bool {
	switch strings.ToLower(name) {
	case "write", "edit", "multiedit", "notebookedit", "apply_patch":
		return true
	}
	return false
}
func (s *Service) nonMutating(name string, input protocol.JSONObject) (bool, error) {
	switch strings.ToLower(name) {
	case "agent", "askuserquestion", "enterplanmode", "exitplanmode", "glob", "grep", "read", "skill", "sleep", "task", "taskget", "tasklist", "taskupdate", "todowrite", "toolsearch", "webfetch", "websearch":
		return true, nil
	case "bash":
		if units, err := protocol.DecodeJavaScriptJSONString(input.Get("command")); err == nil {
			for i := 0; i < len(units); i++ {
				if units[i] >= 0xd800 && units[i] <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
					i++
					continue
				}
				if units[i] >= 0xd800 && units[i] <= 0xdfff {
					return false, &UnsupportedRecordedCommand{"lone UTF-16 command value"}
				}
			}
		}
		command, ok := stringValue(input.Get("command"))
		if !ok {
			return false, nil
		}
		if s.options.ClassifyRecordedCommand != nil {
			return s.options.ClassifyRecordedCommand(command)
		}
		return s.options.RecordedCommandIsReadOnly(command), nil
	}
	return false, nil
}
func normalizedSources(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	sort.Slice(result, func(a, b int) bool {
		x, y := utf16.Encode([]rune(result[a])), utf16.Encode([]rune(result[b]))
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				return x[i] < y[i]
			}
		}
		return len(x) < len(y)
	})
	if len(result) > 8 {
		result = result[:8]
	}
	return result
}
func (s *Service) collect(messages []protocol.JSONObject, base string, frozen bool) transcriptEvidence {
	confirmed, uncertain := map[string]transcriptChange{}, map[string]transcriptChange{}
	order := map[string]int{}
	sequence := 0
	success, failed := results(messages)
	seen := map[string]bool{}
	unverified := []string{}
	for _, message := range messages {
		if text(message, "type") != "tool_use" {
			continue
		}
		for _, block := range blocks(message) {
			name, ok := stringValue(block.Get("name"))
			id, idOK := stringValue(block.Get("id"))
			if text(block, "type") != "tool_use" || !ok || !idOK || seen[id] {
				continue
			}
			seen[id] = true
			input := object(block.Get("input"))
			nonMutating, classifyError := s.nonMutating(name, input)
			if classifyError != nil {
				return transcriptEvidence{failure: classifyError}
			}
			inputObject := input.IsObject() || strings.HasPrefix(strings.TrimSpace(string(block.Get("input"))), "[")
			if failed[id] || !inputObject {
				if !nonMutating {
					unverified = append(unverified, name)
				}
				continue
			}
			if nonMutating {
				continue
			}
			if !knownMutation(name) {
				unverified = append(unverified, name)
				continue
			}
			cwd := nullishCWD(message, base)
			changes := s.extract(name, input, cwd)
			if len(changes) == 0 {
				unverified = append(unverified, name)
			}
			target := uncertain
			if success[id] {
				target = confirmed
			}
			for _, item := range changes {
				if frozen {
					item.identity = s.identity(item.absolute, true)
				}
				if old, ok := target[item.identity]; ok {
					old.additions += item.additions
					old.deletions += item.deletions
					if old.diff != "" && item.diff != "" {
						old.diff += "\n" + item.diff
					} else {
						old.diff += item.diff
					}
					target[item.identity] = old
				} else {
					target[item.identity] = item
					order[item.identity] = sequence
					sequence++
				}
			}
		}
	}
	comparator := collate.New(language.English)
	sorted := func(m map[string]transcriptChange) []transcriptChange {
		result := []transcriptChange{}
		for _, item := range m {
			result = append(result, item)
		}
		sort.SliceStable(result, func(a, b int) bool {
			cmp := comparator.CompareString(result[a].path, result[b].path)
			if cmp == 0 {
				return order[result[a].identity] < order[result[b].identity]
			}
			return cmp < 0
		})
		return result
	}
	return transcriptEvidence{confirmed: sorted(confirmed), uncertain: sorted(uncertain), unverified: normalizedSources(unverified)}
}
func (s *Service) replay(messages []protocol.JSONObject, base, identity string, before fileText, frozen bool) fileText {
	content := before
	success, _ := results(messages)
	for _, message := range messages {
		if text(message, "type") != "tool_use" {
			continue
		}
		for _, tool := range blocks(message) {
			name, id := text(tool, "name"), text(tool, "id")
			input := object(tool.Get("input"))
			if text(tool, "type") != "tool_use" || id == "" || !success[id] || name == "" || !input.IsObject() {
				continue
			}
			cwd := nullishCWD(message, base)
			matches := false
			for _, item := range s.extract(name, input, cwd) {
				if s.identity(item.absolute, frozen) == identity {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			if strings.EqualFold(name, "write") {
				if value, ok := stringValue(input.Get("content")); ok {
					content = fileText{true, true, value}
					continue
				}
			}
			var edits []json.RawMessage
			if strings.EqualFold(name, "edit") {
				raw, _ := input.MarshalJSON()
				edits = []json.RawMessage{raw}
			} else if strings.EqualFold(name, "multiedit") {
				_ = json.Unmarshal(input.Get("edits"), &edits)
			}
			if edits == nil {
				content = fileText{}
				continue
			}
			for _, raw := range edits {
				edit := object(raw)
				old, oldOK := stringValue(edit.Get("old_string"))
				new, newOK := stringValue(edit.Get("new_string"))
				if !edit.IsObject() || !oldOK || !newOK {
					content = fileText{}
					break
				}
				if old == "" {
					content = fileText{true, true, new}
					continue
				}
				all := string(edit.Get("replace_all")) == "true"
				if !content.defined || !content.exists || !strings.Contains(content.content, old) || (!all && strings.Index(content.content, old) != strings.LastIndex(content.content, old)) {
					content = fileText{}
					break
				}
				content.content = applyLiteralEdit(content.content, old, new, all)
			}
		}
	}
	return content
}
