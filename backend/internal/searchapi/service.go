// Package searchapi preserves the desktop's workspace and transcript search
// behavior. SQLite supplies disposable locators; canonical files verify text.
package searchapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Message }
func bad(message string) error    { return &APIError{400, "BAD_REQUEST", message} }

type Command struct {
	Path  string
	Args  []string
	Argv0 string
}
type Metadata struct {
	Title, ProjectPath, ModifiedAt string
	WorkDir                        *string
	SourceSnapshot                 *localindex.FileSnapshot
}
type FilterCandidate struct {
	Path     string
	Metadata Metadata
}
type Content interface {
	Query(context.Context, string, localindex.ContentQueryOptions) (*localindex.ContentQueryResult, error)
	Suggest(context.Context, string, *float64) (*localindex.ContentSuggestions, error)
}
type EntryLine struct {
	Entry      protocol.JSONObject `json:"entry"`
	LineNumber int64               `json:"lineNumber"`
}
type TargetedEntries struct {
	Entries    []EntryLine `json:"entries"`
	BytesRead  int64       `json:"bytesRead"`
	RangesRead int         `json:"rangesRead"`
}
type Options struct {
	ConfigDir, WorkDir      string
	Environment             map[string]string
	Content                 Content
	Metadata                func(context.Context, []string) (map[string]Metadata, error)
	ReadEntriesAtLines      func(context.Context, string, map[int64]bool) (*TargetedEntries, error)
	GetCandidatesForFilters func(context.Context, localindex.ContentQueryOptions) ([]FilterCandidate, error)
	ParseDate               func(string) (float64, bool, error)
	Ripgrep                 func() Command
	Now                     func() time.Time
}
type Service struct{ o Options }

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.ConfigDir) || !filepath.IsAbs(o.WorkDir) || o.ParseDate == nil || o.Ripgrep == nil || o.Content == nil || o.Metadata == nil || o.ReadEntriesAtLines == nil {
		return nil, errors.New("Search requires absolute config/workspace paths and actual content, metadata, locator, date and ripgrep producers")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Environment = cloneEnv(o.Environment)
	return &Service{o}, nil
}
func cloneEnv(e map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range e {
		out[k] = v
	}
	return out
}
func jsSpace(r rune) bool {
	return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func trim(s string) string            { return strings.TrimFunc(s, jsSpace) }
func raw(v any) json.RawMessage       { r, _ := json.Marshal(v); return r }
func str(v json.RawMessage) string    { var s string; _ = json.Unmarshal(v, &s); return s }
func isString(v json.RawMessage) bool { return len(v) > 0 && v[0] == '"' }
func truthy(v json.RawMessage) bool {
	if len(v) == 0 || string(v) == "null" || string(v) == "false" {
		return false
	}
	if isString(v) {
		return str(v) != ""
	}
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return n != 0
	}
	return true
}
func number(v json.RawMessage, fallback float64) float64 {
	if len(v) == 0 || string(v) == "null" {
		return fallback
	}
	switch string(v) {
	case "true":
		return 1
	case "false":
		return 0
	}
	if isString(v) {
		s := trim(str(v))
		if s == "" {
			return 0
		}
		n, e := strconv.ParseFloat(s, 64)
		if e == nil {
			return n
		}
		return math.NaN()
	}
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return n
	}
	if len(v) > 0 && v[0] == '[' {
		converted, e := protocol.JavaScriptStringJSON(v)
		if e == nil {
			return number(converted, math.NaN())
		}
	}
	return math.NaN()
}
func actualNumber(v json.RawMessage) *float64 {
	if len(v) == 0 || isString(v) || string(v) == "true" || string(v) == "false" || string(v) == "null" {
		return nil
	}
	var n float64
	if json.Unmarshal(v, &n) != nil {
		return nil
	}
	return &n
}
func ptr(s string) *string { return &s }
func sliceEnd(length int, end float64) int {
	if math.IsNaN(end) {
		return 0
	}
	if math.IsInf(end, 1) {
		return length
	}
	if math.IsInf(end, -1) {
		return 0
	}
	n := math.Trunc(end)
	if n < 0 {
		n = max(0, float64(length)+n)
	}
	return int(min(float64(length), max(0, n)))
}
func iso(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		ms = 0
	}
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

type WorkspaceResult struct {
	File    string   `json:"file"`
	Line    int64    `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context,omitempty"`
}
type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type SessionMatch struct {
	Role       string          `json:"role"`
	MessageID  *string         `json:"messageId"`
	LineNumber int64           `json:"lineNumber"`
	Snippet    json.RawMessage `json:"snippet"`
	Highlights []Highlight     `json:"highlights"`
	Timestamp  *string         `json:"timestamp,omitempty"`
}
type SessionResult struct {
	SessionID   string         `json:"sessionId"`
	Title       string         `json:"title"`
	ProjectPath string         `json:"projectPath"`
	WorkDir     *string        `json:"workDir"`
	ModifiedAt  string         `json:"modifiedAt"`
	MatchCount  int64          `json:"matchCount"`
	Matches     []SessionMatch `json:"matches"`
}
type SessionOutput struct {
	Results   []SessionResult `json:"results"`
	Truncated bool            `json:"truncated"`
}

