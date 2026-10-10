// Package sessionstore implements the server's bounded, read-only transcript
// boundary. Session projection and HTTP routing are separate business services.
package sessionstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const (
	HistoryScanBytes           = 16 * 1024 * 1024
	HistoryFullScanBytes       = 64 * 1024 * 1024
	HistorySemanticRecordBytes = 8 * 1024 * 1024
	HistoryRecordBytes         = 1024 * 1024
	HistoryPageBytes           = 256 * 1024
	HistoryPageRecords         = 200
	HistoryPageRows            = 500
	HistoryFullBytes           = 32 * 1024 * 1024
	HistoryFullRows            = 250000
)

type APIError struct {
	StatusCode    int
	Code, Message string
}

func (err *APIError) Error() string     { return err.Message }
func (err *APIError) ErrorName() string { return "ApiError" }
func changed(message string) error      { return &APIError{409, "HISTORY_CHANGED", message} }

// Snapshot uses the bigint stat strings consumed by the source. In particular,
// inode/device numbers must never pass through float64 before stringification.
type Snapshot struct {
	Dev, Ino, MtimeNS string
	Size              int64
}

func (stat Snapshot) version() string {
	return fmt.Sprintf("%s:%s:%d:%s", stat.Dev, stat.Ino, stat.Size, stat.MtimeNS)
}

type File interface {
	io.ReaderAt
	Stat() (Snapshot, error)
	Close() error
}
type FileSystem interface{ Open(string) (File, error) }

type Reader struct {
	files  FileSystem
	budget *Budget
}

func NewReader(files FileSystem) *Reader { return NewReaderWithBudget(files, NewBudget()) }

// Sharing one Budget at server bootstrap matches the source module's four
// process-wide admission lanes without introducing a Go package global.
func NewReaderWithBudget(files FileSystem, budget *Budget) *Reader {
	if files == nil {
		files = OSFileSystem{}
	}
	if budget == nil {
		panic("history reader requires a shared admission budget")
	}
	return &Reader{files: files, budget: budget}
}
func (reader *Reader) Budget() *Budget { return reader.budget }

type PageOptions struct {
	Cursor string
	Limit  *float64
	Full   bool
}
type Entry struct {
	Entry     json.RawMessage `json:"entry"`
	ByteStart int64           `json:"byteStart"`
	ByteEnd   int64           `json:"byteEnd"`
}

func (entry Entry) MarshalJSON() ([]byte, error) {
	raw, err := protocol.ParseAndStringifyJavaScriptJSON(entry.Entry)
	if err != nil {
		return nil, err
	}
	object := protocol.NewJSONObject(protocol.JSONField{Name: "entry", Value: raw})
	start, _ := json.Marshal(entry.ByteStart)
	end, _ := json.Marshal(entry.ByteEnd)
	object.Set("byteStart", start)
	object.Set("byteEnd", end)
	return object.MarshalJSON()
}

type PageInfo struct {
	NextCursor              *string `json:"nextCursor"`
	PreviousCursor          *string `json:"previousCursor"`
	HasMore                 bool    `json:"hasMore"`
	HistoryComplete         bool    `json:"historyComplete"`
	ContentTruncated        bool    `json:"contentTruncated,omitempty"`
	SourceVersion           string  `json:"sourceVersion"`
	ScannedBytes            int64   `json:"scannedBytes"`
	OmittedOversizedEntries int     `json:"omittedOversizedEntries"`
}

// MarshalJSON preserves source insertion order: contentTruncated is inserted
// before sourceVersion only when present.
func (page PageInfo) MarshalJSON() ([]byte, error) {
	object := protocol.NewJSONObject()
	for _, field := range []struct {
		name  string
		value any
	}{{"nextCursor", page.NextCursor}, {"previousCursor", page.PreviousCursor}, {"hasMore", page.HasMore}, {"historyComplete", page.HistoryComplete}} {
		raw, _ := json.Marshal(field.value)
		object.Set(field.name, raw)
	}
	if page.ContentTruncated {
		object.Set("contentTruncated", []byte("true"))
	}
	for _, field := range []struct {
		name  string
		value any
	}{{"sourceVersion", page.SourceVersion}, {"scannedBytes", page.ScannedBytes}, {"omittedOversizedEntries", page.OmittedOversizedEntries}} {
		raw, _ := json.Marshal(field.value)
		object.Set(field.name, raw)
	}
	return object.MarshalJSON()
}

