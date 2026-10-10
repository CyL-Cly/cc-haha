// Package marketapi implements the original two-source Skills Market service.
package marketapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const maxFileSize = 5 * 1024 * 1024
const maxTotalSize = 20 * 1024 * 1024
const maxFileCount = 200
const previewSize = 300 * 1024
const metaFilename = ".market-meta.json"

var sources = []string{"clawhub", "skillhub"}

type object = map[string]any
type Options struct {
	ConfigDir   string
	Environment func() map[string]string
	Settings    func(context.Context) (protocol.JSONObject, error)
	HTTPClient  *http.Client
	Changed     func()
	Now         func() time.Time
}
type cacheEntry struct {
	value           any
	expires, stored int64
}
type page struct {
	items []object
	next  string
}
type Service struct {
	o          Options
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	listMu     sync.Mutex
	cache      map[string]cacheEntry
	order      []string
	health     map[string]object
	owners     map[string]string
	inFlight   map[string]bool
	closed     bool
	operations sync.WaitGroup
}
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string                    { return e.Message }
func fail(status int, code, message string) error { return &Error{status, code, message} }
func upstream(source, code, message string) error { return fail(502, code, message) }
func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.ConfigDir) {
		return nil, errors.New("Market requires explicit absolute config directory")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Environment == nil {
		o.Environment = func() map[string]string { return map[string]string{} }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, ctx: ctx, cancel: cancel, cache: map[string]cacheEntry{}, health: map[string]object{"clawhub": {"status": "ok"}, "skillhub": {"status": "ok"}}, owners: map[string]string{}, inFlight: map[string]bool{}}, nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.operations.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func clone(v object) object {
	out := object{}
	for k, x := range v {
		out[k] = x
	}
	return out
}
func obj(v any) object {
	if x, ok := v.(orderedObject); ok {
		return x.values
	}
	if x, ok := v.(map[string]any); ok {
		return x
	}
	return object{}
}
func str(v any) string  { x, _ := v.(string); return x }
func num(v any) float64 { x, _ := v.(float64); return x }
func arr(v any) []any   { x, _ := v.([]any); return x }
func first(v ...any) string {
	for _, x := range v {
		if t := str(x); t != "" {
			return t
		}
	}
	return ""
}
func optional(dst object, key string, v any) {
	if v != nil {
		dst[key] = v
	}
}
func language(name string) string {
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	langs := map[string]string{"md": "markdown", "ts": "typescript", "tsx": "typescript", "js": "javascript", "jsx": "javascript", "mjs": "javascript", "cjs": "javascript", "json": "json", "yaml": "yaml", "yml": "yaml", "sh": "bash", "bash": "bash", "zsh": "bash", "py": "python", "toml": "toml", "css": "css", "html": "html", "txt": "text", "xml": "xml", "sql": "sql", "rs": "rust", "go": "go", "rb": "ruby"}
	if v := langs[ext]; v != "" {
		return v
	}
	return "text"
}
func validSource(source string) bool { return source == "clawhub" || source == "skillhub" }
func safeDir(slug string) string {
	n := strings.ToLower(slug)
	if n == "" || strings.Contains(n, "..") {
		return ""
	}
	for i, c := range n {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return ""
	}
	return n
}
func validFile(path string) bool {
	return path != "" && len(utf16.Encode([]rune(path))) <= 512 && !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "\\") && !strings.Contains(path, "..") && !strings.ContainsRune(path, 0)
}
func (s *Service) touch(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.order = append(s.order, key)
}
func (s *Service) cached(key string, stale bool) (any, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || !stale && s.o.Now().UnixMilli() > e.expires {
		return nil, 0, false
	}
	if !stale {
		s.touch(key)
	}
	return e.value, e.stored, true
}
func (s *Service) put(key string, value any, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now().UnixMilli()
	s.cache[key] = cacheEntry{value, now + ttl.Milliseconds(), now}
	s.touch(key)
	if len(s.order) > 500 {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
}
func (s *Service) Status() object {
	s.mu.Lock()
	defer s.mu.Unlock()
	return object{"clawhub": clone(s.health["clawhub"]), "skillhub": clone(s.health["skillhub"])}
}
func (s *Service) success(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health[source] = object{"status": "ok", "fetchedAt": s.o.Now().UnixMilli()}
}
func (s *Service) failure(source, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.health[source]
	status := "failed"
	if old["status"] == "ok" && old["fetchedAt"] != nil {
		status = "degraded"
	}
	s.health[source] = object{"status": status, "error": message}
	optional(s.health[source], "fetchedAt", old["fetchedAt"])
}
func (s *Service) skillsDir() string { return filepath.Join(s.o.ConfigDir, "skills") }
func (s *Service) meta(dir string) object {
	data, err := os.ReadFile(filepath.Join(s.skillsDir(), dir, metaFilename))
	if err != nil {
		return nil
	}
	var m object
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	source, slug := str(m["source"]), str(m["slug"])
	count, ok := m["fileCount"].(float64)
	if !validSource(source) || safeDir(slug) == "" || safeDir(slug) != dir || str(m["id"]) != source+":"+slug || str(m["installedAt"]) == "" || !ok || count < 0 || float64(int64(count)) != count {
		return nil
	}
	if v, ok := m["version"]; ok {
		if _, ok := v.(string); !ok {
			return nil
		}
	}
	if v, ok := m["signatureVerified"]; ok {
		if _, ok := v.(bool); !ok {
			return nil
		}
	}
	return m
}
func (s *Service) annotate(skill object) object {
	out := clone(skill)
	delete(out, "notInstallableReason")
	delete(out, "installedInfo")
	dir := safeDir(str(out["slug"]))
	if dir == "" {
		out["installState"] = "not-installable"
		out["notInstallableReason"] = "invalid-name"
		return out
	}
	info, err := os.Stat(filepath.Join(s.skillsDir(), dir))
	if err != nil || !info.IsDir() {
		out["installState"] = "installable"
		return out
	}
	m := s.meta(dir)
	if m != nil && str(m["id"]) == str(out["source"])+":"+str(out["slug"]) {
		out["installState"] = "installed"
		out["installedInfo"] = object{"installedAt": m["installedAt"], "dirName": dir}
		optional(obj(out["installedInfo"]), "version", m["version"])
	} else {
		out["installState"] = "not-installable"
		out["notInstallableReason"] = "name-conflict"
	}
	return out
}
func fileLimits(detail object) object {
	files, _ := detail["files"].([]object)
	total := num(detail["totalSize"])
	hasSkill, large := false, false
	for _, f := range files {
		f["tooBig"] = num(f["size"]) > maxFileSize
		large = large || f["tooBig"] == true
		hasSkill = hasSkill || f["path"] == "SKILL.md"
	}
	if detail["installState"] != "installable" {
		return detail
	}
	reason := ""
	if len(files) == 0 || !hasSkill {
		reason = "empty-file-list"
	} else if len(files) > maxFileCount {
		reason = "too-many-files"
	} else if large || total > maxTotalSize {
		reason = "file-too-large"
	}
	if reason != "" {
		detail["installState"] = "not-installable"
		detail["notInstallableReason"] = reason
	}
	return detail
}
func decodeCursor(raw string) map[string]string {
	out := map[string]string{}
	data, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return out
	}
	var m object
	if json.Unmarshal(data, &m) != nil {
		return out
	}
	for _, source := range sources {
		if v := str(m[source]); v != "" {
			out[source] = v
		}
	}
	return out
}
func encodeCursor(m map[string]string) any {
	if len(m) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for _, source := range sources {
		if v := m[source]; v != "" {
			if b.Len() > 1 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(source)
			x, _ := json.Marshal(v)
			b.Write(k)
			b.WriteByte(':')
			b.Write(x)
		}
	}
	b.WriteByte('}')
	return base64.RawURLEncoding.EncodeToString([]byte(b.String()))
}