func (s *Service) Handle(ctx context.Context, method, sub string, body json.RawMessage) (int, any, error) {
	if method != "POST" {
		return 0, nil, &APIError{405, "METHOD_NOT_ALLOWED", "Method " + method + " not allowed. Use POST."}
	}
	var obj protocol.JSONObject
	if !json.Valid(body) {
		return 0, nil, bad("Invalid JSON body")
	}
	if string(body) == "null" {
		return 0, nil, errors.New("null is not an object (evaluating 'body.query')")
	}
	_ = json.Unmarshal(body, &obj)
	query := obj.Get("query")
	if !truthy(query) || !isString(query) {
		return 0, nil, bad("Missing or invalid \"query\" in request body")
	}
	if sub == "sessions" {
		output, e := s.SearchSessions(ctx, str(query), obj)
		if e != nil {
			return 0, nil, e
		}
		return 200, map[string]any{"results": output.Results, "total": len(output.Results), "truncated": output.Truncated}, nil
	}
	if sub == "" {
		results, e := s.SearchWorkspace(ctx, str(query), obj)
		if e != nil {
			return 0, nil, e
		}
		return 200, map[string]any{"results": results, "total": len(results)}, nil
	}
	return 0, nil, &APIError{404, "NOT_FOUND", "Unknown search endpoint: " + sub}
}

type SessionSuggestion struct {
	SessionID           string `json:"sessionId"`
	OwnerTranscriptPath string `json:"ownerTranscriptPath"`
	ProjectPath         string `json:"projectPath"`
	ModifiedAt          string `json:"modifiedAt"`
}
type SuggestionOutput struct {
	Sessions         []SessionSuggestion `json:"sessions"`
	Truncated        bool                `json:"truncated"`
	IndexUnavailable bool                `json:"indexUnavailable"`
}

func (s *Service) Suggest(ctx context.Context, query string, limit *float64, deadlineMs *float64) (SuggestionOutput, error) {
	out := SuggestionOutput{Sessions: []SessionSuggestion{}}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if trim(query) == "" {
		return out, nil
	}
	if deadlineMs != nil && float64(s.o.Now().UnixMilli()) > *deadlineMs {
		out.Truncated = true
		return out, nil
	}
	projected, e := s.o.Content.Suggest(ctx, query, limit)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if e != nil {
		return out, e
	}
	if projected == nil {
		out.Truncated = true
		out.IndexUnavailable = true
		return out, nil
	}
	out.Truncated = projected.Truncated
	for _, item := range projected.Sessions {
		out.Sessions = append(out.Sessions, SessionSuggestion{item.OwnerSessionID, item.OwnerTranscriptPath, item.ProjectPath, iso(item.ModifiedAtMs)})
	}
	return out, nil
}
func collapseWhitespace(s string) string {
	var out strings.Builder
	space := false
	for _, r := range s {
		if jsSpace(r) {
			if !space {
				out.WriteByte(' ')
			}
			space = true
		} else {
			out.WriteRune(r)
			space = false
		}
	}
	return trim(out.String())
}
func indexUnits(text, needle []uint16) int {
	if len(needle) == 0 {
		return 0
	}
	for n := 0; n+len(needle) <= len(text); n++ {
		found := true
		for j := range needle {
			if text[n+j] != needle[j] {
				found = false
				break
			}
		}
		if found {
			return n
		}
	}
	return -1
}
func buildSnippet(text, query string, sensitive bool) (json.RawMessage, []Highlight) {
	normalized := collapseWhitespace(text)
	haystack, needle := normalized, query
	if !sensitive {
		haystack = localindex.NormalizeContent(haystack)
		needle = localindex.NormalizeContent(needle)
	}
	units := utf16.Encode([]rune(normalized))
	wanted := utf16.Encode([]rune(needle))
	at := indexUnits(utf16.Encode([]rune(haystack)), wanted)
	if at < 0 {
		head := units[:min(len(units), 240)]
		if len(head) < len(units) {
			head = append(append([]uint16(nil), head...), 0x2026)
		}
		return protocol.QuoteJavaScriptUTF16(head), []Highlight{}
	}
	start := max(0, at-120)
	end := min(len(units), at+len(wanted)+120)
	out := []uint16{}
	prefix := 0
	if start > 0 {
		out = append(out, 0x2026)
		prefix = 1
	}
	if start < len(units) && end >= start {
		out = append(out, units[start:end]...)
	}
	if end < len(units) {
		out = append(out, 0x2026)
	}
	return protocol.QuoteJavaScriptUTF16(out), []Highlight{{prefix + at - start, prefix + at - start + len(wanted)}}
}
