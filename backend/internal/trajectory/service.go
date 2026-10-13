package trajectory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

const appendMaxBytes = 4 * 1024 * 1024
const turnScanMaxBytes = 256 * 1024 * 1024

type Service struct {
	Reader   *sessionstore.Reader
	mu       sync.Mutex
	turns    map[string]*turnIndex
	snapshot map[string]*snapshotIndex
	order    []string
}

func New(reader *sessionstore.Reader) *Service {
	return &Service{Reader: reader, turns: map[string]*turnIndex{}, snapshot: map[string]*snapshotIndex{}}
}

type Source struct {
	Path, SnapshotPath, Scope string
	Main                      bool
}
type PageOptions struct{ Cursor, After string }
type Page struct {
	Rows            []Row   `json:"rows"`
	Snapshots       *[]Row  `json:"snapshots,omitempty"`
	TurnBase        *int    `json:"turnBase"`
	OlderCursor     *string `json:"olderCursor"`
	TailToken       string  `json:"tailToken"`
	HistoryComplete bool    `json:"historyComplete"`
	Omitted         int     `json:"omittedOversizedEntries"`
	Missing         bool    `json:"sourceMissing,omitempty"`
}
type Detail struct {
	RowID     string   `json:"rowId"`
	Entries   []Object `json:"entries"`
	Truncated bool     `json:"truncated"`
}
type tail struct {
	V        int    `json:"v"`
	Dev      string `json:"dev"`
	Ino      string `json:"ino"`
	Offset   int64  `json:"offset"`
	Prefix   string `json:"prefix"`
	Boundary string `json:"boundary"`
}

var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var hash16 = regexp.MustCompile(`^[a-f0-9]{16}$`)
var agentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var locPattern = regexp.MustCompile(`^\d{1,15}-\d{1,15}(?:,\d{1,15}-\d{1,15})*$`)
var rowPattern = regexp.MustCompile(`(?s)^([ucmat]):(.+)$`)

func api(status int, code, msg string) error {
	return &sessionstore.APIError{StatusCode: status, Code: code, Message: msg}
}
func changed(msg string) error { return api(409, "HISTORY_CHANGED", msg) }
func AgentID(v string) (string, error) {
	v = strings.TrimPrefix(v, "agent-")
	if v != "" && !agentPattern.MatchString(v) {
		return "", api(400, "BAD_REQUEST", "Invalid agentId")
	}
	return v, nil
}
func (s *Service) reader() *sessionstore.Reader {
	if s.Reader == nil {
		panic("trajectory requires the shared history reader")
	}
	return s.Reader
}
func exact(ctx context.Context, f sessionstore.File, start, length int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if start < 0 || length < 0 {
		return nil, api(400, "BAD_REQUEST", "Invalid byte range")
	}
	b := make([]byte, length)
	n, err := f.ReadAt(b, start)
	if int64(n) != length {
		return nil, changed("Trajectory source changed during read")
	}
	if err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hash(ctx context.Context, f sessionstore.File, start, end int64) (string, error) {
	b, err := exact(ctx, f, start, end-start)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}
func makeToken(ctx context.Context, f sessionstore.File, stat sessionstore.Snapshot, offset int64) (string, error) {
	prefix, err := hash(ctx, f, 0, min(4096, offset))
	if err != nil {
		return "", err
	}
	boundary, err := hash(ctx, f, max(0, offset-4096), offset)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw(tail{1, stat.Dev, stat.Ino, offset, prefix, boundary})), nil
}
func decodeToken(v string) (tail, error) {
	var t tail
	if len(v) > 1024 {
		return t, api(400, "BAD_REQUEST", "Invalid trajectory tail token")
	}
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil || json.Unmarshal(b, &t) != nil || t.V != 1 || t.Offset < 0 || t.Offset > 9007199254740991 || !hex64.MatchString(t.Prefix) || !hex64.MatchString(t.Boundary) {
		return t, api(400, "BAD_REQUEST", "Invalid trajectory tail token")
	}
	return t, nil
}

