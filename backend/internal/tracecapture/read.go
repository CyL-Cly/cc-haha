package tracecapture

import (
	"context"
	"errors"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func emptySession(id string) protocol.JSONObject {
	return obj("sessionId", id, "summary", emptySummary(), "calls", []protocol.JSONObject{}, "events", []protocol.JSONObject{})
}
func (s *Service) Overview(ctx context.Context, id string, o OverviewOptions) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, e
	}
	mode := s.mode()
	id = normalizeSessionID(id)
	p := s.filePath(id)
	scan, e := resolveScan(ctx, p, o.ScanCursor)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if _, e = os.Stat(p); os.IsNotExist(e) {
		return emptySession(id), nil
	} else if e != nil {
		return protocol.JSONObject{}, e
	}
	if mode == "on" {
		projection, e := s.ensure(ctx, id, p, scan)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		i := s.getIndex()
		if projection != nil && i != nil {
			offset := max(int64(0), o.Offset)
			limit := o.Limit
			if limit == 0 {
				limit = OverviewLimit
			}
			limit = max(1, min(100, limit))
			page, events, e := i.GetSession(ctx, id, &offset, &limit)
			if e != nil {
				s.quarantine(e)
			} else if page != nil {
				snapshot, ok := snapshotFromSource(page.SourceRecord)
				if ok && localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion).Kind == "unchanged" {
					w, e := windowMetadata(snapshot, page.Summary.APICalls, events, o)
					if e != nil {
						return protocol.JSONObject{}, e
					}
					calls := []protocol.JSONObject{}
					ev := []protocol.JSONObject{}
					for _, c := range page.Calls {
						calls = append(calls, shellCall(id, c))
					}
					for _, v := range page.Events {
						ev = append(ev, shellEvent(id, v))
					}
					return obj("sessionId", id, "window", w, "summary", page.Summary, "calls", calls, "events", ev), nil
				}
			}
		}
	}
	snapshot, e := s.scan(ctx, p, scan, nil)
	if os.IsNotExist(e) {
		return emptySession(id), nil
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if snapshot == nil {
		return protocol.JSONObject{}, errors.New("Trace changed while loading; retry the request")
	}
	w, e := windowMetadata(*snapshot, int64(len(snapshot.Calls)), int64(len(snapshot.Events)), o)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	calls := []protocol.JSONObject{}
	ev := []protocol.JSONObject{}
	for _, c := range snapshot.Calls {
		calls = append(calls, shellCall(id, c))
	}
	summary := summarize(calls)
	summary.Models = summary.Models[:min(64, len(summary.Models))]
	summary.FailedCalls = 0
	for _, c := range snapshot.Calls {
		if c.Failed {
			summary.FailedCalls++
		}
	}
	for _, v := range snapshot.Events {
		ev = append(ev, shellEvent(id, v))
	}
	a, b := min(w.Offset, int64(len(calls))), min(w.Offset+w.Limit, int64(len(calls)))
	ea, eb := min(w.Offset, int64(len(ev))), min(w.Offset+w.Limit, int64(len(ev)))
	return obj("sessionId", id, "window", w, "summary", summary, "calls", calls[a:b], "events", ev[ea:eb]), nil
}
func (s *Service) Call(ctx context.Context, id, callID, cursor string) (protocol.JSONObject, error) {
	mode := s.mode()
	id = normalizeSessionID(id)
	p := s.filePath(id)
	scan := ScanOptions{}
	if cursor != "" {
		var e error
		scan, e = resolveScan(ctx, p, cursor)
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	if mode != "off" {
		v, e := s.projectedCall(ctx, id, p, callID, scan, cursor != "")
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if mode == "on" && v.IsObject() {
			return v, nil
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, e := s.scan(ctx, p, scan, nil)
		if os.IsNotExist(e) {
			return protocol.JSONObject{}, nil
		}
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if snapshot == nil {
			continue
		}
		var locator *CallLocator
		for n := range snapshot.Calls {
			if snapshot.Calls[n].ID == callID {
				locator = &snapshot.Calls[n]
				break
			}
		}
		if locator == nil {
			if snapshot.OversizedRecords > 0 {
				return protocol.JSONObject{}, &Error{"TRACE_RECORD_TOO_LARGE", "Call is unavailable in the bounded trace window; oversized records are preserved in the raw download"}
			}
			return protocol.JSONObject{}, nil
		}
		record, e := readCallRange(ctx, p, *locator)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if record.IsObject() && sameLocator(callLocator(record, locator.Ordinal, locator.ByteStart, locator.ByteLength, locator.Ordinal), *locator) && localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion).Kind == "unchanged" {
			return record, nil
		}
	}
	return protocol.JSONObject{}, nil
}
func readCallRange(ctx context.Context, p string, c CallLocator) (protocol.JSONObject, error) {
	b, e := readRange(ctx, p, c.ByteStart, c.ByteLength)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	call, _ := parseEntry(b)
	return call, nil
}
func (s *Service) projectedCall(ctx context.Context, id, p, callID string, scan ScanOptions, hasScan bool) (protocol.JSONObject, error) {
	i := s.getIndex()
	if i == nil {
		return protocol.JSONObject{}, nil
	}
	source, c, e := i.GetCall(ctx, id, callID)
	if e != nil {
		s.quarantine(e)
		return protocol.JSONObject{}, nil
	}
	if source == nil || c == nil || source.State != "ready" || hasScan && source.WindowStartByte != scan.ByteStart {
		projection, e := s.ensure(ctx, id, p, scan)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		if projection == nil {
			return protocol.JSONObject{}, nil
		}
	}
	source, c, e = i.GetCall(ctx, id, callID)
	if e != nil {
		s.quarantine(e)
		return protocol.JSONObject{}, nil
	}
	if source == nil || c == nil {
		return protocol.JSONObject{}, nil
	}
	snapshot, ok := snapshotFromSource(*source)
	if !ok || source.FilePath != p || c.ByteStart < 0 || c.ByteLength < 1 || !safe(c.ByteStart+c.ByteLength) || c.ByteStart+c.ByteLength > source.IndexedBytes {
		return protocol.JSONObject{}, nil
	}
	if localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion).Kind != "unchanged" {
		return protocol.JSONObject{}, nil
	}
	record, e := readCallRange(ctx, p, *c)
	if e != nil {
		if ctx.Err() != nil {
			return protocol.JSONObject{}, ctx.Err()
		}
		var re *Error
		if errors.As(e, &re) {
			return protocol.JSONObject{}, e
		}
		return protocol.JSONObject{}, nil
	}
	if !record.IsObject() || !sameLocator(callLocator(record, c.Ordinal, c.ByteStart, c.ByteLength, c.Ordinal), *c) || localindex.DetectSourceChange(ctx, p, snapshot.Fingerprint, ParserVersion).Kind != "unchanged" {
		return protocol.JSONObject{}, nil
	}
	currentSource, currentCall, e := i.GetCall(ctx, id, callID)
	if e != nil {
		s.quarantine(e)
		return protocol.JSONObject{}, nil
	}
	if currentSource == nil || currentCall == nil || currentSource.Revision != source.Revision || currentSource.ResetToken != source.ResetToken || val(currentSource.Fingerprint, "") != val(source.Fingerprint, "") || currentCall.Ordinal != c.Ordinal || currentCall.ByteStart != c.ByteStart || currentCall.ByteLength != c.ByteLength {
		return protocol.JSONObject{}, nil
	}
	return record, nil
}
func (s *Service) Full(ctx context.Context, id string) (protocol.JSONObject, error) {
	s.mode()
	p := s.filePath(id)
	f, e := os.Open(p)
	if os.IsNotExist(e) {
		return emptySession(id), nil
	}
	if e != nil {
		return protocol.JSONObject{}, e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if stat.Size() > LegacyFullBytesLimit {
		return protocol.JSONObject{}, &Error{"TRACE_RECORD_TOO_LARGE", "Full trace hydration is limited to 8 MiB; use overview pages or raw download"}
	}
	b := make([]byte, stat.Size())
	_, e = f.ReadAt(b, 0)
	if e != nil && len(b) > 0 {
		return protocol.JSONObject{}, e
	}
	calls := []protocol.JSONObject{}
	indexes := map[string]int{}
	events := []protocol.JSONObject{}
	ordinal := 0
	start := 0
	for start < len(b) {
		end := start
		for end < len(b) && b[end] != '\n' {
			end++
		}
		if ordinal >= WindowRecordLimit {
			return protocol.JSONObject{}, &Error{"TRACE_RECORD_TOO_LARGE", "Full trace hydration exceeds the record budget; use overview pages or raw download"}
		}
		if end-start > RecordBytesLimit {
			return protocol.JSONObject{}, &Error{"TRACE_RECORD_TOO_LARGE", "Trace record exceeds 2 MiB; use raw download"}
		}
		line := b[start:end]
		call, event := parseEntry(line)
		if call.IsObject() {
			if e := validateCallShape(call); e != nil {
				return protocol.JSONObject{}, e
			}
			key := text(call.Get("id"))
			if n, ok := indexes[key]; ok {
				calls[n] = call
			} else {
				indexes[key] = len(calls)
				calls = append(calls, call)
			}
		}
		if event.IsObject() {
			events = append(events, event)
		}
		start = end + 1
		ordinal++
	}
	sortObjects := func(values []protocol.JSONObject, key string) {
		for n := 1; n < len(values); n++ {
			for j := n; j > 0 && s.o.Compare(text(values[j-1].Get(key)), text(values[j].Get(key))) > 0; j-- {
				values[j], values[j-1] = values[j-1], values[j]
			}
		}
	}
	sortObjects(calls, "startedAt")
	sortObjects(events, "timestamp")
	fp, e := localindex.CaptureFingerprint(ctx, p, stat.Size(), ParserVersion)
	if e == nil && fp != nil && localindex.FingerprintMatchesFile(f, *fp) {
		_, _ = s.ensure(ctx, normalizeSessionID(id), p, ScanOptions{})
	}
	return obj("sessionId", id, "summary", summarize(calls), "calls", calls, "events", events), nil
}
func TrimCallPreviews(c protocol.JSONObject, maxPreview int) protocol.JSONObject {
	out := c.Clone()
	request := object(c.Get("request")).Clone()
	request.Delete("semantic")
	body := object(request.Get("body")).Clone()
	if length(body.Get("preview")) > maxPreview {
		body.Set("preview", slice(body.Get("preview"), maxPreview))
		body.Set("truncated", raw(true))
	}
	request.Set("body", raw(body))
	out.Set("request", raw(request))
	if response := object(c.Get("response")); response.IsObject() {
		response = response.Clone()
		body = object(response.Get("body")).Clone()
		if length(body.Get("preview")) > maxPreview {
			body.Set("preview", slice(body.Get("preview"), maxPreview))
			body.Set("truncated", raw(true))
		}
		response.Set("body", raw(body))
		out.Set("response", raw(response))
	}
	return out
}