type Page struct {
	Entries []Entry  `json:"entries"`
	Page    PageInfo `json:"page"`
}

type cursor struct {
	object                     protocol.JSONObject
	dev, ino, mtime, direction string
	size, offset               int64
	skipping                   bool
	fingerprints               *anchors
}
type anchors struct {
	Prefix   string `json:"prefix"`
	Tail     string `json:"tail"`
	Boundary string `json:"boundary"`
}

func rawString(raw []byte) (string, bool) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
func integer(raw []byte) (int64, bool) {
	var value float64
	if len(raw) == 0 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') || json.Unmarshal(raw, &value) != nil || value < 0 || value > 9007199254740991 || math.Trunc(value) != value {
		return 0, false
	}
	return int64(value), true
}
func truthy(raw []byte) bool {
	if len(raw) > 0 && (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		value, _ := strconv.ParseFloat(string(raw), 64)
		return value != 0
	}
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("false")) && !bytes.Equal(raw, []byte("0")) && !bytes.Equal(raw, []byte(`""`))
}

// Buffer.from(base64url) accepts both alphabets, ignores nonalphabet characters,
// and discards an incomplete final sextet. Cursor validation follows decoding.
func decodeBase64(value string) []byte {
	var cleaned strings.Builder
	for _, char := range value {
		if char == '=' {
			break
		}
		if char == '-' {
			char = '+'
		}
		if char == '_' {
			char = '/'
		}
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '+' || char == '/' {
			cleaned.WriteRune(char)
		}
	}
	encoded := cleaned.String()
	if len(encoded)%4 == 1 {
		encoded = encoded[:len(encoded)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(encoded)
	return decoded
}
func decodeCursor(value string) (cursor, error) {
	invalid := func() (cursor, error) { return cursor{}, &APIError{400, "BAD_REQUEST", "Invalid history cursor"} }
	if len(utf16.Encode([]rune(value))) > 2048 {
		return invalid()
	}
	raw, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(decodeBase64(value)))
	if err != nil {
		return invalid()
	}
	var object protocol.JSONObject
	if object.UnmarshalJSON(raw) != nil || !object.IsObject() {
		return invalid()
	}
	result := cursor{object: object}
	if !bytes.Equal(object.Get("version"), []byte("1")) {
		return invalid()
	}
	var ok bool
	if result.dev, ok = rawString(object.Get("dev")); !ok {
		return invalid()
	}
	if result.ino, ok = rawString(object.Get("ino")); !ok {
		return invalid()
	}
	if result.mtime, ok = rawString(object.Get("mtime")); !ok {
		return invalid()
	}
	if result.size, ok = integer(object.Get("size")); !ok {
		return invalid()
	}
	if result.offset, ok = integer(object.Get("offset")); !ok || result.offset > result.size {
		return invalid()
	}
	if raw := object.Get("skipping"); bytes.Equal(raw, []byte("true")) {
		result.skipping = true
	} else if !bytes.Equal(raw, []byte("false")) {
		return invalid()
	}
	if raw := object.Get("direction"); len(raw) > 0 {
		if result.direction, ok = rawString(raw); !ok || (result.direction != "older" && result.direction != "newer") {
			return invalid()
		}
	}
	if raw := object.Get("fingerprints"); truthy(raw) {
		var fingerprints protocol.JSONObject
		if fingerprints.UnmarshalJSON(raw) != nil || !fingerprints.IsObject() {
			return invalid()
		}
		result.fingerprints = &anchors{}
		for name, target := range map[string]*string{"prefix": &result.fingerprints.Prefix, "tail": &result.fingerprints.Tail, "boundary": &result.fingerprints.Boundary} {
			*target, ok = rawString(fingerprints.Get(name))
			if !ok || len(*target) != 64 {
				return invalid()
			}
			for _, ch := range *target {
				if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
					return invalid()
				}
			}
		}
	}
	return result, nil
}
func initialCursor(stat Snapshot) cursor {
	object := protocol.NewJSONObject()
	for _, field := range []struct {
		name  string
		value any
	}{{"version", 1}, {"dev", stat.Dev}, {"ino", stat.Ino}, {"size", stat.Size}, {"mtime", stat.MtimeNS}, {"offset", stat.Size}, {"skipping", false}} {
		raw, _ := json.Marshal(field.value)
		object.Set(field.name, raw)
	}
	return cursor{object: object, dev: stat.Dev, ino: stat.Ino, size: stat.Size, mtime: stat.MtimeNS, offset: stat.Size}
}

