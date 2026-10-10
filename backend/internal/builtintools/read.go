package builtintools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
)

const unchanged = "File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current — refer to that instead of re-reading."
const mitigation = "\n\n<system-reminder>\nWhenever you read a file, you should consider whether it would be considered malware. You CAN and SHOULD provide analysis of malware, what it is doing. But you MUST refuse to improve or augment the code. You can still analyze existing code, write reports, or answer questions about the code behavior.\n</system-reminder>\n"

type textRead struct {
	Content               string
	Lines, Total          int
	Mtime                 float64
	TotalBytes, ReadBytes int
}

func readRange(ctx context.Context, p string, offset, limit int, explicitLimit bool) (textRead, error) {
	f, e := os.Open(p)
	if e != nil {
		return textRead{}, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return textRead{}, e
	}
	if st.IsDir() {
		return textRead{}, fmt.Errorf("EISDIR: illegal operation on a directory, read '%s'", p)
	}
	if !explicitLimit && st.Size() > 256*1024 {
		return textRead{}, fileTooLarge(st.Size())
	}
	r := bufio.NewReader(f)
	selected := []string{}
	index := 0
	totalBytes := 0
	decodedBytes := 0
	for {
		if e := context.Cause(ctx); e != nil {
			return textRead{}, e
		}
		line, e := r.ReadString('\n')
		totalBytes += len(line)
		if !explicitLimit && totalBytes > 256*1024 {
			return textRead{}, fileTooLarge(int64(totalBytes))
		}
		line = string(protocol.DecodeJavaScriptUTF8([]byte(line)))
		if index == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		decodedBytes += len(line)
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if index >= offset && (!explicitLimit || index < offset+limit) {
			selected = append(selected, line)
		}
		index++
		if e == io.EOF {
			break
		}
		if e != nil {
			return textRead{}, e
		}
	}
	content := strings.Join(selected, "\n")
	return textRead{Content: content, Lines: len(selected), Total: index, Mtime: float64(st.ModTime().UnixMilli()), TotalBytes: decodedBytes, ReadBytes: len(content)}, nil
}
func fileTooLarge(n int64) error {
	return fmt.Errorf("File content (%s) exceeds maximum allowed size (256KB). Use offset and limit parameters to read specific portions of the file, or search for specific content instead of reading the whole file.", formatSize(n))
}

// Read validation follows Source's string-only checks. Filesystem access is
// deferred until the actual permission decision has been resolved.
func (s *Service) validateRead(in protocol.JSONObject, scope *cooperative.Scope, p string) error {
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return e
	}
	denied, e := s.matchPathRule(c, "alwaysDenyRules", "Read", p, false)
	if e != nil {
		return e
	}
	if denied {
		return fmt.Errorf("File is in a directory that is denied by your permission settings.")
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".bmp", ".ico", ".tiff", ".tif":
		return fmt.Errorf("This tool cannot read binary files. The file appears to be a binary %s file. Please use appropriate tools for binary file analysis.", ext)
	}
	if strings.Contains("|.mp4|.mov|.avi|.mkv|.webm|.wmv|.flv|.m4v|.mpeg|.mpg|.mp3|.wav|.ogg|.flac|.aac|.m4a|.wma|.aiff|.opus|.zip|.tar|.gz|.bz2|.7z|.rar|.xz|.z|.tgz|.iso|.exe|.dll|.so|.dylib|.bin|.o|.a|.obj|.lib|.app|.msi|.deb|.rpm|.doc|.docx|.xls|.xlsx|.ppt|.pptx|.odt|.ods|.odp|.ttf|.otf|.woff|.woff2|.eot|.pyc|.pyo|.class|.jar|.war|.ear|.node|.wasm|.rlib|.sqlite|.sqlite3|.db|.mdb|.idx|.psd|.ai|.eps|.sketch|.fig|.xd|.blend|.3ds|.max|.swf|.fla|.lockb|.dat|.data|", "|"+ext+"|") && ext != "" {
		return fmt.Errorf("This tool cannot read binary files. The file appears to be a binary %s file. Please use appropriate tools for binary file analysis.", ext)
	}
	switch p {
	case "/dev/zero", "/dev/random", "/dev/urandom", "/dev/full", "/dev/stdin", "/dev/tty", "/dev/console", "/dev/stdout", "/dev/stderr", "/dev/fd/0", "/dev/fd/1", "/dev/fd/2":
		return fmt.Errorf("Cannot read '%s': this device file would block or produce infinite output.", in.String("file_path"))
	}
	if strings.HasPrefix(p, "/proc/") && (strings.HasSuffix(p, "/fd/0") || strings.HasSuffix(p, "/fd/1") || strings.HasSuffix(p, "/fd/2")) {
		return fmt.Errorf("Cannot read '%s': this device file would block or produce infinite output.", in.String("file_path"))
	}
	return nil
}
func formatSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " bytes"
	}
	v, u := float64(n)/1024, "KB"
	if v >= 1024 {
		v /= 1024
		u = "MB"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + u
}
func (s *Service) read(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	future, e := s.startRead(ctx, in, scope)
	if e != nil {
		return nil, e
	}
	return future.Await(scope)
}

