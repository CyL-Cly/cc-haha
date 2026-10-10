package builtintools

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"os"
	"path/filepath"
)

func (s *Service) persistForegroundShellOutput(scope *cooperative.Scope, path, id string, size int64) (string, error) {
	if s.options.ToolResultsDirectory == nil {
		return "", capability("foreground Bash original tool-results directory")
	}
	dir, e := s.options.ToolResultsDirectory(scope)
	if e != nil {
		return "", e
	}
	dest := filepath.Join(dir, id+".txt")
	copy, e := native(s, scope, func() (string, error) {
		// Source catches optional persistence failures and keeps the preview. The
		// required directory producer/owned scope above is never silently missing.
		if check := s.options.BackgroundTasks.CheckPath; check != nil {
			if e := check(dest); e != nil {
				return "", nil
			}
		}
		if e := os.MkdirAll(dir, 0777); e != nil {
			return "", nil
		}
		if size > 64*1024*1024 {
			if e := os.Truncate(path, 64*1024*1024); e != nil {
				return "", nil
			}
		}
		if e := os.Link(path, dest); e == nil {
			return dest, nil
		}
		source, e := os.Open(path)
		if e != nil {
			return "", nil
		}
		defer source.Close()
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			return "", nil
		}
		_, e = io.Copy(out, source)
		closeError := out.Close()
		if e != nil || closeError != nil {
			return "", nil
		}
		return dest, nil
	})
	if e != nil {
		return "", e
	}
	return copy.Await(scope)
}

// Source generatePreview measures UTF-16 code units despite its maxBytes
// parameter name. Preserve a lone surrogate when the exact slice cuts a pair.
func largeBashOutputPreview(content json.RawMessage, path string, size int64) (json.RawMessage, error) {
	units, e := protocol.DecodeJavaScriptJSONString(content)
	if e != nil {
		return nil, e
	}
	more := len(units) > 2000
	cut := len(units)
	if more {
		cut = 2000
		last := -1
		for i, u := range units[:2000] {
			if u == '\n' {
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
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("<persisted-output>\nOutput too large (" + formatSize(size) + "). Full output saved to: " + path + "\n\nPreview (first " + formatSize(2000) + "):\n"), protocol.QuoteJavaScriptUTF16(units[:cut]), quote(suffix + "</persisted-output>")}, "")
}

func trimBashOutputJSON(value json.RawMessage) (json.RawMessage, error) {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return nil, e
	}
	leading := 0
	for i, u := range units {
		if !protocol.IsJavaScriptWhitespace(rune(u)) {
			break
		}
		if u == '\n' {
			leading = i + 1
		}
	}
	end := len(units)
	for end > leading && protocol.IsJavaScriptWhitespace(rune(units[end-1])) {
		end--
	}
	return protocol.QuoteJavaScriptUTF16(units[leading:end]), nil
}

// PersistForegroundShellOutput shares the actual file-mode Bash producer with
// callers that already hold the same owned Scope and real output-file handle.
func (s *Service) PersistForegroundShellOutput(scope *cooperative.Scope, path, id string, size int64) (string, error) {
	if e := s.check(scope); e != nil {
		return "", e
	}
	return s.persistForegroundShellOutput(scope, path, id, size)
}
