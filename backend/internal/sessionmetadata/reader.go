// Package sessionmetadata reads ordinary transcript values intact and projects
// oversized values through the server's bounded metadata envelope.
package sessionmetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type Entry struct {
	Value json.RawMessage
	// TextTruncated corresponds to the source's WeakSet membership. It is not
	// a persisted field and must not be used as an authoritative notification.
	TextTruncated bool
}
type Options struct {
	StartOffset int64
	EndOffset   *int64
	OnSkipped   func() error
}
type Result struct {
	SourceVersion    string `json:"sourceVersion"`
	OmittedRecords   int    `json:"omittedRecords"`
	ProjectedRecords int    `json:"projectedRecords"`
	ScannedBytes     int64  `json:"scannedBytes"`
	NextOffset       int64  `json:"nextOffset"`
}
type Reader struct{ files sessionstore.FileSystem }

func NewReader(files sessionstore.FileSystem) *Reader {
	if files == nil {
		files = sessionstore.OSFileSystem{}
	}
	return &Reader{files: files}
}
func check(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == context.Canceled {
		return &sessionstore.AbortError{Cause: cause}
	}
	return cause
}

type record struct {
	parts                       [][]byte
	length                      int
	parser                      *tokenizer
	projection                  *projection
	parseError, projectionError error
}

func (line *record) feed(part []byte, final bool) {
	if line.parseError != nil || line.projectionError != nil {
		return
	}
	tokens, err := line.parser.feed(part, final)
	if err != nil {
		line.parseError = err
		return
	}
	for _, item := range tokens {
		if err := line.projection.token(item); err != nil {
			line.projectionError = err
			break
		}
	}
}
func (line *record) add(part []byte) {
	line.length += len(part)
	if line.parser == nil && line.length > sessionstore.HistorySemanticRecordBytes {
		line.parser = &tokenizer{}
		line.projection = &projection{}
		for _, saved := range line.parts {
			line.feed(saved, false)
		}
		line.parts = nil
	}
	if line.parser != nil {
		line.feed(part, false)
	} else {
		line.parts = append(line.parts, append([]byte(nil), part...))
	}
}
func (line *record) flush() (entry *Entry, projected, malformed bool, err error) {
	if line.parser != nil {
		line.feed(nil, true)
		if line.projectionError != nil {
			return nil, false, false, line.projectionError
		}
		if line.parseError != nil {
			return nil, false, true, nil
		}
		if line.projection.root != nil && line.projection.root.kind == '{' {
			value := line.projection.entry()
			return &value, true, false, nil
		}
		return nil, true, false, nil
	}
	if line.length == 0 {
		return nil, false, false, nil
	}
	raw := protocol.DecodeJavaScriptUTF8(bytes.Join(line.parts, nil))
	if !json.Valid(raw) {
		return nil, false, true, nil
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) != 0 && trimmed[0] == '{' {
		return &Entry{Value: raw}, false, false, nil
	}
	return nil, false, false, nil
}

// Stream deliberately does not acquire an admission slot: metadata and context
// callers wrap it in their shared sessionstore.Budget lane, as in the source.
func (reader *Reader) Stream(ctx context.Context, path string, onEntry func(Entry, bool, int64) error, options Options) (result Result, err error) {
	handle, err := reader.files.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if closeErr := handle.Close(); closeErr != nil {
			result = Result{}
			err = closeErr
		}
	}()
	stat, err := handle.Stat()
	if err != nil {
		return Result{}, err
	}
	size := stat.Size
	if options.EndOffset != nil && *options.EndOffset < size {
		size = *options.EndOffset
	}
	offset, lineStart := options.StartOffset, options.StartOffset
	result.NextOffset = lineStart
	line := record{}
	flush := func(complete bool) error {
		entry, projected, malformed, err := line.flush()
		if err != nil {
			return err
		}
		if projected {
			result.ProjectedRecords++
		}
		if malformed {
			result.OmittedRecords++
			if options.OnSkipped != nil {
				if err := options.OnSkipped(); err != nil {
					return err
				}
			}
		}
		if entry != nil {
			if err := onEntry(*entry, complete, lineStart); err != nil {
				return err
			}
		}
		line = record{}
		return nil
	}
	chunk := make([]byte, 64*1024)
	for offset < size {
		if err := check(ctx); err != nil {
			return Result{}, err
		}
		length := int64(len(chunk))
		if size-offset < length {
			length = size - offset
		}
		n, readErr := handle.ReadAt(chunk[:length], offset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Result{}, readErr
		}
		if n == 0 {
			return Result{}, &sessionstore.APIError{StatusCode: 409, Code: "HISTORY_CHANGED", Message: "Session history changed during recovery"}
		}
		offset += int64(n)
		for start := 0; start < n; {
			if err := check(ctx); err != nil {
				return Result{}, err
			}
			end := n
			if newline := bytes.IndexByte(chunk[start:n], '\n'); newline >= 0 {
				end = start + newline
			}
			line.add(chunk[start:end])
			start = end + 1
			if end < n {
				if err := flush(true); err != nil {
					return Result{}, err
				}
				lineStart = offset - int64(n) + int64(end) + 1
				result.NextOffset = lineStart
			}
		}
	}
	if line.length != 0 {
		if err := flush(false); err != nil {
			return Result{}, err
		}
	}
	after, err := handle.Stat()
	if err != nil {
		return Result{}, err
	}
	if after.Size < stat.Size || after.Size == stat.Size && after.MtimeNS != stat.MtimeNS {
		return Result{}, &sessionstore.APIError{StatusCode: 409, Code: "HISTORY_CHANGED", Message: "Session changed during recovery; retry"}
	}
	result.SourceVersion = fmt.Sprintf("%s:%s:%d:%s", stat.Dev, stat.Ino, stat.Size, stat.MtimeNS)
	result.ScannedBytes = size - options.StartOffset
	return result, nil
}
