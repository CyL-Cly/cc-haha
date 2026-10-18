package builtintools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type editable struct {
	Content, Encoding, Endings string
	Exists                     bool
	Mtime                      float64
}

func editMetadata(p string) (editable, error) {
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return editable{Encoding: "utf8", Endings: "LF"}, nil
	}
	if e != nil {
		return editable{}, e
	}
	stamp, e := mtime(p)
	if e != nil {
		return editable{}, e
	}
	encoding := "utf8"
	content := string(protocol.DecodeJavaScriptUTF8(b))
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		encoding = "utf16le"
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[i*2:])
		}
		content = string(utf16.Decode(u))
	}
	sample := content
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	crlf := strings.Count(sample, "\r\n")
	lf := strings.Count(sample, "\n") - crlf
	endings := "LF"
	if crlf > lf {
		endings = "CRLF"
	}
	return editable{strings.ReplaceAll(content, "\r\n", "\n"), encoding, endings, true, stamp}, nil
}
func normalizeQuotes(v string) string {
	return strings.NewReplacer("‘", "'", "’", "'", "“", "\"", "”", "\"").Replace(v)
}
func actualString(file, search string) string {
	if search == "" {
		return ""
	}
	normalFile, normalSearch := normalizeQuotes(file), normalizeQuotes(search)
	indented := regexp.MustCompile(`^[ \t]+\S`).MatchString(normalSearch)
	if indented {
		r := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(normalSearch))
		if pos := r.FindStringIndex(normalFile); pos != nil {
			return runeSlice(file, normalFile, pos[0], pos[1])
		}
	} else {
		if strings.Contains(file, search) {
			return search
		}
		if n := strings.Index(normalFile, normalSearch); n >= 0 {
			return runeSlice(file, normalFile, n, n+len(normalSearch))
		}
	}
	lines := strings.Split(normalSearch, "\n")
	hasIndent := false
	for i, l := range lines {
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent > 0 && strings.TrimSpace(l) != "" {
			hasIndent = true
			lines[i] = `[ \t]+` + regexp.QuoteMeta(l[indent:])
		} else {
			lines[i] = regexp.QuoteMeta(l)
		}
	}
	if hasIndent {
		r := regexp.MustCompile(`(?m)^` + strings.Join(lines, "\n"))
		matches := r.FindAllStringIndex(normalFile, -1)
		if len(matches) == 1 {
			return runeSlice(file, normalFile, matches[0][0], matches[0][1])
		}
		if len(matches) > 1 {
			return ""
		}
	}
	if strings.Contains(file, search) {
		return search
	}
	if n := strings.Index(normalFile, normalSearch); n >= 0 {
		return runeSlice(file, normalFile, n, n+len(normalSearch))
	}
	return ""
}

