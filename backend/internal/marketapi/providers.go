package marketapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/markdownfrontmatter"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) endpoint(source, path string, query url.Values) string {
	base := "https://clawhub.ai"
	key := "HAHA_MARKET_BASE_CLAWHUB"
	if source == "skillhub" {
		base = "https://api.skillhub.cn"
		key = "HAHA_MARKET_BASE_SKILLHUB"
	}
	if v := s.o.Environment()[key]; v != "" {
		base = v
	}
	u, e := url.Parse(base)
	if e != nil {
		return base + path
	}
	rel, _ := url.Parse(path)
	u = u.ResolveReference(rel)
	u.RawQuery = query.Encode()
	return u.String()
}
func (s *Service) fetch(ctx context.Context, source, target string) (*http.Response, error) {
	for _, v := range strings.Split(s.o.Environment()["HAHA_MARKET_DISABLE_PROVIDERS"], ",") {
		if strings.TrimSpace(v) == source {
			err := upstream(source, "MARKET_UPSTREAM_ERROR", source+" provider disabled")
			s.failure(source, err.Error())
			return nil, err
		}
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		client, err := s.client(ctx, target)
		if err != nil {
			last = upstream(source, "MARKET_UPSTREAM_ERROR", source+" request failed: "+err.Error())
			continue
		}
		requestCtx, cancel := context.WithCancel(ctx)
		expired := false
		timer := time.AfterFunc(10*time.Second, func() { cancel() })
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
		if err != nil {
			timer.Stop()
			cancel()
			client.CloseIdleConnections()
			last = upstream(source, "MARKET_UPSTREAM_ERROR", source+" request failed: "+err.Error())
			continue
		}
		req.Header.Set("Accept", "application/json, text/plain, */*")
		res, err := client.Do(req)
		if !timer.Stop() {
			expired = true
		}
		if err != nil {
			cancel()
			client.CloseIdleConnections()
			code, message := "MARKET_UPSTREAM_ERROR", source+" request failed: "+err.Error()
			if expired || errors.Is(err, context.DeadlineExceeded) {
				code, message = "MARKET_UPSTREAM_TIMEOUT", source+" request timed out"
			}
			last = upstream(source, code, message)
			continue
		}
		if res.StatusCode == 429 || res.StatusCode >= 500 {
			res.Body.Close()
			cancel()
			client.CloseIdleConnections()
			last = upstream(source, "MARKET_UPSTREAM_ERROR", fmt.Sprintf("%s responded %d", source, res.StatusCode))
			continue
		}
		res.Body = &responseBody{ReadCloser: res.Body, close: func() { cancel(); client.CloseIdleConnections() }}
		s.success(source)
		return res, nil
	}
	s.failure(source, last.Error())
	return nil, last
}

type responseBody struct {
	io.ReadCloser
	close func()
}