type readCompletion struct {
	text      textRead
	unchanged bool
}

func (s *Service) startRead(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	if s.options.FileSkills == nil || skillSimple(s.options.Environment["CLAUDE_CODE_SIMPLE"]) {
		return s.startReadBody(ctx, in, scope, true)
	}
	path, e := s.path(in, "file_path")
	if e != nil {
		return nil, e
	}
	old, exists, e := s.options.Cache.Get(scope, path)
	if e != nil {
		return nil, e
	}
	offset, limit := number(in, "offset", 1), number(in, "limit", 0)
	hasLimit := in.Get("limit") != nil
	dedup := exists && !old.PartialView && old.Offset != nil && int(*old.Offset) == offset && ((old.Limit == nil && !hasLimit) || old.Limit != nil && hasLimit && int(*old.Limit) == limit)
	output, e := ownerasync.NewOwnerFuture[json.RawMessage](s.options.Owner)
	if e != nil {
		return nil, e
	}
	discover := func(current *cooperative.Scope) error {
		pending, e := s.options.FileSkills(ctx, current, path)
		if e != nil {
			return output.Reject(current, e)
		}
		_, e = pending.Then(current, func(next *cooperative.Scope, _ struct{}, failure error) error {
			if failure != nil {
				return output.Reject(next, failure)
			}
			body, e := s.startReadBody(ctx, in, next, false)
			if e != nil {
				return output.Reject(next, e)
			}
			return output.ResolveFrom(next, body)
		})
		return e
	}
	if dedup {
		checked, e := native(s, scope, func() (bool, error) { stamp, e := mtime(path); return e == nil && stamp == old.Timestamp, nil })
		if e != nil {
			return nil, e
		}
		_, e = checked.Then(scope, func(next *cooperative.Scope, unchanged bool, failure error) error {
			if failure != nil {
				return output.Reject(next, failure)
			}
			if unchanged {
				if s.options.ReportEvent != nil {
					if e := s.options.ReportEvent(next, analytics.ReadDedupEvent(path)); e != nil {
						return output.Reject(next, e)
					}
				}
				return output.Resolve(next, object(field("type", "file_unchanged"), protocol.JSONField{Name: "file", Value: object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")})}))
			}
			return discover(next)
		})
		if e != nil {
			return nil, e
		}
	} else if e := discover(scope); e != nil {
		return nil, e
	}
	return output, nil
}

func (s *Service) startReadBody(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope, checkDedup bool) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	p, e := s.path(in, "file_path")
	if e != nil {
		return nil, e
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext == ".ipynb" {
		return s.startNotebookRead(ctx, in, scope, p)
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		if s.options.Images == nil || s.options.Images.Read == nil {
			return nil, capability("Read image native codec/owner producer " + ext)
		}
		future, e := s.options.Images.Read(ctx, scope, p, float64(notebookIntEnvironment(s.options.Environment["CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS"], 25000, 0)))
		if e != nil {
			return nil, e
		}
		return transform(s, scope, future, func(next *cooperative.Scope, result json.RawMessage) (json.RawMessage, error) {
			if s.options.ReportEvent != nil {
				var data, file protocol.JSONObject
				_ = json.Unmarshal(result, &data)
				_ = json.Unmarshal(data.Get("file"), &file)
				content, e := protocol.DecodeJavaScriptJSONString(file.Get("base64"))
				if e != nil {
					return nil, e
				}
				if e = s.options.ReportEvent(next, analytics.FileOperationEvent(analytics.FileOperation{Operation: "read", Tool: "FileReadTool", Path: utf16.Encode([]rune(p)), Content: &content})); e != nil {
					return nil, e
				}
			}
			return result, nil
		})
	case ".pdf":
		return nil, capability("Read image/PDF processing " + ext)
	}
	offset := number(in, "offset", 1)
	limit := number(in, "limit", 0)
	hasLimit := in.Get("limit") != nil
	old, ok, e := s.options.Cache.Get(scope, p)
	if e != nil {
		return nil, e
	}
	dedup := checkDedup && ok && !old.PartialView && old.Offset != nil && int(*old.Offset) == offset && ((old.Limit == nil && !hasLimit) || (old.Limit != nil && hasLimit && int(*old.Limit) == limit))
	start := offset - 1
	if offset == 0 {
		start = 0
	}
	maxTokens := 25000
	if n, e := strconv.Atoi(s.options.Environment["CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS"]); e == nil && n > 0 {
		maxTokens = n
	}
	divisor := 4.0
	if ext == ".json" || ext == ".jsonl" || ext == ".jsonc" {
		divisor = 2
	}
	f, e := native(s, scope, func() (readCompletion, error) {
		if dedup {
			if stamp, e := mtime(p); e == nil && stamp == old.Timestamp {
				return readCompletion{unchanged: true}, nil
			}
		}
		data, e := readRange(ctx, p, start, limit, hasLimit)
		if e != nil {
			return readCompletion{}, e
		}
		estimate := int(math.Floor(float64(len(utf16.Encode([]rune(data.Content))))/divisor + 0.5))
		if estimate > maxTokens/4 {
			if s.options.CountTokens == nil {
				return readCompletion{}, capability("large Read actual token-count API")
			}
			count, e := s.options.CountTokens(ctx, data.Content)
			if e != nil {
				return readCompletion{}, e
			}
			if count != nil {
				estimate = *count
			}
			if estimate > maxTokens {
				return readCompletion{}, fmt.Errorf("File content (%d tokens) exceeds maximum allowed tokens (%d). Please use offset and limit parameters to read specific portions of the file, or use the GrepTool to search for specific content.", estimate, maxTokens)
			}
		}
		return readCompletion{text: data}, nil
	})
	if e != nil {
		return nil, e
	}
	return transform(s, scope, f, func(callback *cooperative.Scope, completion readCompletion) (json.RawMessage, error) {
		if completion.unchanged {
			if s.options.ReportEvent != nil {
				if e := s.options.ReportEvent(callback, analytics.ReadDedupEvent(p)); e != nil {
					return nil, e
				}
			}
			return object(field("type", "file_unchanged"), protocol.JSONField{Name: "file", Value: object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")})}), nil
		}
		data := completion.text
		o := float64(offset)
		var l *float64
		if hasLimit {
			v := float64(limit)
			l = &v
		}
		if e = s.options.Cache.Set(callback, p, builtinwrite.FileState{Content: quote(data.Content), Timestamp: data.Mtime, Offset: &o, Limit: l}); e != nil {
			return nil, e
		}
		if s.options.ReportEvent != nil {
			var readLimit *int
			if hasLimit {
				value := number(in, "limit", 0)
				readLimit = &value
			}
			for _, event := range analytics.ReadEvents(analytics.TextRead{Path: p, ConfigDir: s.options.Environment["CLAUDE_CONFIG_DIR"], Content: utf16.Encode([]rune(data.Content)), TotalLines: data.Total, ReadLines: data.Lines, TotalBytes: data.TotalBytes, ReadBytes: data.ReadBytes, Offset: offset, Limit: readLimit, MessageID: readAnalyticsMessageID(ctx)}) {
				if e := s.options.ReportEvent(callback, event); e != nil {
					return nil, e
				}
			}
		}
		return object(field("type", "text"), protocol.JSONField{Name: "file", Value: object(protocol.JSONField{Name: "filePath", Value: in.Get("file_path")}, field("content", data.Content), field("numLines", data.Lines), field("startLine", offset), field("totalLines", data.Total))}), nil
	})
}
func textResult(id json.RawMessage, content string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", content))
}
func MapResult(name string, data, id json.RawMessage) (protocol.JSONObject, error) {
	if _, e := protocol.DecodeJavaScriptJSONString(id); e != nil {
		return protocol.JSONObject{}, e
	}
	if mcpResourceTool(name) {
		return mapMCPResourceResult(name, data, id)
	}
	var result protocol.JSONObject
	if e := json.Unmarshal(data, &result); e != nil {
		return protocol.JSONObject{}, e
	}
	switch name {
	case "ImageGen", "ImageEdit":
		return mapImageResult(data, id)
	case "WebSearch":
		return websearch.MapResult(result, id)
	case "WebFetch":
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), protocol.JSONField{Name: "content", Value: result.Get("result")}), nil
	case "AskUserQuestion":
		return mapQuestion(result, id)
	case "NotebookEdit":
		return mapNotebookEdit(result, id)
	case "TaskCreate", "TaskGet", "TaskUpdate", "TaskList":
		return mapTask(name, result, id)
	case "Agent":
		return mapAgent(result, id)
	case "SendMessage":
		encoded, e := protocol.ParseAndStringifyJavaScriptJSON(data)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "text"), field("text", string(encoded)))})), nil
	case "TodoWrite":
		if boolean(result, "verificationNudgeNeeded", false) {
			return protocol.JSONObject{}, capability("active verification-agent TodoWrite nudge")
		}
		return textResult(id, todoSuccess), nil
	case "Read":
		if result.String("type") == "image" {
			file, _ := result.Object("file")
			block := protocol.NewJSONObject(field("type", "image"), protocol.JSONField{Name: "source", Value: object(field("type", "base64"), protocol.JSONField{Name: "data", Value: file.Get("base64")}, protocol.JSONField{Name: "media_type", Value: file.Get("type")})})
			return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{block})), nil
		}
		if result.String("type") == "file_unchanged" {
			return textResult(id, unchanged), nil
		}
		var f protocol.JSONObject
		if e := json.Unmarshal(result.Get("file"), &f); e != nil {
			return protocol.JSONObject{}, e
		}
		if result.String("type") == "notebook" {
			return mapNotebookRead(f, id)
		}
		content := f.String("content")
		start := number(f, "startLine", 1)
		if content == "" {
			if number(f, "totalLines", 0) == 0 {
				return textResult(id, "<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"), nil
			}
			return textResult(id, fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>", start, number(f, "totalLines", 0))), nil
		}
		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		for i, l := range lines {
			lines[i] = fmt.Sprintf("%d\t%s", start+i, l)
		}
		return textResult(id, strings.Join(lines, "\n")+mitigation), nil
	case "Edit":
		note := ""
		if boolean(result, "userModified", false) {
			note = ".  The user modified your proposed changes before accepting them. "
		}
		p := result.String("filePath")
		if boolean(result, "replaceAll", false) {
			return textResult(id, "The file "+p+" has been updated"+note+". All occurrences were successfully replaced."), nil
		}
		return textResult(id, "The file "+p+" has been updated successfully"+note+"."), nil
	case "Glob":
		var names []string
		_ = json.Unmarshal(result.Get("filenames"), &names)
		if len(names) == 0 {
			return textResult(id, "No files found"), nil
		}
		if boolean(result, "truncated", false) {
			names = append(names, "(Results are truncated. Consider using a more specific path or pattern.)")
		}
		return textResult(id, strings.Join(names, "\n")), nil
	case "Grep":
		return mapGrep(result, id), nil
	case "Bash":
		stdout := trimBashOutput(result.String("stdout"))
		var persistedPreview json.RawMessage
		if path := result.String("persistedOutputPath"); path != "" {
			var size int64
			_ = json.Unmarshal(result.Get("persistedOutputSize"), &size)
			var e error
			trimmed, e := trimBashOutputJSON(result.Get("stdout"))
			if e != nil {
				return protocol.JSONObject{}, e
			}
			persistedPreview, e = largeBashOutputPreview(trimmed, path, size)
			if e != nil {
				return protocol.JSONObject{}, e
			}
		}
		stderr := strings.TrimFunc(result.String("stderr"), bashSpace)
		interrupted := boolean(result, "interrupted", false)
		if interrupted {
			if result.String("stderr") != "" {
				stderr += "\n"
			}
			stderr += "<error>Command was aborted before completion</error>"
		}
		parts := []string{}
		if stdout != "" {
			parts = append(parts, stdout)
		}
		if stderr != "" {
			parts = append(parts, stderr)
		}
		if taskID := result.String("backgroundTaskId"); taskID != "" {
			return protocol.JSONObject{}, capability("background Bash result requires its actual output path")
		}
		mapped := textResult(id, strings.Join(parts, "\n"))
		if persistedPreview != nil {
			if stderr != "" {
				var e error
				persistedPreview, e = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{persistedPreview, quote(stderr)}, "\n")
				if e != nil {
					return protocol.JSONObject{}, e
				}
			}
			mapped.Set("content", persistedPreview)
		}
		mapped.Set("is_error", raw(interrupted))
		return mapped, nil
	}
	return protocol.JSONObject{}, capability("result mapper " + name)
}
