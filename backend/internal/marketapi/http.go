package marketapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (s *Service) HandleHTTP(w http.ResponseWriter, r *http.Request, parts []string) {
	// The original HTTP abort races the response, not the already-created install.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		w.WriteHeader(499)
		return
	}
	s.operations.Add(1)
	s.mu.Unlock()
	defer s.operations.Done()
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	value, err := s.handle(ctx, r, parts)
	status := 200
	if err != nil {
		status = 500
		value = object{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
		var e *Error
		if errors.As(err, &e) {
			status = e.Status
			value = object{"error": e.Code, "message": e.Message}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func segment(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}
func (s *Service) handle(ctx context.Context, r *http.Request, parts []string) (object, error) {
	sub := segment(parts, 2)
	source, slug := segment(parts, 3), segment(parts, 4)
	if r.Method == "GET" && sub == "status" {
		return object{"sources": s.Status()}, nil
	}
	if r.Method == "GET" && sub == "skills" && source == "" {
		q := r.URL.Query()
		security, installed := first(q.Get("security"), "all"), first(q.Get("installed"), "all")
		if !strings.Contains("|all|verified|benign|unknown|flagged|", "|"+security+"|") {
			return nil, fail(400, "BAD_REQUEST", "Invalid security filter: "+security)
		}
		if !strings.Contains("|all|installed|installable|", "|"+installed+"|") {
			return nil, fail(400, "BAD_REQUEST", "Invalid installed filter: "+installed)
		}
		source = first(q.Get("source"), "all")
		if source != "all" && !validSource(source) {
			return nil, fail(400, "BAD_REQUEST", "Invalid source: "+source)
		}
		limit := parseInt(first(q.Get("limit"), "24"), 24)
		if limit < 1 {
			limit = 1
		}
		if limit > 100 {
			limit = 100
		}
		scope := first(q.Get("scope"), "catalog")
		if scope != "catalog" && scope != "market" {
			return nil, fail(400, "BAD_REQUEST", "Invalid scope: "+scope)
		}
		category := strings.TrimSpace(q.Get("category"))
		if category == "all" {
			category = ""
		}
		return s.List(ctx, ListParams{Q: strings.TrimSpace(q.Get("q")), Source: source, Security: security, Installed: installed, Cursor: q.Get("cursor"), Limit: limit, Scope: scope, Category: category})
	}
	if r.Method == "GET" && sub == "skills" && source != "" && slug != "" && (segment(parts, 5) == "" || segment(parts, 5) == "file") {
		if !validSource(source) {
			return nil, fail(400, "BAD_REQUEST", "Invalid market source: "+source)
		}
		decoded, e := url.PathUnescape(slug)
		if e != nil {
			return nil, e
		}
		slug = decoded
		if strings.ContainsAny(slug, "/\\") || strings.Contains(slug, "..") {
			return nil, fail(400, "BAD_REQUEST", "Invalid skill slug: "+slug)
		}
		owner, e := parseOwner(r.URL.Query().Get("owner"), source)
		if e != nil {
			return nil, e
		}
		if segment(parts, 5) == "file" {
			path := r.URL.Query().Get("path")
			if !validFile(path) {
				return nil, fail(400, "BAD_REQUEST", "Invalid file path: "+path)
			}
			file, err := s.File(ctx, source, slug, path, owner)
			return object{"file": file}, detailError(err)
		}
		skill, status, err := s.Detail(ctx, source, slug, owner)
		return object{"skill": skill, "sourceStatus": status}, detailError(err)
	}
	if r.Method == "POST" && (sub == "install" || sub == "uninstall") {
		var body any
		decoder := json.NewDecoder(r.Body)
		var trailing any
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(&trailing) != io.EOF {
			return nil, fail(400, "BAD_REQUEST", "Invalid JSON body")
		}
		switch body.(type) {
		case map[string]any, []any:
		default:
			return nil, fail(400, "BAD_REQUEST", "Invalid JSON body")
		}
		id := str(obj(body)["id"])
		source, slug, found := strings.Cut(id, ":")
		if !found || !validSource(source) || slug == "" {
			return nil, fail(400, "BAD_REQUEST", "Invalid skill id: "+first(id, "(missing)"))
		}
		if sub == "install" {
			owner, err := parseOwner(obj(body)["owner"], source)
			if err != nil {
				return nil, err
			}
			return s.Install(ctx, source, slug, owner)
		}
		return s.Uninstall(ctx, source, slug)
	}
	path := "/api/market"
	if sub != "" {
		path += "/" + sub
	}
	return nil, fail(405, "METHOD_NOT_ALLOWED", "Method "+r.Method+" not allowed on "+path)
}
func detailError(err error) error {
	var e *Error
	if errors.As(err, &e) && e.Code == "MARKET_UPSTREAM_BAD_RESPONSE" {
		return &Error{404, e.Code, e.Message}
	}
	return err
}