// Scan never retains a record above the semantic budget. Its bounded head is
// available for tool-result stubs; incomplete trailing lines await the append.
func Scan(ctx context.Context, f sessionstore.File, start, end int64, limit int64, emit func(int64, int64, []byte, []byte)) (int64, error) {
	chunk := make([]byte, 256*1024)
	lineStart := start
	parts := []byte{}
	head := []byte{}
	oversized := false
	for offset := start; offset < end; {
		if e := ctx.Err(); e != nil {
			return 0, e
		}
		n, e := f.ReadAt(chunk[:min(int64(len(chunk)), end-offset)], offset)
		if n == 0 {
			return 0, changed("Trajectory source changed during read")
		}
		if e != nil && e != io.EOF {
			return 0, e
		}
		for local := 0; local < n; {
			at := bytes.IndexByte(chunk[local:n], 10)
			stop := n
			if at >= 0 {
				stop = local + at
			}
			part := chunk[local:stop]
			if len(head) < 65536 {
				head = append(head, part[:min(len(part), 65536-len(head))]...)
			}
			if !oversized {
				if int64(len(parts)+len(part)) > limit {
					oversized = true
					parts = nil
				} else {
					parts = append(parts, part...)
				}
			}
			if stop == n {
				break
			}
			lineEnd := offset + int64(stop)
			emit(lineStart, lineEnd, parts, head)
			lineStart = lineEnd + 1
			parts = nil
			head = nil
			oversized = false
			local = stop + 1
		}
		offset += int64(n)
	}
	return lineStart, nil
}
func lastComplete(ctx context.Context, f sessionstore.File, size int64) (int64, error) {
	end := size
	for end > 0 && size-end < sessionstore.HistorySemanticRecordBytes {
		start := max(0, end-65536)
		b, e := exact(ctx, f, start, end-start)
		if e != nil {
			return 0, e
		}
		if at := bytes.LastIndexByte(b, 10); at >= 0 {
			return start + int64(at) + 1, nil
		}
		end = start
	}
	if end == 0 {
		return 0, nil
	}
	return size, nil
}
func (s *Service) before(ctx context.Context, path string, end int64, main bool) (*int, []string, error) {
	var count *int
	recent := []string{}
	err := s.reader().Budget().With(ctx, sessionstore.MetadataLane, func() error {
		f, e := (sessionstore.OSFileSystem{}).Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil {
			return e
		}
		if end > stat.Size {
			return changed("Trajectory source changed")
		}
		count, e = s.countBefore(ctx, f, path, end, main)
		if e != nil {
			return e
		}
		start := max(0, end-256*1024)
		if start > 0 {
			b, e := exact(ctx, f, start, min(65536, end-start))
			if e != nil {
				return e
			}
			if at := bytes.IndexByte(b, 10); at >= 0 {
				start += int64(at) + 1
			} else {
				start = end
			}
		}
		_, e = Scan(ctx, f, start, end, sessionstore.HistorySemanticRecordBytes, func(_, _ int64, b, head []byte) {
			v := parse(b)
			if v != nil && !skipped(v, main) {
				if ts := text(v["timestamp"]); ts != "" {
					recent = append(recent, ts)
					if len(recent) > 16 {
						recent = recent[1:]
					}
				}
			}
		})
		return e
	})
	return count, recent, err
}
func (s *Service) Page(ctx context.Context, source Source, o PageOptions) (Page, error) {
	if o.Cursor != "" && o.After != "" {
		return Page{}, api(400, "BAD_REQUEST", "cursor and after are mutually exclusive")
	}
	var snapshots *[]Row
	if o.Cursor == "" {
		rows, _, e := s.snapshots(ctx, source.SnapshotPath, source.Scope)
		if e != nil {
			return Page{}, e
		}
		snapshots = &rows
	}
	if o.After != "" {
		return s.append(ctx, source, o.After, snapshots)
	}
	page, e := s.reader().ReadPage(ctx, source.Path, sessionstore.PageOptions{Cursor: o.Cursor, Budget: &sessionstore.PageBudget{Records: 1000, Rows: 2000, Bytes: 2 * 1024 * 1024}, Measure: RecordCost, CaptureOversized: true})
	if os.IsNotExist(e) {
		if o.Cursor != "" {
			return Page{}, changed("Trajectory source was removed; reload the newest page")
		}
		zero := 0
		return Page{Rows: []Row{}, Snapshots: snapshots, TurnBase: &zero, HistoryComplete: true, Missing: true, TailToken: base64.RawURLEncoding.EncodeToString(raw(tail{1, "", "", 0, digest(nil), digest(nil)}))}, nil
	}
	if e != nil {
		return Page{}, e
	}
	entries, represented := projectionEntries(page)
	parts := strings.Split(page.Page.SourceVersion, ":")
	offset, _ := strconv.ParseInt(parts[2], 10, 64)
	if len(entries) > 0 {
		offset = entries[0].Start
	}
	turn, recent, e := s.before(ctx, source.Path, offset, source.Main)
	if e != nil {
		return Page{}, e
	}
	token := ""
	if o.Cursor == "" {
		e = s.reader().Budget().With(ctx, sessionstore.MetadataLane, func() error {
			f, e := (sessionstore.OSFileSystem{}).Open(source.Path)
			if e != nil {
				return e
			}
			defer f.Close()
			stat, e := f.Stat()
			if e != nil {
				return e
			}
			size, _ := strconv.ParseInt(parts[2], 10, 64)
			if stat.Dev != parts[0] || stat.Ino != parts[1] || stat.Size < size {
				return changed("Trajectory source changed during read")
			}
			end, e := lastComplete(ctx, f, size)
			if e != nil {
				return e
			}
			token, e = makeToken(ctx, f, stat, end)
			return e
		})
		if e != nil {
			return Page{}, e
		}
	}
	omitted := max(0, page.Page.OmittedOversizedEntries-represented)
	return Page{Rows: Project(entries, turn, source.Main, recent), Snapshots: snapshots, TurnBase: turn, OlderCursor: page.Page.NextCursor, TailToken: token, HistoryComplete: page.Page.HistoryComplete || represented > 0 && omitted == 0 && page.Page.NextCursor == nil && page.Page.PreviousCursor == nil && !page.Page.ContentTruncated, Omitted: omitted}, nil
}
func (s *Service) append(ctx context.Context, source Source, value string, snapshots *[]Row) (Page, error) {
	t, e := decodeToken(value)
	if e != nil {
		return Page{}, e
	}
	entries := []Entry{}
	omitted := 0
	token := ""
	err := s.reader().Budget().With(ctx, sessionstore.PageLane, func() error {
		f, e := (sessionstore.OSFileSystem{}).Open(source.Path)
		if os.IsNotExist(e) {
			return changed("Trajectory source was removed; reload the newest page")
		}
		if e != nil {
			return e
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil {
			return e
		}
		if stat.Dev != t.Dev || stat.Ino != t.Ino || stat.Size < t.Offset {
			return changed("Session history was rewritten; reload the newest page")
		}
		prefix, e := hash(ctx, f, 0, min(4096, t.Offset))
		if e != nil {
			return e
		}
		boundary, e := hash(ctx, f, max(0, t.Offset-4096), t.Offset)
		if e != nil {
			return e
		}
		if prefix != t.Prefix || boundary != t.Boundary {
			return changed("Session history was rewritten; reload the newest page")
		}
		if stat.Size-t.Offset > appendMaxBytes {
			return api(409, "TRAJECTORY_APPEND_TOO_LARGE", "Too much new history for a live append; reload the newest page")
		}
		next, e := Scan(ctx, f, t.Offset, stat.Size, sessionstore.HistorySemanticRecordBytes, func(start, end int64, b, head []byte) {
			v := parse(b)
			if v == nil {
				omitted++
				return
			}
			entries = append(entries, Entry{Value: v, Start: start, End: end})
		})
		if e != nil {
			return e
		}
		after, e := f.Stat()
		if e != nil {
			return e
		}
		if stat.Ino != after.Ino || stat.Size > after.Size || stat.Size == after.Size && stat.MtimeNS != after.MtimeNS {
			return changed("Session history changed during read")
		}
		token, e = makeToken(ctx, f, stat, next)
		return e
	})
	if err != nil {
		return Page{}, err
	}
	turn, recent, e := s.before(ctx, source.Path, t.Offset, source.Main)
	if e != nil {
		return Page{}, e
	}
	return Page{Rows: Project(entries, turn, source.Main, recent), Snapshots: snapshots, TurnBase: turn, TailToken: token, HistoryComplete: omitted == 0, Omitted: omitted}, nil
}
func ParseLoc(v string) ([][2]int64, error) {
	if len(v) > 64*48 || !locPattern.MatchString(v) {
		return nil, api(400, "BAD_REQUEST", "Invalid loc parameter")
	}
	parts := strings.Split(v, ",")
	if len(parts) > 64 {
		return nil, api(400, "BAD_REQUEST", "Too many loc ranges")
	}
	ranges := [][2]int64{}
	total := int64(0)
	for _, p := range parts {
		pair := strings.Split(p, "-")
		a, ea := strconv.ParseInt(pair[0], 10, 64)
		b, eb := strconv.ParseInt(pair[1], 10, 64)
		if ea != nil || eb != nil || a < 0 || b <= a || b > 9007199254740991 {
			return nil, api(400, "BAD_REQUEST", "Invalid loc range")
		}
		if b-a <= sessionstore.HistorySemanticRecordBytes {
			total += b - a
		}
		ranges = append(ranges, [2]int64{a, b})
	}
	if total > 8*1024*1024 {
		return nil, api(400, "BAD_REQUEST", "loc ranges exceed the detail budget")
	}
	return ranges, nil
}
func belongs(kind, key string, e Object, start int64) bool {
	m := object(e["message"])
	fallback := fmt.Sprintf("@%d", start)
	switch kind {
	case "u", "c", "m":
		return e["uuid"] == key || e["uuid"] == nil && key == fallback
	case "a":
		return m["id"] == key || m["id"] == nil && (e["uuid"] == key || e["uuid"] == nil && key == fallback)
	case "t":
		for _, b := range array(m["content"]) {
			v := object(b)
			if v["type"] == "tool_use" && v["id"] == key || v["type"] == "tool_result" && v["tool_use_id"] == key {
				return true
			}
		}
	}
	return false
}
func bound(v any, remaining *int, truncated *bool, depth int) any {
	switch x := v.(type) {
	case string:
		n := min(256*1024, max(0, *remaining))
		size := len(utf16chars(x))
		*remaining -= min(size, n)
		if size > n {
			*truncated = true
			return chars(x, n) + "\n… [truncated]"
		}
		return x
	case []any:
		if depth > 64 {
			*truncated = true
			return "[truncated]"
		}
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = bound(v, remaining, truncated, depth+1)
		}
		return out
	case map[string]any:
		if depth > 64 {
			*truncated = true
			return "[truncated]"
		}
		out := Object{}
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = bound(x[k], remaining, truncated, depth+1)
		}
		return out
	default:
		return v
	}
}
func (s *Service) Detail(ctx context.Context, source Source, id, loc string) (Detail, error) {
	m := rowPattern.FindStringSubmatch(id)
	if len(m) < 3 || len(utf16chars(m[2])) > 1024 {
		return Detail{}, api(400, "BAD_REQUEST", "Invalid trajectory row id")
	}
	ranges, e := ParseLoc(loc)
	if e != nil {
		return Detail{}, e
	}
	byStart := map[int64][2]int64{}
	for _, r := range ranges {
		byStart[r[0]] = r
	}
	ranges = nil
	for _, r := range byStart {
		ranges = append(ranges, r)
	}
	sort.Slice(ranges, func(a, b int) bool { return ranges[a][0] < ranges[b][0] })
	out := Detail{RowID: id, Entries: []Object{}}
	remaining := 4 * 1024 * 1024
	err := s.reader().Budget().With(ctx, sessionstore.PageLane, func() error {
		f, e := (sessionstore.OSFileSystem{}).Open(source.Path)
		if os.IsNotExist(e) {
			return api(404, "NOT_FOUND", "Trajectory source not found")
		}
		if e != nil {
			return e
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil {
			return e
		}
		for _, r := range ranges {
			start, end := r[0], r[1]
			if end > stat.Size {
				return api(400, "BAD_REQUEST", "loc range is outside the transcript")
			}
			before, after := byte(10), byte(10)
			if start > 0 {
				b, e := exact(ctx, f, start-1, 1)
				if e != nil {
					return e
				}
				before = b[0]
			}
			if end < stat.Size {
				b, e := exact(ctx, f, end, 1)
				if e != nil {
					return e
				}
				after = b[0]
			}
			if end-start > sessionstore.HistorySemanticRecordBytes {
				b, e := exact(ctx, f, start, min(65536, end-start))
				if e != nil {
					return e
				}
				stub := Oversized(b)
				ok := false
				for _, v := range array(stub["toolUseIds"]) {
					ok = ok || v == m[2]
				}
				if before != 10 || after != 10 || m[1] != "t" || stub["type"] != "user" || !ok {
					return api(409, "TRAJECTORY_ROW_MISMATCH", "Transcript record no longer matches this row; reload the trajectory")
				}
				out.Truncated = true
				continue
			}
			b, e := exact(ctx, f, start, end-start)
			if e != nil {
				return e
			}
			trimmed := bytes.TrimSuffix(b, []byte{10})
			v := parse(trimmed)
			if before != 10 || (after != 10 && len(trimmed) == len(b)) || v == nil || !belongs(m[1], m[2], v, start) {
				return api(409, "TRAJECTORY_ROW_MISMATCH", "Transcript record no longer matches this row; reload the trajectory")
			}
			out.Entries = append(out.Entries, object(bound(v, &remaining, &out.Truncated, 0)))
		}
		after, e := f.Stat()
		if e != nil {
			return e
		}
		if after.Ino != stat.Ino || after.Size < stat.Size || after.Size == stat.Size && after.MtimeNS != stat.MtimeNS {
			return changed("Trajectory changed during row read")
		}
		return nil
	})
	return out, err
}
func utf16chars(s string) []uint16 { return utf16.Encode([]rune(s)) }