// Default cancellation retains the source AbortController reason and the
// named boundary already recognized by HTTP error handling. Explicit causes
// are returned intact, as signal.reason is in the source.
type AbortError struct{ Cause error }

func (err *AbortError) Error() string     { return "The operation was aborted." }
func (err *AbortError) ErrorName() string { return "AbortError" }
func (err *AbortError) Unwrap() error     { return err.Cause }
func check(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == context.Canceled {
		return &AbortError{Cause: cause}
	}
	return cause
}
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func readExactly(ctx context.Context, file File, buffer []byte, offset int64, message string) error {
	for read := 0; read < len(buffer); {
		if err := check(ctx); err != nil {
			return err
		}
		n, err := file.ReadAt(buffer[read:], offset+int64(read))
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			return changed(message)
		}
		read += n
	}
	return nil
}
func parseEntry(raw []byte) (json.RawMessage, error) {
	decoded := bytes.TrimSpace(protocol.DecodeJavaScriptUTF8(raw))
	if !json.Valid(decoded) {
		return nil, fmt.Errorf("malformed transcript JSON")
	}
	return append(json.RawMessage(nil), decoded...), nil
}
func isObject(raw []byte) bool { return len(raw) > 0 && raw[0] == '{' }

func (reader *Reader) ReadPage(ctx context.Context, path string, options PageOptions) (result Page, err error) {
	if options.Limit != nil && (!isFinite(*options.Limit) || *options.Limit < 1) {
		return result, &APIError{400, "INVALID_HISTORY_LIMIT", "History limit must be a positive finite number"}
	}
	err = reader.budget.With(ctx, PageLane, func() error { var readErr error; result, readErr = reader.readPage(ctx, path, options); return readErr })
	return
}
func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func (reader *Reader) readPage(ctx context.Context, path string, options PageOptions) (result Page, resultErr error) {
	file, err := reader.files.Open(path)
	if err != nil {
		return Page{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			result = Page{}
			resultErr = err
		}
	}()
	if err = check(ctx); err != nil {
		return Page{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		return Page{}, err
	}
	c := initialCursor(stat)
	if options.Cursor != "" {
		c, err = decodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
	}
	if c.dev != stat.Dev || c.ino != stat.Ino || stat.Size < c.size || stat.Size == c.size && stat.MtimeNS != c.mtime {
		return Page{}, changed("Session history changed; reload the newest page")
	}
	var scanned int64
	fingerprint := func(start int64) (string, error) {
		buffer := make([]byte, min(4096, c.size-start))
		if err := readExactly(ctx, file, buffer, start, "Session history changed during validation"); err != nil {
			return "", err
		}
		scanned += int64(len(buffer))
		hash := sha256.Sum256(buffer)
		return hex.EncodeToString(hash[:]), nil
	}
	sourceAnchors := func() (anchors, error) {
		prefix, err := fingerprint(0)
		if err != nil {
			return anchors{}, err
		}
		tail, err := fingerprint(max(0, c.size-4096))
		return anchors{Prefix: prefix, Tail: tail}, err
	}
	boundary := func(offset int64) (string, error) { return fingerprint(max(0, min(offset-2048, c.size-4096))) }
	if c.fingerprints == nil && options.Cursor != "" && stat.Size > c.size {
		return Page{}, changed("Legacy history cursor cannot validate append; reload the newest page")
	}
	anchorsBefore, err := sourceAnchors()
	if err != nil {
		return Page{}, err
	}
	if c.fingerprints != nil {
		if c.fingerprints.Prefix != anchorsBefore.Prefix || c.fingerprints.Tail != anchorsBefore.Tail {
			return Page{}, changed("Session history was rewritten; reload the newest page")
		}
		value, err := boundary(c.offset)
		if err != nil {
			return Page{}, err
		}
		if value != c.fingerprints.Boundary {
			return Page{}, changed("Session history was rewritten; reload the newest page")
		}
	}
	newer := c.direction == "newer"
	position := c.offset
	var buffer []byte
	bufferStart, bufferEnd := int64(-1), int64(-1)
	skipping := c.skipping
	omitted := 0
	var outputBytes int64
	renderedRows := 0
	entries := []Entry{}
	recordLimit := float64(HistoryPageRecords)
	byteBudget, rowBudget, scanBudget := int64(HistoryPageBytes), HistoryPageRows, int64(HistoryScanBytes)
	if options.Full {
		recordLimit = 9007199254740991
		byteBudget = HistoryFullBytes
		rowBudget = HistoryFullRows
		scanBudget = HistoryFullScanBytes
	}
	requested := float64(HistoryPageRecords)
	if options.Full {
		requested = HistoryFullRows
	}
	if options.Limit != nil {
		requested = *options.Limit
	}
	recordLimit = math.Max(1, math.Min(recordLimit, math.Floor(requested)))
	load := func() (bool, error) {
		capacity := min(65536, scanBudget-65536-scanned)
		if capacity <= 0 {
			return false, nil
		}
		start, end := max(0, position-capacity), position
		if newer {
			start = position
			end = min(c.size, position+capacity)
		}
		if end <= start {
			return false, nil
		}
		buffer = make([]byte, end-start)
		if err := readExactly(ctx, file, buffer, start, "History changed during read"); err != nil {
			return false, err
		}
		scanned += int64(len(buffer))
		bufferStart = start
		bufferEnd = end
		return true, nil
	}
	for (newer && position < c.size || !newer && position > 0) && float64(len(entries)) < recordLimit {
		if err = check(ctx); err != nil {
			return Page{}, err
		}
		previous := position
		parts := [][]byte{}
		var count int64
		complete := false
		oversized := skipping
		end, start := position, position
		first := true
		for !complete {
			if len(buffer) == 0 || newer && position >= bufferEnd || !newer && position <= bufferStart {
				loaded, err := load()
				if err != nil {
					return Page{}, err
				}
				if !loaded {
					break
				}
			}
			var part []byte
			if newer {
				local := int(position - bufferStart)
				newline := bytes.IndexByte(buffer[local:], 10)
				stop := len(buffer)
				if newline >= 0 {
					newline += local
					stop = newline
				}
				part = buffer[local:stop]
				position = bufferStart + int64(stop)
				if newline >= 0 {
					position++
				}
				end = bufferStart + int64(stop)
				complete = newline >= 0 || position == c.size
			} else {
				local := int(position - bufferStart)
				if first && local > 0 && buffer[local-1] == 10 {
					local--
					position--
					end--
				}
				newline := bytes.LastIndexByte(buffer[:local], 10)
				stop := newline + 1
				part = buffer[stop:local]
				position = bufferStart + int64(stop)
				start = position
				complete = newline >= 0 || position == 0
			}
			count += int64(len(part))
			if !oversized && count <= HistorySemanticRecordBytes {
				parts = append(parts, part)
			} else if !oversized {
				oversized = true
				parts = nil
			}
			first = false
		}
		if !complete {
			if oversized {
				if !skipping {
					omitted++
				}
				skipping = true
			} else {
				position = previous
			}
			break
		}
		if oversized {
			if !skipping {
				omitted++
			}
			skipping = false
			continue
		}
		if count == 0 {
			continue
		}
		var raw []byte
		if len(parts) == 1 {
			raw = parts[0]
		} else {
			raw = make([]byte, 0, count)
			if newer {
				for _, part := range parts {
					raw = append(raw, part...)
				}
			} else {
				for index := len(parts) - 1; index >= 0; index-- {
					raw = append(raw, parts[index]...)
				}
			}
		}
		entry, err := parseEntry(raw)
		if err != nil {
			omitted++
			continue
		}
		if !isObject(entry) {
			continue
		}
		var object protocol.JSONObject
		_ = object.UnmarshalJSON(entry)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(object.Get("message"))
		rowCost := 1
		content := message.Get("content")
		if len(content) > 0 && content[0] == '[' {
			var rows []json.RawMessage
			_ = json.Unmarshal(content, &rows)
			if len(rows) > 1 {
				rowCost = len(rows)
			}
		}
		serialized, err := protocol.ParseAndStringifyJavaScriptJSON(entry)
		if err != nil {
			return Page{}, err
		}
		if (outputBytes+int64(len(serialized)) > byteBudget || renderedRows+rowCost > rowBudget) && len(entries) > 0 {
			position = previous
			break
		}
		renderedRows += rowCost
		byteStart := start
		if newer {
			byteStart = previous
		}
		entries = append(entries, Entry{entry, byteStart, end})
		outputBytes += int64(len(serialized))
	}
	after, err := file.Stat()
	if err != nil {
		return Page{}, err
	}
	if after.Ino != stat.Ino || after.Size < stat.Size || after.Size == stat.Size && after.MtimeNS != stat.MtimeNS {
		return Page{}, changed("Session history changed during read")
	}
	lower, upper := position, c.offset
	if newer {
		lower, upper = c.offset, position
	}
	afterAnchors, err := sourceAnchors()
	if err != nil {
		return Page{}, err
	}
	if anchorsBefore.Prefix != afterAnchors.Prefix || anchorsBefore.Tail != afterAnchors.Tail {
		return Page{}, changed("Session history was rewritten during read")
	}
	encode := func(offset int64, direction string, continuation bool) (*string, error) {
		object := c.object.Clone()
		raw, _ := json.Marshal(offset)
		object.Set("offset", raw)
		raw, _ = json.Marshal(direction)
		object.Set("direction", raw)
		raw, _ = json.Marshal(continuation)
		object.Set("skipping", raw)
		hash, err := boundary(offset)
		if err != nil {
			return nil, err
		}
		fingerprints := anchorsBefore
		fingerprints.Boundary = hash
		raw, _ = json.Marshal(fingerprints)
		object.Set("fingerprints", raw)
		encoded, err := object.MarshalJSON()
		if err != nil {
			return nil, err
		}
		value := base64.RawURLEncoding.EncodeToString(encoded)
		return &value, nil
	}
	var next, previous *string
	if lower > 0 {
		next, err = encode(lower, "older", !newer && skipping)
		if err != nil {
			return Page{}, err
		}
	}
	if upper < c.size {
		previous, err = encode(upper, "newer", newer && skipping)
		if err != nil {
			return Page{}, err
		}
	}
	truncated := false
	for _, entry := range entries {
		var object protocol.JSONObject
		_ = object.UnmarshalJSON(entry.Entry)
		if bytes.Equal(object.Get("bodyTruncated"), []byte("true")) {
			truncated = true
		}
	}
	if !newer {
		for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
			entries[left], entries[right] = entries[right], entries[left]
		}
	}
	return Page{entries, PageInfo{NextCursor: next, PreviousCursor: previous, HasMore: lower > 0, HistoryComplete: lower == 0 && upper == c.size && omitted == 0 && !truncated, ContentTruncated: truncated, SourceVersion: fmt.Sprintf("%s:%s:%d:%s", c.dev, c.ino, c.size, c.mtime), ScannedBytes: scanned, OmittedOversizedEntries: omitted}}, nil
}