func (b *responseBody) Close() error { err := b.ReadCloser.Close(); b.close(); return err }
func (s *Service) clawFetch(ctx context.Context, target, slug string) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	pinned := requestedOwner(ctx, slug)
	if pinned == "" {
		if e := catalogEntry("clawhub", slug); e != nil {
			pinned = str(e["owner"])
		}
	}
	owner := pinned
	if owner == "" {
		s.mu.Lock()
		owner = s.owners[slug]
		s.mu.Unlock()
	}
	if owner != "" && !q.Has("owner") {
		q.Set("owner", owner)
		u.RawQuery = q.Encode()
	}
	res, err := s.fetch(ctx, "clawhub", u.String())
	if err != nil || res.StatusCode != 409 {
		return res, err
	}
	var data object
	_ = json.NewDecoder(res.Body).Decode(&data)
	res.Body.Close()
	matches := arr(data["matches"])
	resolved := ""
	if pinned == "" && data["code"] == "AMBIGUOUS_SKILL_SLUG" && len(matches) > 0 {
		resolved = str(obj(matches[0])["ownerHandle"])
	}
	if resolved == "" {
		return nil, upstream("clawhub", "MARKET_UPSTREAM_ERROR", "clawhub responded 409 for "+u.Path)
	}
	s.mu.Lock()
	s.owners[slug] = resolved
	s.mu.Unlock()
	q.Set("owner", resolved)
	u.RawQuery = q.Encode()
	return s.fetch(ctx, "clawhub", u.String())
}
func (s *Service) fetchJSON(ctx context.Context, source, target, slug string) (object, error) {
	var res *http.Response
	var err error
	if source == "clawhub" && slug != "" {
		res, err = s.clawFetch(ctx, target, slug)
	} else {
		res, err = s.fetch(ctx, source, target)
	}
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		code := "MARKET_UPSTREAM_ERROR"
		if source == "clawhub" && slug != "" && res.StatusCode == 404 {
			code = "MARKET_UPSTREAM_BAD_RESPONSE"
		}
		u, _ := url.Parse(target)
		err = upstream(source, code, fmt.Sprintf("%s responded %d for %s", source, res.StatusCode, u.Path))
		if source != "clawhub" || slug == "" {
			s.failure(source, err.Error())
		}
		return nil, err
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, upstream(source, "MARKET_UPSTREAM_ERROR", source+" request failed: "+err.Error())
	}
	var out object
	if json.Unmarshal(data, &out) != nil {
		err = upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", source+" returned invalid JSON")
		if source != "clawhub" || slug == "" {
			s.failure(source, err.Error())
		}
		return nil, err
	}
	if reports := out["securityReports"]; reports != nil {
		var raw protocol.JSONObject
		if json.Unmarshal(data, &raw) == nil {
			out["securityReports"] = orderedObject{obj(reports), jsonObjectKeys(raw.Get("securityReports"))}
		}
	}
	return out, nil
}

type orderedObject struct {
	values object
	keys   []string
}

