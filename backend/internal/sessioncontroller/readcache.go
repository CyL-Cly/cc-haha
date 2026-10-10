package sessioncontroller

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/unicode/norm"
)

const unchangedStub = "File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current — refer to that instead of re-reading."

const (
	defaultReadCacheEntries  = 10
	headlessReadCacheEntries = 100
)

type FileState struct {
	Content   json.RawMessage
	Timestamp float64
}

func (state FileState) MarshalJSON() ([]byte, error) {
	timestamp := json.RawMessage("null")
	if !math.IsNaN(state.Timestamp) && !math.IsInf(state.Timestamp, 0) {
		timestamp = raw(state.Timestamp)
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: state.Content}, protocol.JSONField{Name: "timestamp", Value: timestamp}).MarshalJSON()
}

type ReadCache struct {
	entries    map[string]FileState
	order      []string
	size       int
	maxEntries int
}

// NewReadCache retains extractReadFilesFromMessages's standalone helper default.
// The headless caller supplies its independent READ_FILE_STATE_CACHE_SIZE.
func NewReadCache() *ReadCache { return newReadCache(defaultReadCacheEntries) }
func newReadCache(maxEntries int) *ReadCache {
	return &ReadCache{entries: map[string]FileState{}, maxEntries: maxEntries}
}
func byteSize(content json.RawMessage) int {
	units, _ := protocol.DecodeJavaScriptJSONString(content)
	size := len([]byte(string(utf16.Decode(units))))
	if size == 0 {
		return 1
	}
	return size
}
func (cache *ReadCache) Set(path string, value FileState) {
	path = filepath.Clean(path)
	if previous, exists := cache.entries[path]; exists {
		_ = previous
		cache.Delete(path)
	}
	size := byteSize(value.Content)
	if size > 25*1024*1024 {
		return
	}
	cache.entries[path] = FileState{Content: append(json.RawMessage(nil), value.Content...), Timestamp: value.Timestamp}
	cache.order = append([]string{path}, cache.order...)
	cache.size += size
	for len(cache.order) > cache.maxEntries || cache.size > 25*1024*1024 {
		cache.Delete(cache.order[len(cache.order)-1])
	}
}
func (cache *ReadCache) Delete(path string) {
	path = filepath.Clean(path)
	if previous, exists := cache.entries[path]; exists {
		cache.size -= byteSize(previous.Content)
		delete(cache.entries, path)
		for index, name := range cache.order {
			if name == path {
				cache.order = append(cache.order[:index:index], cache.order[index+1:]...)
				break
			}
		}
	}
}
func (cache *ReadCache) Get(path string) (FileState, bool) {
	path = filepath.Clean(path)
	value, exists := cache.entries[path]
	if exists {
		cache.Delete(path)
		cache.Set(path, value)
	}
	value.Content = append(json.RawMessage(nil), value.Content...)
	return value, exists
}
func (cache *ReadCache) Entries() []protocol.JSONObject {
	result := make([]protocol.JSONObject, 0, len(cache.order))
	for _, path := range cache.order {
		result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: quote(path)}, protocol.JSONField{Name: "value", Value: raw(cache.entries[path])}))
	}
	return result
}
func (cache *ReadCache) Clone() *ReadCache {
	result := newReadCache(cache.maxEntries)
	for index := len(cache.order) - 1; index >= 0; index-- {
		path := cache.order[index]
		result.Set(path, cache.entries[path])
	}
	return result
}
func expandFilePath(value json.RawMessage, cwd, home string) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return "", capability("non-string tool file path")
	}
	units = trimUnits(units)
	path := string(utf16.Decode(units))
	if !bytes.Equal(protocol.QuoteJavaScriptUTF16(units), quote(path)) {
		return "", capability("tool file path with unpaired UTF-16")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("Path contains null bytes")
	}
	switch {
	case path == "":
		path = cwd
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, path[2:])
	case !filepath.IsAbs(path):
		path = filepath.Join(cwd, path)
	}
	return norm.NFC.String(filepath.Clean(path)), nil
}
func timestamp(value json.RawMessage, parse sessionprojection.DateParser) (float64, error) {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return 0, capability("non-string read-cache Date input")
	}
	valueTime, valid, err := parse(units)
	if !valid && err == nil {
		return math.NaN(), nil
	}
	return valueTime, err
}
func stripReadContent(content json.RawMessage) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(content)
	// Work on UTF-16 below, retaining lone code units rather than round-tripping
	// stripped model-visible text through Go Unicode replacement.
	open, close := utf16.Encode([]rune("<system-reminder>")), utf16.Encode([]rune("</system-reminder>"))
	indexOf := func(value, needle []uint16) int {
		for index := 0; index+len(needle) <= len(value); index++ {
			match := true
			for offset := range needle {
				if value[index+offset] != needle[offset] {
					match = false
					break
				}
			}
			if match {
				return index
			}
		}
		return -1
	}
	for {
		start := indexOf(units, open)
		if start < 0 {
			break
		}
		end := indexOf(units[start+len(open):], close)
		if end < 0 {
			break
		}
		end += start + len(open) + len(close)
		units = append(units[:start:start], units[end:]...)
	}
	result := []uint16{}
	for len(units) > 0 {
		end := 0
		for end < len(units) && units[end] != '\n' {
			end++
		}
		line := units[:end]
		position := 0
		for position < len(line) && whitespace(line[position]) {
			position++
		}
		digitStart := position
		for position < len(line) && line[position] >= '0' && line[position] <= '9' {
			position++
		}
		if position > digitStart && position < len(line) && (line[position] == '→' || line[position] == '\t') {
			remaining := line[position+1:]
			valid := true
			for _, unit := range remaining {
				if unit == '\r' || unit == 0x2028 || unit == 0x2029 {
					valid = false
					break
				}
			}
			if valid {
				line = remaining
			}
		}
		result = append(result, line...)
		if end == len(units) {
			break
		}
		result = append(result, '\n')
		units = units[end+1:]
	}
	return protocol.QuoteJavaScriptUTF16(trimUnits(result))
}
func decodeDisk(data []byte) json.RawMessage {
	units := []uint16{}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		for index := 0; index+1 < len(data); index += 2 {
			units = append(units, binary.LittleEndian.Uint16(data[index:index+2]))
		}
	} else {
		units = utf16.Encode([]rune(string(storage.DecodeUTF8(data))))
	}
	normalized := []uint16{}
	for index, unit := range units {
		if unit == '\r' && index+1 < len(units) && units[index+1] == '\n' {
			continue
		}
		normalized = append(normalized, unit)
	}
	return protocol.QuoteJavaScriptUTF16(normalized)
}
func inaccessible(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}

