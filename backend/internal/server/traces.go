package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

func traceAPIError(e error) error {
	var api *apiError
	if errors.As(e, &api) {
		return e
	}
	return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
}
func traceDecode(s string) (string, error) {
	v, e := url.PathUnescape(s)
	if e != nil || !utf8.ValidString(v) {
		return "", badRequest("Invalid trace session id")
	}
	return trimJS(v), nil
}
func traceNumber(s string) (float64, bool) {
	s = trimJS(s)
	if s == "" {
		return 0, true
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") || strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B") || strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
		base := 16
		switch s[1] {
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		n, e := strconv.ParseUint(s[2:], base, 64)
		return float64(n), e == nil
	}
	n, e := strconv.ParseFloat(s, 64)
	return n, e == nil
}
func traceParseInt(v string) (float64, bool) {
	prefix := parseIntPrefix.FindString(trimJS(v))
	if prefix == "" {
		return 0, false
	}
	n, e := strconv.ParseFloat(prefix, 64)
	return n, e == nil || math.IsInf(n, 0)
}
func tracePageLimit(v string) int64 {
	n, ok := traceParseInt(v)
	if !ok || math.IsInf(n, 0) || math.IsNaN(n) || n <= 0 {
		return 50
	}
	return int64(min(n, 200))
}
func tracePageOffset(v string) int64 {
	n, ok := traceParseInt(v)
	if !ok || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 {
		return 0
	}
	return int64(min(n, 9007199254740991))
}

