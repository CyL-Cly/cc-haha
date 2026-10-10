package scheduler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

type PageOptions struct {
	TaskID                       string
	Limit                        *float64
	Cursor                       string
	SummaryOnly, NonterminalOnly bool
	CompletedAfterMs             *float64
}
type Page struct {
	Runs          []protocol.JSONObject `json:"runs"`
	NextCursor    string                `json:"nextCursor,omitempty"`
	Revision      *int64                `json:"revision,omitempty"`
	RevisionToken string                `json:"revisionToken"`
	Reset         bool                  `json:"reset,omitempty"`
}
type position struct {
	time    float64
	ordinal int
	id      string
}
type pageItem struct {
	row protocol.JSONObject
	position
}

func stableToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func scopedRevision(revision string, o PageOptions) string {
	var id any
	if o.TaskID != "" {
		id = o.TaskID
	}
	scope := protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: raw(id)}, protocol.JSONField{Name: "nonterminalOnly", Value: raw(o.NonterminalOnly)}, protocol.JSONField{Name: "completedAfterMs", Value: raw(o.CompletedAfterMs)})
	b, _ := scope.MarshalJSON()
	return revision + ":scope:" + stableToken(string(protocol.StringifyValidatedJavaScriptJSON(b)))
}
func decodeCursor(value, revision string) (*position, bool) {
	if value == "" {
		return nil, false
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	var fields []json.RawMessage
	if e != nil || json.Unmarshal(b, &fields) != nil || len(fields) != 5 || string(fields[0]) != "1" {
		return nil, true
	}
	var actual, id string
	var millis, ordinal float64
	if json.Unmarshal(fields[1], &actual) != nil || actual != revision || json.Unmarshal(fields[2], &millis) != nil || string(fields[2]) == "null" || json.Unmarshal(fields[3], &ordinal) != nil || string(fields[3]) == "null" || json.Unmarshal(fields[4], &id) != nil || string(fields[4]) == "null" || math.IsNaN(millis) || math.IsInf(millis, 0) || ordinal < 0 || ordinal > 9007199254740991 || math.Trunc(ordinal) != ordinal {
		return nil, true
	}
	return &position{millis, int(ordinal), id}, false
}
func encodeCursor(p position, revision string) string {
	return base64.RawURLEncoding.EncodeToString(protocol.StringifyValidatedJavaScriptJSON(raw([]any{1, revision, p.time, p.ordinal, p.id})))
}
func summary(row protocol.JSONObject) protocol.JSONObject {
	out := protocol.NewJSONObject()
	for _, key := range []string{"id", "taskId", "startedAt", "completedAt", "status"} {
		if v := row.Get(key); v != nil {
			out.Set(key, v)
		}
	}
	for _, key := range []string{"output", "error"} {
		v := bytes.TrimSpace(row.Get(key))
		out.Set("has"+strings.ToUpper(key[:1])+key[1:], raw(len(v) > 0 && v[0] == '"'))
	}
	for _, key := range []string{"exitCode", "durationMs", "sessionId"} {
		if v := row.Get(key); v != nil {
			out.Set(key, v)
		}
	}
	return out
}
func dateMs(value json.RawMessage) (float64, bool, error) {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return 0, false, nil
	}
	ms, valid, e := sessionprojection.ParseDateUTC(units)
	if e != nil {
		// Invalid prose is an Invalid Date in Source. Numeric/local/legacy date
		// forms require the wider Date.parse producer, rather than guessing UTC.
		if strings.IndexAny(string(utf16.Decode(units)), "0123456789") < 0 {
			return 0, false, nil
		}
		return 0, false, &CapabilityError{"legacy or local-timezone scheduled run Date.parse"}
	}
	return ms, valid, nil
}
func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "timeout"
}
func pageLimit(o PageOptions) float64 {
	if o.Limit == nil {
		return 100
	}
	return math.Max(math.Trunc(*o.Limit), 0)
}
func Paginate(runs []protocol.JSONObject, o PageOptions, revision string) (Page, error) {
	expected := scopedRevision(revision, o)
	cursor, reset := decodeCursor(o.Cursor, expected)
	items := []pageItem{}
	for ordinal, row := range runs {
		if o.TaskID != "" && row.String("taskId") != o.TaskID || o.NonterminalOnly && terminal(row.String("status")) {
			continue
		}
		ms, valid, e := dateMs(row.Get("startedAt"))
		if e != nil {
			return Page{}, e
		}
		if !valid {
			ms = 0
		}
		if o.CompletedAfterMs != nil {
			v := row.Get("completedAt")
			if v == nil || string(v) == "null" {
				v = row.Get("startedAt")
			}
			completed, valid, e := dateMs(v)
			if e != nil {
				return Page{}, e
			}
			if !terminal(row.String("status")) || !valid || completed < *o.CompletedAfterMs {
				continue
			}
		}
		if cursor != nil && !(ms < cursor.time || ms == cursor.time && ordinal > cursor.ordinal || ms == cursor.time && ordinal == cursor.ordinal && row.String("id") > cursor.id) {
			continue
		}
		items = append(items, pageItem{row.Clone(), position{ms, ordinal, row.String("id")}})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].time != items[j].time {
			return items[i].time > items[j].time
		}
		return items[i].ordinal < items[j].ordinal
	})
	limit := pageLimit(o)
	count := len(items)
	if math.IsNaN(limit) {
		count = 0
	} else if limit < float64(count) {
		count = int(limit)
	}
	out := Page{Runs: []protocol.JSONObject{}, RevisionToken: revision, Reset: reset}
	for _, item := range items[:count] {
		row := item.row
		if o.SummaryOnly {
			row = summary(row)
		}
		out.Runs = append(out.Runs, row)
	}
	if float64(len(items)) > limit && count > 0 {
		out.NextCursor = encodeCursor(items[count-1].position, expected)
	}
	return out, nil
}
func (s *Service) GetRunsPage(ctx context.Context, o PageOptions) (Page, error) {
	return s.getRunsPage(ctx, o, "getRunsPage")
}
func (s *Service) getRunsPage(ctx context.Context, o PageOptions, operation string) (Page, error) {
	if e := ctx.Err(); e != nil {
		return Page{}, context.Cause(ctx)
	}
	if page := s.projectedPage(ctx, o); page != nil {
		return *page, nil
	}
	_, rows, data, e := s.store.read(s.store.runsPath(), "runs")
	if e != nil {
		return Page{}, e
	}
	page, e := Paginate(rows, o, fileToken(data))
	if e == nil {
		s.shadowPage(ctx, rows, o, operation)
	}
	return page, e
}
func (s *Service) legacyFullRuns(ctx context.Context, id string, limit *float64) ([]protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return nil, context.Cause(ctx)
	}
	_, runs, _, e := s.store.read(s.store.runsPath(), "runs")
	if e != nil {
		return nil, e
	}
	rows := []protocol.JSONObject{}
	dates := map[int]float64{}
	for _, row := range runs {
		if id != "" && row.String("taskId") != id {
			continue
		}
		ms, valid, e := dateMs(row.Get("startedAt"))
		if e != nil {
			return nil, e
		}
		if !valid {
			ms = math.NaN()
		}
		dates[len(rows)] = ms
		rows = append(rows, row)
	}
	// Original default-index full fallback uses stable Date comparison, while the
	// explicit off/shadow paging path assigns Invalid Dates the numeric key zero.
	items := make([]pageItem, len(rows))
	for n, row := range rows {
		items[n] = pageItem{row, position{time: dates[n]}}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].time > items[j].time })
	for n, item := range items {
		rows[n] = item.row
	}
	if limit != nil {
		n := math.Trunc(*limit)
		if math.IsNaN(n) {
			n = 0
		}
		if n < 0 {
			n = math.Max(float64(len(rows))+n, 0)
		}
		if n < float64(len(rows)) {
			rows = rows[:int(n)]
		}
	}
	return rows, nil
}
func (s *Service) GetRecentRuns(ctx context.Context, limit float64) ([]protocol.JSONObject, error) {
	if s.projection.mode() == "on" {
		return s.legacyFullRuns(ctx, "", &limit)
	}
	page, e := s.getRunsPage(ctx, PageOptions{Limit: &limit}, "getRecentRuns")
	return page.Runs, e
}
func (s *Service) GetTaskRuns(ctx context.Context, id string) ([]protocol.JSONObject, error) {
	if s.projection.mode() == "on" {
		return s.legacyFullRuns(ctx, id, nil)
	}
	limit := float64(2147483647)
	page, e := s.getRunsPage(ctx, PageOptions{TaskID: id, Limit: &limit}, "getTaskRuns")
	return page.Runs, e
}
func (s *Service) GetRunDetail(ctx context.Context, id string) (*protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return nil, context.Cause(ctx)
	}
	s.deactivateIfOff()
	_, rows, _, e := s.store.read(s.store.runsPath(), "runs")
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		if row.String("id") == id {
			copy := row.Clone()
			return &copy, nil
		}
	}
	return nil, nil
}
