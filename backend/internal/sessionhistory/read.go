package sessionhistory

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const SkipPrecompactThreshold = 5 * 1024 * 1024

var metadataMarkers = [][]byte{[]byte(`"type":"summary"`), []byte(`"type":"custom-title"`), []byte(`"type":"tag"`), []byte(`"type":"agent-name"`), []byte(`"type":"agent-color"`), []byte(`"type":"agent-setting"`), []byte(`"type":"mode"`), []byte(`"type":"worktree-state"`), []byte(`"type":"pr-link"`)}

func (loader *Loader) read(path string, keepAllLeaves bool) ([]byte, []byte, error) {
	reader, err := loader.BeginRead(path, keepAllLeaves)
	if err != nil {
		return nil, nil, err
	}
	var file interface {
		ReadAt([]byte, int64) (int, error)
		Close() error
	}
	for reader.operation != ReadComplete {
		request := reader.Request()
		var result PhysicalReadResult
		switch request.Operation {
		case ReadStat:
			result.Size, result.Error = loader.options.Stat(path)
		case ReadWhole:
			result.Bytes, result.Error = loader.options.ReadFile(path)
		case ReadOpen:
			file, result.Error = loader.options.Files.Open(path)
		case ReadAt:
			chunk := make([]byte, request.Length)
			var n int
			n, result.Error = file.ReadAt(chunk, request.Offset)
			result.Bytes = chunk[:n]
		case ReadClose:
			result.Error = file.Close()
		}
		if err := reader.Complete(result); err != nil {
			return nil, nil, err
		}
	}
	return reader.data, reader.metadata, reader.err
}

type largeRead struct {
	data      []byte
	boundary  int64
	preserved bool
}
type largeScanner struct {
	result      largeRead
	carry, snap []byte
	lineOffset  int64
}

func (scanner *largeScanner) feed(data []byte) {
	result, carry, snap, lineOffset := &scanner.result, scanner.carry, scanner.snap, scanner.lineOffset
	n := len(data)
	process := func(line []byte, straddle bool) {
		if bytes.HasPrefix(line, []byte(`{"type":"attribution-snapshot"`)) {
			snap = append([]byte(nil), line...)
			return
		}
		marker := bytes.Index(line, []byte(`"compact_boundary"`))
		if straddle && bytes.HasPrefix(line, []byte(`{"type":"system"`)) || !straddle && marker >= 0 && marker < 256 {
			var value protocol.JSONObject
			if json.Unmarshal(bytes.TrimSuffix(protocol.DecodeJavaScriptUTF8(line), []byte{'\n'}), &value) == nil && compact(value) {
				if truthy(object(value.Get("compactMetadata")).Get("preservedSegment")) {
					result.preserved = true
				} else {
					result.data = result.data[:0]
					result.boundary = lineOffset
					result.preserved = false
					snap = nil
				}
			}
		}
		result.data = append(result.data, line...)
	}
	start := 0
	for start < n {
		newline := bytes.IndexByte(data[start:n], '\n')
		if newline < 0 {
			carry = append(carry, data[start:n]...)
			break
		}
		end := start + newline + 1
		if len(carry) > 0 {
			straddle := len(carry) >= len(`{"type":"attribution-snapshot"`)
			carry = append(carry, data[start:end]...)
			process(carry, straddle)
			lineOffset += int64(len(carry))
			carry = carry[:0]
		} else {
			process(data[start:end], false)
			lineOffset += int64(end - start)
		}
		start = end
	}
	scanner.carry, scanner.snap, scanner.lineOffset = carry, snap, lineOffset
}
func (scanner *largeScanner) finish() {
	result, carry, snap := &scanner.result, scanner.carry, scanner.snap
	if len(carry) > 0 {
		if bytes.HasPrefix(carry, []byte(`{"type":"attribution-snapshot"`)) {
			snap = append([]byte(nil), carry...)
		} else {
			result.data = append(result.data, carry...)
		}
	}
	if len(snap) > 0 {
		if len(result.data) > 0 && result.data[len(result.data)-1] != '\n' {
			result.data = append(result.data, '\n')
		}
		result.data = append(result.data, snap...)
	}
	if result.data == nil {
		result.data = []byte{}
	}

}

type metadataScanner struct{ result, carry []byte }

func (scanner *metadataScanner) feed(data []byte) {
	result, carry := scanner.result, scanner.carry
	defer func() { scanner.result, scanner.carry = result, carry }()
	if len(carry) > 0 {
		retain := len(carry) < 25
		if !retain && carry[0] == '{' {
			for _, marker := range metadataMarkers {
				if bytes.HasPrefix(carry[1:], marker) {
					retain = true
					break
				}
			}
		}
		if retain {
			data = append(append([]byte(nil), carry...), data...)
		} else {
			newline := bytes.IndexByte(data, '\n')
			if newline < 0 {
				carry = nil
				return
			}
			data = data[newline+1:]
		}
	}
	markerFound := false
	for _, marker := range metadataMarkers {
		if bytes.Contains(data, marker) {
			markerFound = true
			break
		}
	}
	if markerFound {
		start := 0
		for {
			newline := bytes.IndexByte(data[start:], '\n')
			if newline < 0 {
				break
			}
			stop := start + newline
			line := data[start:stop]
			for _, marker := range metadataMarkers {
				if bytes.Contains(line, marker) {
					result = append(result, line...)
					result = append(result, '\n')
					break
				}
			}
			start = stop + 1
		}
		carry = append([]byte(nil), data[start:]...)
	} else {
		newline := bytes.LastIndexByte(data, '\n')
		carry = append([]byte(nil), data[newline+1:]...)
	}
	if len(carry) > 64*1024 {
		carry = nil
	}
}
func (scanner *metadataScanner) finish() []byte {
	result, carry := scanner.result, scanner.carry
	for _, marker := range metadataMarkers {
		if bytes.Contains(carry, marker) {
			result = append(result, carry...)
			break
		}
	}
	return result
}

// Synchronous direct scanner seams use the same owner-neutral byte reducers.
// Async cache jobs drive ReadContinuation instead of putting these on workers.
func (loader *Loader) readLarge(path string, size int64) (result largeRead, err error) {
	file, err := loader.options.Files.Open(path)
	if err != nil {
		return result, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = closeErr
		}
	}()
	scanner := largeScanner{}
	for offset := int64(0); offset < size; {
		chunk := make([]byte, min(int64(1024*1024), size-offset))
		n, readErr := file.ReadAt(chunk, offset)
		if readErr != nil && readErr != io.EOF {
			return result, readErr
		}
		if n == 0 {
			break
		}
		offset += int64(n)
		scanner.feed(chunk[:n])
	}
	scanner.finish()
	return scanner.result, nil
}
func (loader *Loader) preMetadata(path string, end int64) (result []byte, err error) {
	file, err := loader.options.Files.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = closeErr
		}
	}()
	scanner := metadataScanner{}
	for offset := int64(0); offset < end; {
		chunk := make([]byte, min(int64(64*1024), end-offset))
		n, readErr := file.ReadAt(chunk, offset)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if n == 0 {
			break
		}
		offset += int64(n)
		scanner.feed(chunk[:n])
	}
	return scanner.finish(), nil
}