func (s *Server) tracesHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *tracecapture.Service) (err error) {
	defer func() {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				w.WriteHeader(499)
				err = nil
			} else {
				err = traceAPIError(err)
			}
		}
	}()
	if service == nil {
		return unsupported("traces")
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	if len(parts) == 2 {
		if r.Method != "GET" {
			return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed on /api/traces"}
		}
		limit, offset := tracePageLimit(r.URL.Query().Get("limit")), tracePageOffset(r.URL.Query().Get("offset"))
		query := trimJS(r.URL.Query().Get("q"))
		if query != "" {
			return s.searchedTracesHTTP(w, r, service, query, limit, offset)
		}
		list, e := service.List(r.Context(), tracecapture.ListOptions{Limit: limit, Offset: offset})
		if e != nil {
			return e
		}
		items := traceObjects(list.Get("traces"))
		for n := range items {
			meta, e := s.traceSessionMeta(r.Context(), traceString(items[n].Get("sessionId")), true)
			if e != nil {
				return e
			}
			items[n].Set("session", traceRaw(meta))
		}
		list.Set("traces", traceRaw(items))
		writeJSON(w, 200, list)
		return nil
	}
	if sub == "settings" {
		switch r.Method {
		case "GET":
			writeJSON(w, 200, service.Settings(r.Context()))
			return nil
		case "PUT":
			body, e := jsonObject(r)
			if e != nil {
				return e
			}
			var enabled *bool
			if value := body["enabled"]; string(value) == "true" || string(value) == "false" {
				v := string(value) == "true"
				enabled = &v
			}
			settings, e := service.UpdateSettings(r.Context(), enabled)
			if e != nil {
				return e
			}
			writeJSON(w, 200, settings)
			return nil
		default:
			return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed on /api/traces/settings"}
		}
	}
	if len(parts) == 4 && parts[3] == "revision" {
		if r.Method != "GET" {
			return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed on /api/traces/:sessionId/revision"}
		}
		id, e := traceDecode(sub)
		if e != nil {
			return e
		}
		if id == "" {
			return badRequest("Trace session id is required")
		}
		var since *int64
		query := r.URL.Query()
		if values, exists := query["sinceRevision"]; exists {
			n, ok := traceNumber(values[0])
			if !ok || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
				return badRequest("Invalid trace revision")
			}
			v := int64(n)
			since = &v
		}
		var token *string
		if values, exists := query["sinceRevisionToken"]; exists {
			v := values[0]
			length := len(utf16.Encode([]rune(v)))
			if length < 1 || length > 512 {
				return badRequest("Invalid trace revision token")
			}
			token = &v
		}
		result, e := service.Revision(r.Context(), id, since, token)
		if e != nil {
			return e
		}
		writeJSON(w, 200, result)
		return nil
	}
	if r.Method == "DELETE" && len(parts) == 3 {
		id, e := traceDecode(sub)
		if e != nil {
			return e
		}
		if id == "" {
			return badRequest("Trace session id is required")
		}
		result, e := service.Delete(r.Context(), id)
		if e != nil {
			return e
		}
		writeJSON(w, 200, result)
		return nil
	}
	return notFound("Unknown traces endpoint: " + sub)
}
func traceRaw(v any) json.RawMessage       { b, _ := json.Marshal(v); return b }
func traceString(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
func traceObjects(v json.RawMessage) []protocol.JSONObject {
	var out []protocol.JSONObject
	_ = json.Unmarshal(v, &out)
	return out
}
func (s *Server) traceSessionMeta(ctx context.Context, id string, indexed bool) (any, error) {
	if s.projection == nil {
		return nil, nil
	}
	info, e := s.findSession(ctx, id)
	if e != nil {
		var api *apiError
		if errors.As(e, &api) && api.status == 404 {
			return nil, nil
		}
		return nil, e
	}
	if info.memory {
		return nil, nil
	}
	if !indexed {
		info, e = s.scanSession(ctx, info.Path)
		if e != nil {
			return nil, e
		}
	}
	var workDir any = info.WorkDir
	if info.workDirNull {
		workDir = nil
	}
	return map[string]any{"id": id, "title": info.Title, "projectPath": info.ProjectPath, "workDir": workDir}, nil
}
func (s *Server) searchedTracesHTTP(w http.ResponseWriter, r *http.Request, service *tracecapture.Service, q string, limit, offset int64) error {
	files, e := service.ListFiles(r.Context())
	if e != nil {
		return e
	}
	items := traceObjects(files.Get("files"))
	type candidate struct {
		id   string
		meta any
	}
	filtered := []candidate{}
	terms := strings.FieldsFunc(localindex.NormalizeContent(q), func(r rune) bool { return trimJS(string(r)) == "" })
	for _, item := range items {
		id := traceString(item.Get("sessionId"))
		meta, e := s.traceSessionMeta(r.Context(), id, true)
		if e != nil {
			return e
		}
		haystack := id
		if fields, ok := meta.(map[string]any); ok {
			for _, k := range []string{"title", "projectPath", "workDir"} {
				if v, ok := fields[k].(string); ok && v != "" {
					haystack += "\n" + v
				}
			}
		}
		haystack = localindex.NormalizeContent(haystack)
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, candidate{id, meta})
		}
	}
	a, b := min(offset, int64(len(filtered))), min(min(offset, int64(len(filtered)))+limit, int64(len(filtered)))
	page := filtered[a:b]
	traces := []protocol.JSONObject{}
	if len(page) > 0 {
		ids := []string{}
		for _, item := range page {
			ids = append(ids, item.id)
		}
		list, e := service.List(r.Context(), tracecapture.ListOptions{SessionIDs: ids, Limit: int64(len(ids))})
		if e != nil {
			return e
		}
		byID := map[string]protocol.JSONObject{}
		for _, item := range traceObjects(list.Get("traces")) {
			byID[traceString(item.Get("sessionId"))] = item
		}
		for _, candidate := range page {
			if item, ok := byID[candidate.id]; ok {
				item.Set("session", traceRaw(candidate.meta))
				traces = append(traces, item)
			}
		}
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "traces", Value: traceRaw(traces)}, protocol.JSONField{Name: "total", Value: traceRaw(len(filtered))}, protocol.JSONField{Name: "storageDir", Value: files.Get("storageDir")}, protocol.JSONField{Name: "settings", Value: files.Get("settings")})
	writeJSON(w, 200, response)
	return nil
}
func (s *Server) sessionTraceHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *tracecapture.Service) (err error) {
	defer func() {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				w.WriteHeader(499)
				err = nil
			} else {
				var resource *tracecapture.Error
				if errors.As(err, &resource) && resource.Code == "TRACE_PAGE_STALE" {
					err = &apiError{409, resource.Code, resource.Message}
				} else if errors.As(err, &resource) && resource.Code == "TRACE_RECORD_TOO_LARGE" {
					err = &apiError{413, resource.Code, resource.Message}
				} else if errors.As(err, &resource) && resource.Code == "TRACE_INDEX_BUSY" {
					err = &apiError{503, resource.Code, resource.Message}
				} else {
					err = traceAPIError(err)
				}
			}
		}
	}()
	if r.Method != "GET" {
		return methodNotAllowed(r.Method)
	}
	if service == nil {
		return unsupported("sessions/trace")
	}
	id := parts[2]
	var e error
	sub := ""
	if len(parts) > 4 {
		sub = parts[4]
	}
	if sub == "raw" {
		path, exists, e := service.File(r.Context(), id)
		if e != nil {
			return e
		}
		if !exists {
			return notFound("Trace not found: " + id)
		}
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		defer file.Close()
		stat, e := file.Stat()
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="trace.jsonl"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		_, e = io.Copy(w, file)
		return e
	}
	if sub == "calls" {
		callID := ""
		if len(parts) > 5 {
			callID = parts[5]
		}
		if trimJS(callID) == "" {
			return badRequest("callId is required")
		}
		call, e := service.Call(r.Context(), id, callID, "")
		if e != nil {
			return e
		}
		if !call.IsObject() {
			return notFound("Trace call not found: " + callID)
		}
		writeJSON(w, 200, map[string]any{"call": call})
		return nil
	}
	q := r.URL.Query()
	offsetText := q.Get("offset")
	if offsetText == "" {
		if _, exists := q["offset"]; exists {
			return badRequest("Invalid trace offset")
		}
		offsetText = "0"
	}
	for _, c := range offsetText {
		if c < '0' || c > '9' {
			return badRequest("Invalid trace offset")
		}
	}
	offset, e := strconv.ParseInt(offsetText, 10, 64)
	if e != nil || offset > 9007199254740991 {
		return badRequest("Invalid trace offset")
	}
	trace, e := service.Overview(requestSignalContext(r), id, tracecapture.OverviewOptions{Offset: offset, RevisionToken: q.Get("revisionToken"), ScanCursor: q.Get("scanCursor")})
	if e != nil {
		return e
	}
	calls := traceObjects(trace.Get("calls"))
	for n := range calls {
		calls[n] = tracecapture.TrimCallPreviews(calls[n], tracecapture.ListPreviewChars)
	}
	trace.Set("calls", traceRaw(calls))
	meta, e := s.traceSessionMeta(r.Context(), id, false)
	if e != nil {
		return e
	}
	signature, e := s.traceMessagesSignature(r.Context(), id)
	if e != nil {
		return e
	}
	trace.Set("messageSignature", traceRaw(signature))
	trace.Set("session", traceRaw(meta))
	writeJSON(w, 200, trace)
	return nil
}
func (s *Server) traceMessagesSignature(ctx context.Context, id string) (*string, error) {
	if s.projection == nil {
		return nil, nil
	}
	info, e := s.findSession(ctx, id)
	if e != nil {
		var api *apiError
		if errors.As(e, &api) && api.status == 404 {
			return nil, nil
		}
		return nil, e
	}
	if info.memory {
		return nil, nil
	}
	hash := sha256.New()
	add := func(path string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		file, e := (sessionstore.OSFileSystem{}).Open(path)
		if os.IsNotExist(e) {
			b, _ := protocol.ParseAndStringifyJavaScriptJSON(traceRaw([]string{path, "missing"}))
			_, _ = hash.Write(b)
			return nil
		}
		if e != nil {
			return e
		}
		defer file.Close()
		stat, e := file.Stat()
		if e != nil {
			return e
		}
		ctime, e := traceCTime(path)
		if e != nil {
			return e
		}
		b, _ := protocol.ParseAndStringifyJavaScriptJSON(traceRaw([]string{path, stat.Dev, stat.Ino, strconv.FormatInt(stat.Size, 10), stat.MtimeNS, ctime}))
		_, _ = hash.Write(b)
		return nil
	}
	if e = add(info.Path); e != nil {
		return nil, e
	}
	directory := filepath.Join(filepath.Dir(info.Path), id, "subagents")
	entries, e := os.ReadDir(directory)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "agent-") && strings.HasSuffix(entry.Name(), ".jsonl") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if e = add(filepath.Join(directory, name)); e != nil {
			return nil, e
		}
	}
	result := hex.EncodeToString(hash.Sum(nil))
	return &result, nil
}