// ExtractReadCache runs both actual passes before interrupted-history removal.
// Read/Write retain timestamp semantics; successful Edit reads real disk bytes.
func ExtractReadCache(history []*protocol.JSONObject, cwd, home string, files TranscriptFiles, parse sessionprojection.DateParser) (*ReadCache, error) {
	return extractReadCache(history, cwd, home, files, parse, defaultReadCacheEntries)
}

func extractReadCache(history []*protocol.JSONObject, cwd, home string, files TranscriptFiles, parse sessionprojection.DateParser, maxEntries int) (*ReadCache, error) {
	cache := newReadCache(maxEntries)
	readPaths, editPaths, writePaths := map[string]string{}, map[string]string{}, map[string]string{}
	writes := map[string]FileState{}
	for _, message := range history {
		if text(message.Get("type")) != "assistant" {
			continue
		}
		blocks, err := parseObjects(object(message.Get("message")).Get("content"))
		if err != nil {
			continue
		}
		for _, block := range blocks {
			if text(block.Get("type")) != "tool_use" {
				continue
			}
			toolName := text(block.Get("name"))
			if toolName != "Read" && toolName != "Write" && toolName != "Edit" {
				continue
			}
			input := object(block.Get("input"))
			if !truthy(input.Get("file_path")) {
				continue
			}
			path, err := expandFilePath(input.Get("file_path"), cwd, home)
			if err != nil {
				return nil, err
			}
			id, err := stringKey(block.Get("id"))
			if err != nil {
				return nil, err
			}
			switch text(block.Get("name")) {
			case "Read":
				if input.Get("offset") == nil && input.Get("limit") == nil {
					readPaths[id] = path
				}
			case "Write":
				if truthy(input.Get("content")) {
					if _, err := protocol.DecodeJavaScriptJSONString(input.Get("content")); err != nil {
						return nil, capability("non-string Write content")
					}
					writes[id] = FileState{Content: input.Get("content")}
					writePaths[id] = path
				}
			case "Edit":
				editPaths[id] = path
			}
		}
	}
	for _, message := range history {
		if text(message.Get("type")) != "user" {
			continue
		}
		blocks, err := parseObjects(object(message.Get("message")).Get("content"))
		if err != nil {
			continue
		}
		for _, block := range blocks {
			if text(block.Get("type")) != "tool_result" || !truthy(block.Get("tool_use_id")) {
				continue
			}
			id, err := stringKey(block.Get("tool_use_id"))
			if err != nil {
				return nil, err
			}
			if path, exists := readPaths[id]; exists {
				content := block.Get("content")
				if _, err := protocol.DecodeJavaScriptJSONString(content); err == nil && !strings.HasPrefix(text(content), unchangedStub) && truthy(message.Get("timestamp")) {
					when, err := timestamp(message.Get("timestamp"), parse)
					if err != nil {
						return nil, err
					}
					cache.Set(path, FileState{stripReadContent(content), when})
				}
			}
			if value, exists := writes[id]; exists && truthy(message.Get("timestamp")) {
				when, err := timestamp(message.Get("timestamp"), parse)
				if err != nil {
					return nil, err
				}
				inputPath := writePaths[id]
				value.Timestamp = when
				cache.Set(inputPath, value)
			}
			if path := editPaths[id]; path != "" && string(block.Get("is_error")) != "true" {
				data, err := files.ReadFile(path)
				if err != nil {
					if inaccessible(err) {
						continue
					}
					return nil, err
				}
				info, err := files.Stat(path)
				if err != nil {
					if inaccessible(err) {
						continue
					}
					return nil, err
				}
				cache.Set(path, FileState{decodeDisk(data), float64(info.ModTime().UnixMilli())})
			}
		}
	}
	return cache, nil
}