// Quote normalization changes UTF-8 byte lengths, but preserves code-unit/rune
// positions. Map matched normalized offsets back to the original typography.
func runeSlice(original, normalized string, start, end int) string {
	a, b := len([]rune(normalized[:start])), len([]rune(normalized[:end]))
	return string([]rune(original)[a:b])
}
func preserveStyle(old, actual, new string) string {
	if old == actual {
		return new
	}
	hasDouble := strings.ContainsAny(actual, "“”")
	hasSingle := strings.ContainsAny(actual, "‘’")
	chars := []rune(new)
	for i, c := range chars {
		opening := i == 0 || strings.ContainsRune(" \t\n\r([{—–", chars[i-1])
		if c == '"' && hasDouble {
			if opening {
				chars[i] = '“'
			} else {
				chars[i] = '”'
			}
		}
		if c == '\'' && hasSingle {
			if opening {
				chars[i] = '‘'
			} else {
				chars[i] = '’'
			}
		}
	}
	new = string(chars)
	o, a, n := strings.Split(old, "\n"), strings.Split(actual, "\n"), strings.Split(new, "\n")
	if len(o) != len(a) || len(o) != len(n) {
		return new
	}
	for i, l := range n {
		oi := o[i][:len(o[i])-len(strings.TrimLeft(o[i], " \t"))]
		ai := a[i][:len(a[i])-len(strings.TrimLeft(a[i], " \t"))]
		ni := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if oi == ni && oi != ai {
			n[i] = ai + l[len(ni):]
		}
	}
	return strings.Join(n, "\n")
}
func (s *Service) validateEdit(in protocol.JSONObject, scope *cooperative.Scope, p string) error {
	old, e := text(in.Get("old_string"))
	if e != nil {
		return e
	}
	new, e := text(in.Get("new_string"))
	if e != nil {
		return e
	}
	if old == new {
		return errors.New("No changes to make: old_string and new_string are exactly the same.")
	}
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return e
	}
	deny, e := s.deniedPath(c, "Edit", p)
	if e != nil {
		return e
	}
	if deny {
		return errors.New("File is in a directory that is denied by your permission settings.")
	}
	if s.options.Platform == "windows" && (strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")) {
		// Matching deny rules is lexical. Never stat an UNC path before the
		// existing tool permission resolver admits it (Source FileEditTool).
		return nil
	}
	if filepath.Base(p) == "settings.json" && (strings.Contains(p, string(filepath.Separator)+".claude"+string(filepath.Separator)) || under(filepath.Join(s.options.Home, ".claude"), p)) {
		return capability("settings-file edit schema and permission validation")
	}
	f, e := native(s, scope, func() (editable, error) {
		st, e := os.Stat(p)
		if e == nil && st.Size() > 10*1024*1024 {
			return editable{}, fmt.Errorf("File is too large to edit (%s). Maximum editable file size is 10MB.", formatSize(st.Size()))
		}
		return editMetadata(p)
	})
	if e != nil {
		return e
	}
	data, e := f.Await(scope)
	if e != nil {
		return e
	}
	if !data.Exists {
		if old == "" {
			return nil
		}
		return fmt.Errorf("File does not exist. Note: your current working directory is %s.", s.options.CWD)
	}
	if old == "" {
		if strings.TrimSpace(data.Content) != "" {
			return errors.New("Cannot create new file - file already exists.")
		}
		return nil
	}
	if strings.HasSuffix(p, ".ipynb") {
		return errors.New("File is a Jupyter Notebook. Use the NotebookEdit tool to edit this file.")
	}
	state, ok, e := s.options.Cache.Get(scope, p)
	if e != nil {
		return e
	}
	if !ok || state.PartialView {
		return errors.New("File has not been read yet. Read it first before writing to it.")
	}
	if data.Mtime > state.Timestamp && !(state.Offset == nil && state.Limit == nil && stringEqual(state.Content, data.Content)) {
		return errors.New("File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.")
	}
	actual := actualString(data.Content, old)
	if actual == "" {
		return fmt.Errorf("String to replace not found in file.\nString: %s", old)
	}
	count := strings.Count(data.Content, actual)
	if count > 1 && !boolean(in, "replace_all", false) {
		return fmt.Errorf("Found %d matches of the string to replace, but replace_all is false. To replace all occurrences, set replace_all to true. To replace only one occurrence, please provide more context to uniquely identify the instance.\nString: %s", count, old)
	}
	return nil
}
func stringEqual(raw json.RawMessage, text string) bool {
	var v string
	return json.Unmarshal(raw, &v) == nil && v == text
}
func (s *Service) edit(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope, userModified bool) (json.RawMessage, error) {
	return s.editWithParent(ctx, in, scope, userModified, nil)
}
func (s *Service) editWithParent(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope, userModified bool, parent json.RawMessage) (json.RawMessage, error) {
	p, e := s.path(in, "file_path")
	if e != nil {
		return nil, e
	}
	if s.options.FileSkills != nil && !skillSimple(s.options.Environment["CLAUDE_CODE_SIMPLE"]) {
		future, e := s.options.FileSkills(ctx, scope, p)
		if e != nil {
			return nil, e
		}
		if _, e := future.Await(scope); e != nil {
			return nil, e
		}
	}
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return nil, e
	}
	if !taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) && (!s.options.NonInteractive || taskEnvironmentTruthy(s.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"])) {
		if s.options.FileHistoryTrack == nil {
			return nil, capability("enabled actual fileHistoryTrackEdit for Edit")
		}
		if e := s.options.FileHistoryTrack(scope, p, parent); e != nil {
			return nil, e
		}
	}
	data, e := editMetadata(p)
	if e != nil {
		return nil, e
	}
	if data.Exists {
		state, ok, e := s.options.Cache.Get(scope, p)
		if e != nil {
			return nil, e
		}
		if !ok || data.Mtime > state.Timestamp {
			if !(ok && state.Offset == nil && state.Limit == nil && stringEqual(state.Content, data.Content)) {
				return nil, errors.New("File has been unexpectedly modified. Read it again before attempting to write it.")
			}
		}
	}
	old, e := text(in.Get("old_string"))
	if e != nil {
		return nil, e
	}
	new, e := text(in.Get("new_string"))
	if e != nil {
		return nil, e
	}
	actual := actualString(data.Content, old)
	if actual == "" {
		actual = old
	}
	replacement := preserveStyle(old, actual, new)
	replaceAll := boolean(in, "replace_all", false)
	updated := replacement
	if old != "" {
		n := 1
		if replaceAll {
			n = -1
		}
		updated = strings.Replace(data.Content, actual, replacement, n)
	}
	if updated == data.Content {
		return nil, errors.New("String not found in file. Failed to apply edit.")
	}
	patch := builtinwrite.StructuredPatch(convertTabs(data.Content), convertTabs(updated))
	toWrite := updated
	if data.Endings == "CRLF" {
		toWrite = strings.ReplaceAll(strings.ReplaceAll(updated, "\r\n", "\n"), "\n", "\r\n")
	}
	bytes := []byte(toWrite)
	if data.Encoding == "utf16le" {
		u := utf16.Encode([]rune(toWrite))
		bytes = make([]byte, len(u)*2)
		for i, v := range u {
			binary.LittleEndian.PutUint16(bytes[i*2:], v)
		}
	}
	// Source's read/staleness/replace section has no await. Use the existing
	// flushed file writer and preserve encoding, mode and symlink target.
	target := resolved(p)
	if e := (builtinwrite.OSFiles{}).WriteFlush(target, bytes); e != nil {
		return nil, e
	}
	if s.options.LSP != nil {
		s.options.LSP.NotifyChange(p, quote(updated))
	}
	stamp, e := mtime(p)
	if e != nil {
		return nil, e
	}
	if e = s.options.Cache.Set(scope, p, builtinwrite.FileState{Content: quote(updated), Timestamp: stamp}); e != nil {
		return nil, e
	}
	if s.options.ReportEvent != nil {
		oldUnits, e := protocol.DecodeJavaScriptJSONString(in.Get("old_string"))
		if e != nil {
			return nil, e
		}
		newUnits, e := protocol.DecodeJavaScriptJSONString(in.Get("new_string"))
		if e != nil {
			return nil, e
		}
		for _, event := range analytics.EditEvents(p, oldUnits, newUnits, replaceAll) {
			if e = s.options.ReportEvent(scope, event); e != nil {
				return nil, e
			}
		}
	}
	return object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")}, field("oldString", actual), protocol.JSONField{Name: "newString", Value: in.Get("new_string")}, field("originalFile", data.Content), field("structuredPatch", patch), field("userModified", userModified), field("replaceAll", replaceAll)), nil
}
func convertTabs(v string) string {
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		n := len(l) - len(strings.TrimLeft(l, "\t"))
		lines[i] = strings.Repeat("  ", n) + l[n:]
	}
	return strings.Join(lines, "\n")
}