type StreamOptions struct {
	StartOffset    int64
	EndOffset      *int64
	MaxRecordBytes *int64
	OnSkipped      func()
}
type StreamResult struct {
	SourceVersion    string `json:"sourceVersion"`
	OmittedRecords   int    `json:"omittedRecords"`
	OversizedRecords int    `json:"oversizedRecords"`
	ScannedBytes     int64  `json:"scannedBytes"`
	NextOffset       int64  `json:"nextOffset"`
}

// Stream matches the source's unadmitted forward source. Call Budget.With for
// metadata/context/recovery consumers, as those source services do themselves.
func (reader *Reader) Stream(ctx context.Context, path string, onEntry func(json.RawMessage, bool, int64) error, options StreamOptions) (result StreamResult, resultErr error) {
	file, err := reader.files.Open(path)
	if err != nil {
		return StreamResult{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			result = StreamResult{}
			resultErr = err
		}
	}()
	stat, err := file.Stat()
	if err != nil {
		return StreamResult{}, err
	}
	size := stat.Size
	if options.EndOffset != nil {
		size = min(size, *options.EndOffset)
	}
	first := options.StartOffset
	lineStart, next := first, first
	chunk := make([]byte, 65536)
	var parts [][]byte
	var length int64
	skipping := false
	omitted, oversized := 0, 0
	maxRecord := int64(HistoryRecordBytes)
	if options.MaxRecordBytes != nil {
		maxRecord = *options.MaxRecordBytes
	}
	flush := func(complete bool) error {
		if skipping {
			omitted++
			oversized++
			if options.OnSkipped != nil {
				options.OnSkipped()
			}
		} else if length > 0 {
			raw := bytes.Join(parts, nil)
			entry, err := parseEntry(raw)
			if err != nil {
				omitted++
				if options.OnSkipped != nil {
					options.OnSkipped()
				}
			} else if isObject(entry) {
				if err = onEntry(entry, complete, lineStart); err != nil {
					return err
				}
			}
		}
		parts = nil
		length = 0
		skipping = false
		return nil
	}
	for offset := first; offset < size; {
		if err = check(ctx); err != nil {
			return StreamResult{}, err
		}
		n, readErr := file.ReadAt(chunk[:min(int64(len(chunk)), size-offset)], offset)
		if readErr != nil && readErr != io.EOF {
			return StreamResult{}, readErr
		}
		if n == 0 {
			return StreamResult{}, changed("Session history changed during recovery")
		}
		offset += int64(n)
		for start := 0; start < n; {
			if err = check(ctx); err != nil {
				return StreamResult{}, err
			}
			newline := bytes.IndexByte(chunk[start:n], 10)
			end := n
			if newline >= 0 {
				end = start + newline
			}
			if !skipping {
				length += int64(end - start)
				if length > maxRecord {
					skipping = true
					parts = nil
				} else {
					parts = append(parts, append([]byte(nil), chunk[start:end]...))
				}
			}
			start = end + 1
			if end < n {
				if err = flush(true); err != nil {
					return StreamResult{}, err
				}
				lineStart = offset - int64(n) + int64(end) + 1
				next = lineStart
			}
		}
	}
	if length > 0 || skipping {
		if err = flush(false); err != nil {
			return StreamResult{}, err
		}
	}
	after, err := file.Stat()
	if err != nil {
		return StreamResult{}, err
	}
	if after.Size < stat.Size || after.Size == stat.Size && after.MtimeNS != stat.MtimeNS {
		return StreamResult{}, changed("Session changed during recovery; retry")
	}
	return StreamResult{stat.version(), omitted, oversized, size - first, next}, nil
}
