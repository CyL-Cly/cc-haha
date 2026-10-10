package tracecapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
)

type ScanOptions struct {
	ByteStart, Ordinal int64
	SkipLine           bool
	RemainingRecords   *int64
}
type ScanSnapshot struct {
	Calls                 []CallLocator
	Events                []EventLocator
	NextOrdinal           int64
	Fingerprint           localindex.Fingerprint
	OversizedRecords      int64
	ScanTruncated         bool
	WindowStartByte       int64
	OversizedContinuation bool
}
type Window struct {
	Offset           int64  `json:"offset"`
	Limit            int64  `json:"limit"`
	TotalCalls       int64  `json:"totalCalls"`
	TotalEvents      int64  `json:"totalEvents"`
	HasMore          bool   `json:"hasMore"`
	RevisionToken    string `json:"revisionToken"`
	State            string `json:"state"`
	OversizedRecords int64  `json:"oversizedRecords"`
	StartByte        int64  `json:"startByte"`
	ScannedBytes     int64  `json:"scannedBytes"`
	FileBytes        int64  `json:"fileBytes"`
	RecordLimit      int    `json:"recordLimit"`
	RecordBytesLimit int    `json:"recordBytesLimit"`
	NextScanCursor   string `json:"nextScanCursor,omitempty"`
}
type OverviewOptions struct {
	Offset, Limit             int64
	RevisionToken, ScanCursor string
}

