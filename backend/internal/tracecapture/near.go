package tracecapture

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

const NearLimit = 50
const NearScanBytes = 32 * 1024 * 1024
const NearScanRecords = 4000

var nearTime = regexp.MustCompile(`"(completedAt|startedAt|timestamp)"\s*:\s*"([^"\\]{1,64})"`)

func nearKey(b []byte) *time.Time {
	var started *time.Time
	event := bytes.HasPrefix(b, []byte(`{"type":"event"`))
	for _, m := range nearTime.FindAllSubmatch(b, -1) {
		t, e := time.Parse(time.RFC3339Nano, string(m[2]))
		if e != nil {
			continue
		}
		if event {
			if string(m[1]) == "timestamp" {
				return &t
			}
			continue
		}
		if string(m[1]) == "completedAt" {
			return &t
		}
		if string(m[1]) == "startedAt" && started == nil {
			started = &t
		}
	}
	return started
}
func nearResult(id string, calls []CallLocator, token string, limited bool) protocol.JSONObject {
	sort.SliceStable(calls, func(a, b int) bool {
		if calls[a].StartedAt == calls[b].StartedAt {
			return calls[a].ByteStart < calls[b].ByteStart
		}
		return calls[a].StartedAt < calls[b].StartedAt
	})
	limited = limited || len(calls) > NearLimit
	calls = calls[:min(NearLimit, len(calls))]
	shells := []protocol.JSONObject{}
	locs := obj()
	for _, c := range calls {
		shells = append(shells, shellCall(id, c))
		locs.Set(c.ID, jstr(fmt.Sprintf("%d-%d", c.ByteStart, c.ByteStart+c.ByteLength)))
	}
	out := obj("calls", shells, "revisionToken", token, "captured", true, "locs", locs)
	if limited {
		out.Set("limited", raw(true))
	}
	return out
}
func (i *Index) callsNear(ctx context.Context, id, from, to string) (source *SourceRecord, calls []CallLocator, e error) {
	e = i.DB.Read(ctx, func(op *localindex.Operation) error {
		source, e = readSource(op, id)
		if e != nil || source == nil {
			return e
		}
		rows, e := op.All(`SELECT * FROM trace_calls WHERE session_id=? AND started_at>=? AND started_at<=? ORDER BY started_at,first_ordinal LIMIT ?`, id, from, to, NearLimit+1)
		for _, r := range rows {
			calls = append(calls, callRow(r))
		}
		return e
	})
	return
}
func (s *Service) Near(ctx context.Context, id string, from, to time.Time) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, e
	}
	from, to = from.Truncate(time.Millisecond), to.Truncate(time.Millisecond)
	if from.IsZero() || to.IsZero() || to.Before(from) || to.Sub(from) > time.Hour {
		return protocol.JSONObject{}, &Error{"TRACE_NEAR_INVALID_WINDOW", "from/to must be ISO timestamps with from <= to and a window of at most one hour"}
	}
	id = normalizeSessionID(id)
	path := s.filePath(id)
	stat, e := os.Stat(path)
	if os.IsNotExist(e) {
		return obj("calls", []protocol.JSONObject{}, "captured", false), nil
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if s.mode() == "on" && stat.Size() <= WindowBytesLimit {
		projection, e := s.ensure(ctx, id, path, ScanOptions{})
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if projection != nil {
			if index := s.getIndex(); index != nil {
				source, calls, e := index.callsNear(ctx, id, iso(from), iso(to))
				if e != nil {
					s.quarantine(e)
				} else if source != nil && source.State == "ready" && source.FilePath == path && source.WindowStartByte == 0 && !source.ScanTruncated {
					if snapshot, ok := snapshotFromSource(*source); ok && localindex.DetectSourceChange(ctx, path, snapshot.Fingerprint, ParserVersion).Kind == "unchanged" {
						token := digest([]byte("0:" + localindex.SerializeFingerprint(snapshot.Fingerprint)))
						return nearResult(id, calls, token, false), nil
					}
				}
			}
		}
	}
	s.scanMu.Lock()
	reserved := false
	select {
	case s.scanGate <- struct{}{}:
		reserved = true
	default:
		if s.scanWaiters >= 8 {
			s.scanMu.Unlock()
			return protocol.JSONObject{}, &Error{"TRACE_INDEX_BUSY", "Trace reader queue is full; retry shortly"}
		}
		s.scanWaiters++
	}
	s.scanMu.Unlock()
	if !reserved {
		select {
		case s.scanGate <- struct{}{}:
		case <-ctx.Done():
			s.scanMu.Lock()
			s.scanWaiters--
			s.scanMu.Unlock()
			return protocol.JSONObject{}, ctx.Err()
		}
		s.scanMu.Lock()
		s.scanWaiters--
		s.scanMu.Unlock()
	}
	defer func() { <-s.scanGate }()
	f, e := (sessionstore.OSFileSystem{}).Open(path)
	if e != nil {
		if os.IsNotExist(e) {
			return obj("calls", []protocol.JSONObject{}, "captured", false), nil
		}
		return protocol.JSONObject{}, e
	}
	defer f.Close()
	snapshot, e := f.Stat()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	size := snapshot.Size
	read := func(position, n int64) ([]byte, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		n = min(n, max(0, size-position))
		b := make([]byte, n)
		read, e := f.ReadAt(b, position)
		if e != nil && e != io.EOF {
			return nil, e
		}
		return b[:read], nil
	}
	probe := func(offset int64) (int64, *time.Time, error) {
		line := offset
		if offset > 0 {
			position := offset - 1
			for {
				if position >= size || position-offset > RecordBytesLimit {
					return 0, nil, nil
				}
				b, e := read(position, 65536)
				if e != nil {
					return 0, nil, e
				}
				if len(b) == 0 {
					return 0, nil, nil
				}
				if at := bytes.IndexByte(b, 10); at >= 0 {
					line = position + int64(at) + 1
					break
				}
				position += int64(len(b))
			}
		}
		if line >= size {
			return 0, nil, nil
		}
		b, e := read(line, 16*1024)
		if e != nil {
			return 0, nil, e
		}
		if at := bytes.IndexByte(b, 10); at >= 0 {
			b = b[:at]
		}
		return line, nearKey(b), nil
	}
	low, high := int64(0), size
	target := from.Add(-time.Minute)
	for high-low > 65536 {
		mid := low + (high-low)/2
		line, key, e := probe(mid)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if line == 0 || line >= high || key == nil || !key.Before(target) {
			high = mid
		} else {
			low = line + 1
		}
	}
	start := max(0, low-1)
	reader := bufio.NewReaderSize(io.NewSectionReader(f, start, size-start), 64*1024)
	position := start
	skip := low > 0
	total, records := int64(0), 0
	matches := map[string]CallLocator{}
	limited := false
scan:
	for position < size {
		if e := ctx.Err(); e != nil {
			return protocol.JSONObject{}, e
		}
		if total >= NearScanBytes || records >= NearScanRecords {
			limited = true
			break
		}
		lineStart := position
		line := []byte{}
		oversized := false
		for {
			part, e := reader.ReadSlice('\n')
			position += int64(len(part))
			total += int64(len(part))
			if !oversized && !skip {
				if len(line)+len(part) > RecordBytesLimit+1 {
					oversized = true
					line = nil
				} else {
					line = append(line, part...)
				}
			}
			if e == bufio.ErrBufferFull {
				if total >= NearScanBytes {
					limited = true
					break scan
				}
				continue
			}
			if e != nil && e != io.EOF {
				return protocol.JSONObject{}, e
			}
			if e == io.EOF {
				break scan
			}
			break
		}
		if skip {
			skip = false
			continue
		}
		records++
		if oversized {
			continue
		}
		key := nearKey(line[:min(16*1024, len(line))])
		if key != nil && key.After(to.Add(30*time.Minute)) {
			break
		}
		call, _ := parseEntry(line)
		if !call.IsObject() {
			continue
		}
		started, e := time.Parse(time.RFC3339Nano, text(call.Get("startedAt")))
		started = started.Truncate(time.Millisecond)
		if e != nil || started.Before(from) || started.After(to) {
			continue
		}
		locator := callLocator(call, int64(records), lineStart, int64(len(line)), int64(records))
		if locatorFits(locator) {
			matches[locator.ID] = locator
		}
	}
	after, e := f.Stat()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if after.Dev != snapshot.Dev || after.Ino != snapshot.Ino || after.Size < snapshot.Size || after.Size == snapshot.Size && after.MtimeNS != snapshot.MtimeNS {
		return protocol.JSONObject{}, &Error{"TRACE_PAGE_STALE", "Trace source changed during near lookup"}
	}
	calls := []CallLocator{}
	for _, c := range matches {
		calls = append(calls, c)
	}
	token := "file:" + digest([]byte(fmt.Sprintf("%s:%s:%d:%s", snapshot.Dev, snapshot.Ino, snapshot.Size, snapshot.MtimeNS)))
	return nearResult(id, calls, token, limited), nil
}
func (s *Service) CallAt(ctx context.Context, id, callID string, at *[2]int64) (protocol.JSONObject, error) {
	if at != nil {
		start, end := at[0], at[1]
		if start >= 0 && end > start && end-start <= RecordBytesLimit+1 && safe(end) {
			path := s.filePath(id)
			f, e := os.Open(path)
			if os.IsNotExist(e) {
				return protocol.JSONObject{}, nil
			}
			if e != nil {
				return protocol.JSONObject{}, e
			}
			defer f.Close()
			stat, e := f.Stat()
			if e != nil {
				return protocol.JSONObject{}, e
			}
			if end <= stat.Size() {
				a, b := max(0, start-1), min(stat.Size(), end+1)
				if e := ctx.Err(); e != nil {
					return protocol.JSONObject{}, e
				}
				data := make([]byte, b-a)
				n, e := f.ReadAt(data, a)
				if e != nil && e != io.EOF {
					return protocol.JSONObject{}, e
				}
				if int64(n) == b-a && (start == 0 || data[0] == 10) {
					body := data[start-a : end-a]
					line := bytes.TrimSuffix(body, []byte{10})
					if (len(body) != len(line) || b == end || data[len(data)-1] == 10) && !bytes.ContainsRune(line, '\n') {
						call, _ := parseEntry(line)
						if call.IsObject() && text(call.Get("id")) == callID {
							return call, nil
						}
					}
				}
			}
		}
	}
	return s.Call(ctx, id, callID, "")
}