func jsonObjectKeys(raw []byte) []string {
	if !json.Valid(raw) {
		return nil
	}
	canonical := protocol.StringifyValidatedJavaScriptJSON(raw)
	decoder := json.NewDecoder(strings.NewReader(string(canonical)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	keys := []string{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil
		}
		keys = append(keys, key.(string))
	}
	return keys
}
func normalizeClaw(item object, search bool) object {
	slug := str(item["slug"])
	out := object{"id": "clawhub:" + slug, "source": "clawhub", "slug": slug, "name": first(item["displayName"], slug), "summary": first(item["summary"]), "author": object{"handle": ""}, "stats": object{"downloads": float64(0)}, "tags": []any{}, "securityStatus": "unknown", "installState": "installable"}
	optional(out, "updatedAt", item["updatedAt"])
	if search {
		owner := obj(item["owner"])
		out["author"] = object{"handle": first(owner["handle"], item["ownerHandle"])}
		optional(obj(out["author"]), "displayName", owner["displayName"])
		optional(obj(out["author"]), "avatarUrl", owner["image"])
		if item["downloads"] != nil {
			obj(out["stats"])["downloads"] = item["downloads"]
		}
	} else {
		stats := obj(item["stats"])
		for _, key := range []string{"downloads", "installs", "stars"} {
			optional(obj(out["stats"]), key, stats[key])
		}
		tags := []any{}
		for _, t := range arr(item["topics"]) {
			if _, ok := t.(string); ok {
				tags = append(tags, t)
			}
		}
		out["tags"] = tags
		optional(out, "version", obj(item["latestVersion"])["version"])
	}
	return out
}
func normalizeSkillhub(item object) object {
	slug := str(item["slug"])
	out := object{"id": "skillhub:" + slug, "source": "skillhub", "slug": slug, "name": first(item["name"], item["displayName"], slug), "summary": first(item["description_zh"], item["description"], item["summary_zh"], item["summary"]), "author": object{"handle": first(item["ownerName"])}, "stats": object{"downloads": float64(0)}, "tags": []any{}, "securityStatus": "unknown", "requiresApiKey": obj(item["labels"])["requires_api_key"] == "true", "installState": "installable"}
	for _, key := range []string{"downloads", "installs", "stars"} {
		optional(obj(out["stats"]), key, item[key])
	}
	tags := []any{}
	for _, t := range arr(item["subCategories"]) {
		if n := str(obj(t)["name"]); n != "" {
			tags = append(tags, n)
		}
	}
	out["tags"] = tags
	for _, key := range []string{"category", "version", "verified"} {
		optional(out, key, item[key])
	}
	optional(out, "updatedAt", item["updated_at"])
	if str(item["iconUrl"]) != "" {
		out["iconUrl"] = item["iconUrl"]
	}
	if item["verified"] == true {
		out["securityStatus"] = "verified"
	}
	if item["source"] == "clawhub" {
		if u, e := url.Parse(str(item["upstream_url"])); e == nil && u.Scheme != "" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 && parts[len(parts)-1] != "" {
				out["upstream"] = object{"source": "clawhub", "slug": parts[len(parts)-1]}
			}
		}
	}
	return out
}
func parseInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	end := 0
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		end = 1
	}
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if n, e := strconv.Atoi(value[:end]); e == nil {
		return n
	}
	return fallback
}
func (s *Service) providerPage(ctx context.Context, source, q, cursor string, limit int) (*page, error) {
	query := url.Values{}
	path := "/api/v1/skills"
	key := "items"
	if source == "clawhub" {
		if q != "" {
			path = "/api/v1/search"
			key = "results"
			query.Set("q", q)
		} else {
			query.Set("limit", strconv.Itoa(limit))
			query.Set("sort", "downloads")
			if cursor != "" {
				query.Set("cursor", cursor)
			}
		}
	} else {
		path = "/api/skills"
		p := parseInt(cursor, 1)
		if p < 1 {
			p = 1
		}
		query.Set("page", strconv.Itoa(p))
		query.Set("pageSize", strconv.Itoa(limit))
		if q != "" {
			query.Set("keyword", q)
		}
	}
	data, e := s.fetchJSON(ctx, source, s.endpoint(source, path, query), "")
	if e != nil {
		return nil, e
	}
	out := &page{items: []object{}}
	if source == "skillhub" {
		envelope := data
		data = obj(data["data"])
		key = "skills"
		if envelope["code"] != float64(0) || data["skills"] == nil {
			return nil, upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", fmt.Sprintf("skillhub responded code=%v: %s", envelope["code"], first(envelope["message"], "bad payload")))
		}
		p := parseInt(cursor, 1)
		if p < 1 {
			p = 1
		}
		if float64(p*limit) < num(data["total"]) {
			out.next = strconv.Itoa(p + 1)
		}
	} else if q == "" {
		out.next = str(data["nextCursor"])
	}
	items, ok := data[key].([]any)
	if !ok {
		return nil, upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", source+" "+map[bool]string{true: "search", false: "list"}[q != ""]+" missing "+key)
	}
	for _, v := range items {
		item := obj(v)
		if str(item["slug"]) == "" {
			continue
		}
		if source == "clawhub" && q != "" {
			if v, exists := item["source"]; exists && v != "clawhub" {
				continue
			}
			if v, exists := obj(item["install"])["kind"]; exists && v != "clawhub" {
				continue
			}
			if len(out.items) >= limit {
				break
			}
		}
		if source == "clawhub" {
			out.items = append(out.items, normalizeClaw(item, q != ""))
		} else {
			out.items = append(out.items, normalizeSkillhub(item))
		}
	}
	return out, nil
}
func (s *Service) listFiles(ctx context.Context, source, slug, version string) ([]object, error) {
	path := "/api/v1/skills/" + url.PathEscape(slug)
	if source == "clawhub" {
		if version == "" {
			data, e := s.fetchJSON(ctx, source, s.endpoint(source, path, nil), slug)
			if e != nil {
				return nil, e
			}
			version = first(obj(data["latestVersion"])["version"], obj(obj(data["skill"])["latestVersion"])["version"])
		}
		if version == "" {
			return []object{}, nil
		}
		path += "/versions/" + url.PathEscape(version)
	} else {
		path += "/files"
	}
	data, e := s.fetchJSON(ctx, source, s.endpoint(source, path, nil), map[bool]string{true: slug, false: ""}[source == "clawhub"])
	if e != nil {
		return nil, e
	}
	if source == "clawhub" {
		data = obj(data["version"])
	}
	raw, ok := data["files"].([]any)
	if !ok && source == "skillhub" {
		return nil, upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", "skillhub files missing list")
	}
	files := []object{}
	for _, f := range raw {
		files = append(files, clone(obj(f)))
	}
	return files, nil
}
func normalizedFiles(files []object) ([]object, float64) {
	out := []object{}
	total := float64(0)
	for _, f := range files {
		copy := clone(f)
		copy["language"] = language(str(f["path"]))
		copy["tooBig"] = false
		out = append(out, copy)
		total += num(f["size"])
	}
	return out, total
}
func (s *Service) providerDetail(ctx context.Context, source, slug string) (object, error) {
	path := "/api/v1/skills/" + url.PathEscape(slug)
	lookupSlug := ""
	if source == "clawhub" {
		lookupSlug = slug
	}
	data, e := s.fetchJSON(ctx, source, s.endpoint(source, path, nil), lookupSlug)
	if e != nil {
		return nil, e
	}
	item := obj(data["skill"])
	if str(item["slug"]) == "" {
		return nil, upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", source+" detail missing skill")
	}
	var out object
	files := []object{}
	reports := []object{}
	version := ""
	description := ""
	if source == "clawhub" {
		out = normalizeClaw(item, false)
		version = first(obj(data["latestVersion"])["version"], out["version"])
		optional(out, "license", obj(data["latestVersion"])["license"])
		if version != "" {
			if vd, e := s.fetchJSON(ctx, source, s.endpoint(source, path+"/versions/"+url.PathEscape(version), nil), slug); e == nil {
				v := obj(vd["version"])
				for _, f := range arr(v["files"]) {
					files = append(files, clone(obj(f)))
				}
				if license := str(v["license"]); license != "" {
					out["license"] = license
				}
				security := obj(v["security"])
				if status := str(security["status"]); status != "" {
					state, text := "flagged", "Scan status: "+status
					if status == "clean" {
						state, text = "benign", "Clean"
						if security["hasWarnings"] == true {
							text = "Clean (with warnings)"
						}
					}
					out["securityStatus"] = state
					r := object{"vendor": "clawhub-scan", "status": status, "statusText": text}
					optional(r, "reportUrl", security["virustotalUrl"])
					reports = append(reports, r)
					for _, spec := range []struct{ key, vendor string }{{"vt", "VirusTotal"}, {"skillspector", "skillspector"}, {"llm", "LLM review"}} {
						scanner := obj(obj(security["scanners"])[spec.key])
						status := first(scanner["normalizedStatus"], scanner["status"])
						if status == "" {
							continue
						}
						text := status
						if n := str(scanner["recommendation"]); n != "" {
							text += " · " + n
						}
						report := object{"vendor": spec.vendor, "status": status, "statusText": text}
						if v := str(scanner["summary"]); v != "" {
							report["summary"] = v
						}
						if spec.key == "vt" {
							optional(report, "reportUrl", security["virustotalUrl"])
						}
						reports = append(reports, report)
					}
				}
			}
		}
		description = first(item["description"])
		if strings.HasPrefix(description, "---") {
			if parsed, e := markdownfrontmatter.Parse(description); e == nil {
				description = parsed.Content
				raw, _ := json.Marshal(parsed.Frontmatter)
				var fm object
				_ = json.Unmarshal(raw, &fm)
				out["descriptionFrontmatter"] = fm
			}
		}
	} else {
		out = normalizeSkillhub(item)
		if stats := obj(item["stats"]); len(stats) > 0 {
			for _, key := range []string{"downloads", "installs", "stars"} {
				optional(obj(out["stats"]), key, stats[key])
			}
		}
		version = first(obj(data["latestVersion"])["version"], out["version"])
		if list, e := s.listFiles(ctx, source, slug, ""); e == nil {
			files = list
		}
		description = str(out["summary"])
		for _, f := range files {
			if f["path"] == "SKILL.md" {
				if text, _, e := s.fetchFile(ctx, source, slug, "SKILL.md"); e == nil {
					description = text
				}
				break
			}
		}
		keys := orderedKeys(data["securityReports"])
		for _, vendor := range keys {
			report := obj(obj(data["securityReports"])[vendor])
			if status := str(report["status"]); status != "" {
				r := object{"vendor": vendor, "status": status, "statusText": first(report["statusText"], status)}
				optional(r, "reportUrl", report["reportUrl"])
				reports = append(reports, r)
			}
		}
		out["securityStatus"] = "unknown"
		if len(reports) > 0 {
			out["securityStatus"] = "benign"
			if item["verified"] == true {
				out["securityStatus"] = "verified"
			}
			for _, r := range reports {
				status := strings.ToLower(str(r["status"]))
				if status != "benign" && status != "safe" && status != "clean" {
					out["securityStatus"] = "flagged"
				}
			}
		}
	}
	owner := obj(data["owner"])
	fallback := ""
	if source == "skillhub" {
		fallback = str(obj(out["author"])["handle"])
	}
	out["author"] = object{"handle": first(owner["handle"], fallback)}
	optional(obj(out["author"]), "displayName", owner["displayName"])
	if source == "clawhub" || str(owner["image"]) != "" {
		optional(obj(out["author"]), "avatarUrl", owner["image"])
	}
	if version != "" {
		out["version"] = version
	} else {
		delete(out, "version")
	}
	if len(reports) > 0 {
		out["securityReports"] = reports
	}
	if source == "clawhub" {
		if handle := str(owner["handle"]); handle != "" {
			out["pageUrl"] = "https://clawhub.ai/" + url.PathEscape(handle) + "/" + url.PathEscape(slug)
		}
	}
	if text := meaningfulChangelog(obj(data["latestVersion"])["changelog"]); text != "" {
		note := object{"text": text}
		if version != "" {
			note["version"] = version
		}
		optional(note, "publishedAt", obj(data["latestVersion"])["createdAt"])
		out["changelog"] = note
	}

	out["description"] = description
	normalized, total := normalizedFiles(files)
	out["files"], out["totalSize"] = normalized, total
	return out, nil
}
func orderedKeys(value any) []string {
	if ordered, ok := value.(orderedObject); ok {
		return ordered.keys
	}
	m := obj(value)
	keys := []string{}
	for key := range m {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}
func sortStrings(keys []string) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}
func (s *Service) fetchFile(ctx context.Context, source, slug, path string) (string, int, error) {
	target := s.endpoint(source, "/api/v1/skills/"+url.PathEscape(slug)+"/file", url.Values{"path": {path}})
	var res *http.Response
	var e error
	if source == "clawhub" {
		res, e = s.clawFetch(ctx, target, slug)
	} else {
		res, e = s.fetch(ctx, source, target)
	}
	if e != nil {
		return "", 0, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		code := "MARKET_UPSTREAM_ERROR"
		if res.StatusCode == 404 {
			code = "MARKET_UPSTREAM_BAD_RESPONSE"
		}
		return "", 0, upstream(source, code, fmt.Sprintf("%s file fetch failed (%d)", source, res.StatusCode))
	}
	large := func() error {
		return upstream(source, "MARKET_UPSTREAM_BAD_RESPONSE", fmt.Sprintf("%s file %s exceeds the actual size limit (%d bytes)", source, path, maxFileSize))
	}
	if res.ContentLength > maxFileSize {
		return "", 0, large()
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, maxFileSize+1))
	if e != nil {
		return "", 0, e
	}
	if len(data) > maxFileSize {
		return "", 0, large()
	}
	content := string(protocol.DecodeJavaScriptUTF8(data))
	content = strings.TrimPrefix(content, "\ufeff")
	return content, len(data), nil
}