func scanFile(ctx context.Context, path string, scan ScanOptions, appendSource *SourceRecord, compare func(string, string) int) (*ScanSnapshot, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	before, e := localindex.SnapshotPath(path)
	if e != nil {
		return nil, e
	}
	if !localindex.FingerprintMatchesFile(f, localindex.Fingerprint{Size: before.Size, MtimeMs: before.MtimeMs, CtimeMs: before.CtimeMs, FileIdentity: before.FileIdentity}) {
		return nil, nil
	}
	position := scan.ByteStart
	windowStart := position
	ordinal := scan.Ordinal
	oversized := int64(0)
	if appendSource != nil {
		position = appendSource.IndexedBytes
		windowStart = appendSource.WindowStartByte
		ordinal = val(appendSource.NextOrdinal, 0)
		oversized = appendSource.OversizedRecords
		old := localindex.DeserializeFingerprint(val(appendSource.Fingerprint, ""))
		if old == nil {
			return nil, nil
		}
		change := localindex.DetectSourceChange(ctx, path, *old, ParserVersion)
		if change.Kind != "append" && change.Kind != "unchanged" {
			return nil, nil
		}
	}
	if position > info.Size() {
		return nil, &Error{"TRACE_PAGE_STALE", "Trace file changed; restart from the first window"}
	}
	indexed := position
	lineStart := position
	fragment := []byte{}
	skipping := scan.SkipLine
	records := int64(0)
	recordBudget := val(scan.RemainingRecords, int64(WindowRecordLimit))
	truncated, continuation := false, false
	calls := []CallLocator{}
	positions := map[string]int{}
	events := []EventLocator{}
	stop := false
	for position < before.Size && !stop {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if records >= recordBudget {
			truncated = true
			break
		}
		chunk := make([]byte, min(int64(256*1024), before.Size-position))
		n, e := f.ReadAt(chunk, position)
		if n == 0 {
			return nil, e
		}
		if e != nil && e != io.EOF {
			return nil, e
		}
		chunk = chunk[:n]
		start := 0
		for start < len(chunk) {
			end := bytes.IndexByte(chunk[start:], '\n')
			if end < 0 {
				part := chunk[start:]
				if !skipping {
					if len(fragment)+len(part) > RecordBytesLimit {
						fragment = nil
						skipping = true
					} else {
						fragment = append(fragment, part...)
					}
				}
				break
			}
			end += start
			part := chunk[start : end+1]
			if skipping || len(fragment)+len(part) > RecordBytesLimit {
				oversized++
				ordinal++
			} else {
				line := part
				if len(fragment) > 0 {
					fragment = append(fragment, part...)
					line = fragment
				}
				call, event := parseEntry(line)
				if call.IsObject() {
					if e := validateCallShape(call); e != nil {
						return nil, e
					}
					first := ordinal
					if n, ok := positions[text(call.Get("id"))]; ok {
						first = val(calls[n].FirstOrdinal, calls[n].Ordinal)
					}
					c := callLocator(call, ordinal, lineStart, int64(len(line)), first)
					if !locatorFits(c) {
						oversized++
					} else {
						if n, ok := positions[c.ID]; ok {
							calls[n] = c
						} else {
							positions[c.ID] = len(calls)
							calls = append(calls, c)
						}
					}
				}
				if event.IsObject() {
					v := eventLocator(event, ordinal, lineStart, int64(len(line)))
					if !locatorFits(v) {
						oversized++
					} else {
						if v.Title != nil {
							v.Title = ptr(text(slice(jstr(*v.Title), 256)))
						}
						if v.Message != nil {
							v.Message = ptr(text(slice(jstr(*v.Message), 512)))
						}
						events = append(events, v)
					}
				}
				ordinal++
			}
			skipping = false
			indexed = position + int64(end) + 1
			lineStart = indexed
			fragment = nil
			records++
			start = end + 1
			if records >= recordBudget || indexed-windowStart >= WindowBytesLimit {
				truncated = indexed < before.Size
				stop = true
				break
			}
		}
		position += int64(n)
		if skipping && position-windowStart >= WindowBytesLimit {
			oversized++
			indexed = position
			truncated = position < before.Size
			continuation = truncated
			if !truncated {
				ordinal++
			}
			skipping = false
			stop = true
		}
	}
	if !truncated && skipping {
		oversized++
		indexed = before.Size
		ordinal++
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	after, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !localindex.FingerprintMatchesInfo(after, localindex.Fingerprint{Size: before.Size, MtimeMs: before.MtimeMs, CtimeMs: before.CtimeMs, FileIdentity: before.FileIdentity}) {
		return nil, nil
	}
	fp, e := localindex.CaptureFingerprint(ctx, path, indexed, ParserVersion)
	if e != nil {
		return nil, e
	}
	if fp.Size != before.Size || fp.MtimeMs != before.MtimeMs || fp.CtimeMs != before.CtimeMs || val(fp.FileIdentity, "") != val(before.FileIdentity, "") {
		return nil, nil
	}
	sortedCalls(calls, compare)
	sortedEvents(events, compare)
	return &ScanSnapshot{calls, events, ordinal, *fp, oversized, truncated, windowStart, continuation}, nil
}
func resolveScan(ctx context.Context, path, cursor string) (ScanOptions, error) {
	if cursor == "" {
		return ScanOptions{}, nil
	}
	stale := &Error{"TRACE_PAGE_STALE", "Trace file changed or cursor is invalid; restart from the first window"}
	if len(cursor) > 4096 {
		return ScanOptions{}, stale
	}
	b := decodeLooseBase64(cursor)
	var e error
	if e != nil {
		return ScanOptions{}, stale
	}
	o := object(b)
	fp := localindex.DeserializeFingerprint(text(o.Get("fingerprint")))
	start, ordinal := number(o.Get("byteStart")), number(o.Get("ordinal"))
	if fp == nil || !numeric(o.Get("byteStart")) || !numeric(o.Get("ordinal")) || !safe(int64(start)) || float64(int64(start)) != start || !safe(int64(ordinal)) || float64(int64(ordinal)) != ordinal || int64(start) != fp.IndexedBytes {
		return ScanOptions{}, stale
	}
	change := localindex.DetectSourceChange(ctx, path, *fp, ParserVersion)
	if change.Kind != "unchanged" {
		return ScanOptions{}, stale
	}
	return ScanOptions{ByteStart: int64(start), Ordinal: int64(ordinal), SkipLine: bytes.Equal(bytes.TrimSpace(o.Get("skipLine")), []byte("true"))}, nil
}
func windowMetadata(s ScanSnapshot, calls, events int64, o OverviewOptions) (Window, error) {
	offset := max(0, o.Offset)
	limit := o.Limit
	if limit == 0 {
		limit = OverviewLimit
	}
	limit = max(1, min(int64(OverviewLimit), limit))
	fingerprint := localindex.SerializeFingerprint(s.Fingerprint)
	token := digest([]byte(integerString(s.WindowStartByte) + ":" + fingerprint))
	if o.RevisionToken != "" && o.RevisionToken != token {
		return Window{}, &Error{"TRACE_PAGE_STALE", "Trace file changed; reload this trace window before paging"}
	}
	state := "ready"
	if s.ScanTruncated || s.OversizedRecords > 0 {
		state = "limited"
	}
	w := Window{Offset: offset, Limit: limit, TotalCalls: calls, TotalEvents: events, HasMore: offset+limit < max(calls, events), RevisionToken: token, State: state, OversizedRecords: s.OversizedRecords, StartByte: s.WindowStartByte, ScannedBytes: s.Fingerprint.IndexedBytes - s.WindowStartByte, FileBytes: s.Fingerprint.Size, RecordLimit: WindowRecordLimit, RecordBytesLimit: RecordBytesLimit}
	if s.ScanTruncated {
		cursor := obj("byteStart", s.Fingerprint.IndexedBytes, "ordinal", s.NextOrdinal, "skipLine", s.OversizedContinuation, "fingerprint", fingerprint)
		w.NextScanCursor = base64.RawURLEncoding.EncodeToString(raw(cursor))
	}
	return w, nil
}
func snapshotFromSource(s SourceRecord) (ScanSnapshot, bool) {
	fp := localindex.DeserializeFingerprint(val(s.Fingerprint, ""))
	if fp == nil || fp.ParserVersion != ParserVersion {
		return ScanSnapshot{}, false
	}
	return ScanSnapshot{Fingerprint: *fp, NextOrdinal: val(s.NextOrdinal, 0), WindowStartByte: s.WindowStartByte, OversizedRecords: s.OversizedRecords, ScanTruncated: s.ScanTruncated, OversizedContinuation: val(s.LastErrorCode, "") == "TRACE_OVERSIZED_RECORD_CONTINUATION"}, true
}
func projectionInput(id, path string, s *ScanSnapshot) ProjectionInput {
	fp := localindex.SerializeFingerprint(s.Fingerprint)
	return ProjectionInput{Source: SourceInput{SessionID: id, FilePath: path, Size: s.Fingerprint.Size, MtimeMs: s.Fingerprint.MtimeMs, IndexedBytes: s.Fingerprint.IndexedBytes, FileIdentity: s.Fingerprint.FileIdentity, Fingerprint: &fp, PendingTailBytes: ptr(s.Fingerprint.Size - s.Fingerprint.IndexedBytes), NextOrdinal: &s.NextOrdinal, OversizedRecords: s.OversizedRecords, ScanTruncated: s.ScanTruncated, WindowStartByte: s.WindowStartByte, OversizedContinuation: s.OversizedContinuation}, Calls: s.Calls, Events: s.Events}
}
func readRange(ctx context.Context, path string, start, length int64) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !safe(start) || !safe(length) || length > RecordBytesLimit {
		return nil, &Error{"TRACE_RECORD_TOO_LARGE", "Trace record exceeds 2 MiB; download the raw trace"}
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b := make([]byte, length)
	n, e := f.ReadAt(b, start)
	if int64(n) != length {
		return nil, errors.New("Trace changed while loading; retry the request")
	}
	if e != nil && e != io.EOF {
		return nil, e
	}
	return b, ctx.Err()
}
func sameLocator(a, b CallLocator) bool {
	return a.ID == b.ID && a.StartedAt == b.StartedAt && sameNullable(a.CompletedAt, b.CompletedAt) && a.Status == b.Status && a.Source == b.Source && sameNullable(a.Model, b.Model) && sameNullable(a.DurationMs, b.DurationMs) && a.Failed == b.Failed && a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens
}
func normalizeSessionID(s string) string {
	u := units(jstr(s))
	out := make([]byte, 0, len(u))
	for _, c := range u {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", rune(c)) {
			out = append(out, byte(c))
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func sameNullable[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