type ListParams struct {
	Q, Source, Security, Installed, Cursor string
	Limit                                  int
}

func (s *Service) List(ctx context.Context, p ListParams) (object, error) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	type outcome struct {
		p      *page
		status object
	}
	outcomes := map[string]outcome{}
	var lock sync.Mutex
	var wg sync.WaitGroup
	cursor := decodeCursor(p.Cursor)
	for _, source := range sources {
		if p.Source != "all" && p.Source != source {
			continue
		}
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			o := outcome{}
			if p.Cursor != "" && cursor[source] == "" {
				o = outcome{&page{items: []object{}}, object{"status": "ok", "fromCache": true}}
			} else {
				limit := p.Limit
				if p.Q != "" && source == "clawhub" {
					limit = 50
				}
				key := fmt.Sprintf("list:%s:%s:%d", source, cursor[source], limit)
				ttl := 5 * time.Minute
				if p.Q != "" {
					key = fmt.Sprintf("search:%s:%s:%s:%d", source, p.Q, cursor[source], limit)
					ttl = 2 * time.Minute
				}
				if v, _, ok := s.cached(key, false); ok {
					o = outcome{v.(*page), object{"status": "ok", "fetchedAt": s.o.Now().UnixMilli(), "fromCache": true}}
				} else {
					fetchedPage, err := s.providerPage(ctx, source, p.Q, cursor[source], limit)
					if err == nil {
						s.put(key, fetchedPage, ttl)
						o = outcome{fetchedPage, object{"status": "ok", "fetchedAt": s.o.Now().UnixMilli(), "fromCache": false}}
					} else if v, stored, ok := s.cached(key, true); ok {
						o = outcome{v.(*page), object{"status": "cached", "fetchedAt": stored, "fromCache": true, "error": err.Error()}}
					} else {
						o.status = clone(obj(s.Status()[source]))
						o.status["fromCache"] = false
						o.status["error"] = err.Error()
					}
				}
			}
			lock.Lock()
			outcomes[source] = o
			lock.Unlock()
		}(source)
	}
	wg.Wait()
	merged := []object{}
	next := map[string]string{}
	status := object{}
	for _, source := range sources {
		if o, ok := outcomes[source]; ok {
			status[source] = o.status
			if o.p != nil {
				merged = append(merged, o.p.items...)
				if o.p.next != "" {
					next[source] = o.p.next
				}
			}
		} else {
			status[source] = object{"status": "ok", "fromCache": false}
		}
	}
	bySlug := map[string]object{}
	for _, item := range merged {
		if item["source"] == "clawhub" {
			bySlug[str(item["slug"])] = item
		}
	}
	deduped := []object{}
	for _, item := range merged {
		if item["source"] == "skillhub" {
			if original := bySlug[str(obj(item["upstream"])["slug"])]; original != nil {
				mirrors, _ := original["mirrors"].([]string)
				original["mirrors"] = append(mirrors, str(item["id"]))
				if str(original["iconUrl"]) == "" && str(item["iconUrl"]) != "" {
					original["iconUrl"] = item["iconUrl"]
				}
				if original["securityStatus"] == "unknown" && item["securityStatus"] != "unknown" {
					original["securityStatus"] = item["securityStatus"]
				}
				if len(arr(original["tags"])) == 0 && len(arr(item["tags"])) > 0 {
					original["tags"] = item["tags"]
				}
				continue
			}
		}
		deduped = append(deduped, item)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		return num(obj(deduped[i]["stats"])["downloads"]) > num(obj(deduped[j]["stats"])["downloads"])
	})
	items := []object{}
	for _, item := range deduped {
		item = s.annotate(item)
		if p.Security != "" && p.Security != "all" && item["securityStatus"] != p.Security {
			continue
		}
		if p.Installed == "installed" && item["installState"] != "installed" || p.Installed == "installable" && item["installState"] == "installed" {
			continue
		}
		items = append(items, item)
	}
	return object{"items": items, "nextCursor": encodeCursor(next), "sources": status}, nil
}
func (s *Service) Detail(ctx context.Context, source, slug string) (object, object, error) {
	key := "detail:" + source + ":" + slug
	status := object{"status": "ok", "fetchedAt": s.o.Now().UnixMilli(), "fromCache": true}
	var d object
	if v, _, ok := s.cached(key, false); ok {
		d = v.(object)
	} else {
		var err error
		d, err = s.providerDetail(ctx, source, slug)
		if err != nil {
			if v, stored, ok := s.cached(key, true); ok {
				d = v.(object)
				status = object{"status": "cached", "fetchedAt": stored, "fromCache": true, "error": err.Error()}
			} else {
				return nil, nil, err
			}
		} else {
			s.put(key, d, 10*time.Minute)
			status["fromCache"] = false
		}
	}
	copy := s.annotate(d)
	files, _ := d["files"].([]object)
	owned := make([]object, len(files))
	for i, f := range files {
		owned[i] = clone(f)
	}
	copy["files"] = owned
	return fileLimits(copy), status, nil
}
func (s *Service) File(ctx context.Context, source, slug, path string) (object, error) {
	key := "file:" + source + ":" + slug + ":" + path
	if v, _, ok := s.cached(key, false); ok {
		return v.(object), nil
	}
	content, size, err := s.fetchFile(ctx, source, slug, path)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len([]byte(content)) > previewSize {
		content = string(protocol.DecodeJavaScriptUTF8([]byte(content)[:previewSize]))
		truncated = true
	}
	out := object{"path": path, "content": content, "language": language(path), "size": size, "truncated": truncated}
	s.put(key, out, 30*time.Minute)
	return out, nil
}
func (s *Service) client(ctx context.Context, target string) (*http.Client, error) {
	settings := protocol.NewJSONObject()
	if s.o.Settings != nil {
		if v, e := s.o.Settings(ctx); e == nil {
			settings = v
		}
	}
	return nativeprovidermodels.HTTPClientForNetwork(settings, s.o.Environment(), target, s.o.HTTPClient)
}
