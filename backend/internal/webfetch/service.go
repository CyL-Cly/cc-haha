package webfetch

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

const MaxMarkdownLength = 100000
const maxHTTPContentLength = 10 * 1024 * 1024
const maxCacheSize = 50 * 1024 * 1024

type Settings struct {
	SkipPreflight    *bool
	DesktopServerURL string
}
type Persisted struct {
	Path  string
	Size  int
	Error error
}
type Options struct {
	HTTPClient    *http.Client
	Settings      func(context.Context) (Settings, error)
	Now           func() time.Time
	UserAgent     string
	PersistID     func() string
	PersistBinary func(context.Context, []byte, string, string) (Persisted, error)
	ApplyPrompt   func(context.Context, json.RawMessage, bool) (json.RawMessage, error)
}
type Service struct {
	options     Options
	mu          sync.Mutex
	urls        map[string]*list.Element
	urlOrder    *list.List
	urlSize     int
	domains     map[string]*list.Element
	domainOrder *list.List
}
type cacheEntry struct {
	key     string
	value   FetchedContent
	size    int
	expires time.Time
}
type domainEntry struct {
	key     string
	expires time.Time
}
type FetchedContent struct {
	Content                              string
	Bytes, Code                          int
	CodeText, ContentType, PersistedPath string
	PersistedSize                        int
}
type Redirect struct {
	Type        string `json:"type"`
	OriginalURL string `json:"originalUrl"`
	RedirectURL string `json:"redirectUrl"`
	StatusCode  int    `json:"statusCode"`
}
type Result struct {
	Fetched  *FetchedContent
	Redirect *Redirect
}
type Output struct {
	Bytes      int             `json:"bytes"`
	Code       int             `json:"code"`
	CodeText   string          `json:"codeText"`
	Result     json.RawMessage `json:"result"`
	DurationMS float64         `json:"durationMs"`
	URL        string          `json:"url"`
}
type NamedError struct{ Name, Message string }

func (e *NamedError) Error() string { return e.Message }
func New(o Options) (*Service, error) {
	if o.HTTPClient == nil || o.Settings == nil || o.Now == nil || o.UserAgent == "" || o.PersistID == nil || o.PersistBinary == nil || o.ApplyPrompt == nil {
		return nil, errors.New("WebFetch requires actual HTTP/settings/clock/UA/binary persistence/secondary model producers")
	}
	return &Service{options: o, urls: map[string]*list.Element{}, urlOrder: list.New(), domains: map[string]*list.Element{}, domainOrder: list.New()}, nil
}
func (s *Service) ClearCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls = map[string]*list.Element{}
	s.urlOrder.Init()
	s.urlSize = 0
	s.domains = map[string]*list.Element{}
	s.domainOrder.Init()
}
func ShouldSkipPreflight(settings Settings) bool {
	if settings.SkipPreflight != nil {
		return *settings.SkipPreflight
	}
	return settings.DesktopServerURL != ""
}
func ValidateURL(raw string) bool {
	if len(utf16.Encode([]rune(raw))) > 2000 {
		return false
	}
	u, e := weburl.Parse(raw)
	return e == nil && u.Username() == "" && u.Password() == "" && len(strings.Split(u.Hostname(), ".")) >= 2
}
func PermittedRedirect(original, target string) bool {
	a, e := weburl.Parse(original)
	if e != nil {
		return false
	}
	b, e := weburl.Parse(target)
	return e == nil && a.Protocol() == b.Protocol() && a.Port() == b.Port() && b.Username() == "" && b.Password() == "" && strings.TrimPrefix(a.Hostname(), "www.") == strings.TrimPrefix(b.Hostname(), "www.")
}
func (s *Service) cachedURL(key string) *FetchedContent {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.urls[key]
	if entry == nil {
		return nil
	}
	value := entry.Value.(cacheEntry)
	if !s.options.Now().Before(value.expires) {
		s.removeURL(entry)
		return nil
	}
	s.urlOrder.MoveToFront(entry)
	copy := value.value
	return &copy
}
func (s *Service) removeURL(entry *list.Element) {
	v := entry.Value.(cacheEntry)
	s.urlSize -= v.size
	delete(s.urls, v.key)
	s.urlOrder.Remove(entry)
}
func (s *Service) cacheURL(key string, value FetchedContent, size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size = max(1, size)
	if old := s.urls[key]; old != nil {
		s.removeURL(old)
	}
	if size > maxCacheSize {
		return
	}
	s.urls[key] = s.urlOrder.PushFront(cacheEntry{key, value, size, s.options.Now().Add(15 * time.Minute)})
	s.urlSize += size
	for s.urlSize > maxCacheSize {
		s.removeURL(s.urlOrder.Back())
	}
}
func (s *Service) cachedDomain(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.domains[key]
	if entry == nil {
		return false
	}
	if !s.options.Now().Before(entry.Value.(domainEntry).expires) {
		delete(s.domains, key)
		s.domainOrder.Remove(entry)
		return false
	}
	s.domainOrder.MoveToFront(entry)
	return true
}
func (s *Service) cacheDomain(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.domains[key]; entry != nil {
		s.domainOrder.Remove(entry)
	}
	s.domains[key] = s.domainOrder.PushFront(domainEntry{key, s.options.Now().Add(5 * time.Minute)})
	if s.domainOrder.Len() > 128 {
		entry := s.domainOrder.Back()
		delete(s.domains, entry.Value.(domainEntry).key)
		s.domainOrder.Remove(entry)
	}
}
func encodeURIComponent(s string) string {
	var b strings.Builder
	const hex = "0123456789ABCDEF"
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.!~*'()", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
func (s *Service) CheckDomain(ctx context.Context, domain string) (string, error) {
	if s.cachedDomain(domain) {
		return "allowed", nil
	}
	requestContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(requestContext, "GET", "https://api.anthropic.com/api/web/domain_info?domain="+encodeURIComponent(domain), nil)
	if e != nil {
		return "check_failed", e
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "axios/1.15.2")
	req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	response, e := s.options.HTTPClient.Do(req)
	if e != nil {
		return "check_failed", e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "check_failed", fmt.Errorf("Domain check returned status %d", response.StatusCode)
	}
	body, e := ReadDecodedResponse(response)
	if e != nil {
		return "check_failed", e
	}
	var data protocol.JSONObject
	if json.Unmarshal(body, &data) != nil {
		return "blocked", nil
	}
	if string(data.Get("can_fetch")) == "true" {
		s.cacheDomain(domain)
		return "allowed", nil
	}
	return "blocked", nil
}
func (s *Service) GetWithPermittedRedirects(ctx context.Context, raw string) (Result, error) {
	client := *s.options.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	current := raw
	if u, e := weburl.Parse(raw); e == nil && u.Protocol() != "http:" && u.Protocol() != "https:" {
		return Result{}, &NamedError{"AxiosError", "Unsupported protocol " + u.Protocol()}
	}
	for depth := 0; depth <= 10; depth++ {
		requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
		req, e := http.NewRequestWithContext(requestContext, "GET", current, nil)
		if e != nil {
			cancel()
			return Result{}, e
		}
		req.Header.Set("Accept", "text/markdown, text/html, */*")
		req.Header.Set("User-Agent", s.options.UserAgent)
		req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
		response, e := client.Do(req)
		if e != nil {
			cancel()
			return Result{}, e
		}
		decoded, closer, e := decodedHTTPBody(response)
		var body []byte
		if e == nil {
			body, e = io.ReadAll(io.LimitReader(decoded, maxHTTPContentLength+1))
		}
		if closer != nil {
			_ = closer.Close()
		}
		_ = response.Body.Close()
		cancel()
		if e != nil {
			return Result{}, e
		}
		if len(body) > maxHTTPContentLength {
			return Result{}, fmt.Errorf("maxContentLength size of %d exceeded", maxHTTPContentLength)
		}
		switch response.StatusCode {
		case 301, 302, 307, 308:
			location := response.Header.Get("Location")
			if location == "" {
				return Result{}, errors.New("Redirect missing Location header")
			}
			u, e := weburl.ParseRef(current, location)
			if e != nil {
				return Result{}, e
			}
			target := u.String()
			if PermittedRedirect(current, target) {
				current = target
				continue
			}
			return Result{Redirect: &Redirect{"redirect", current, target, response.StatusCode}}, nil
		}
		if response.StatusCode == 403 && response.Header.Get("X-Proxy-Error") == "blocked-by-allowlist" {
			u, _ := weburl.Parse(current)
			b, _ := protocol.NewJSONObject(protocol.JSONField{Name: "error_type", Value: quote("EGRESS_BLOCKED")}, protocol.JSONField{Name: "domain", Value: quote(u.Hostname())}, protocol.JSONField{Name: "message", Value: quote("Access to " + u.Hostname() + " is blocked by the network egress proxy.")}).MarshalJSON()
			return Result{}, &NamedError{"EgressBlockedError", string(b)}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return Result{}, fmt.Errorf("Request failed with status code %d", response.StatusCode)
		}
		contentType := response.Header.Get("Content-Type")
		content := string(protocol.DecodeJavaScriptUTF8(body))
		persisted := Persisted{}
		if IsBinaryContentType(contentType) {
			persisted, e = s.options.PersistBinary(ctx, body, contentType, s.options.PersistID())
			if e != nil {
				return Result{}, e
			}
		}
		if strings.Contains(contentType, "text/html") {
			content, e = HTMLToMarkdown(content)
			if e != nil {
				return Result{}, e
			}
		}
		return Result{Fetched: &FetchedContent{Content: content, Bytes: len(body), Code: response.StatusCode, CodeText: response.Status[len(fmt.Sprintf("%d ", response.StatusCode)):], ContentType: contentType, PersistedPath: persisted.Path, PersistedSize: persisted.Size}}, nil
	}
	return Result{}, errors.New("Too many redirects (exceeded 10)")
}
func (s *Service) Fetch(ctx context.Context, raw string) (Result, error) {
	if !ValidateURL(raw) {
		return Result{}, errors.New("Invalid URL")
	}
	if cached := s.cachedURL(raw); cached != nil {
		return Result{Fetched: cached}, nil
	}
	u, e := weburl.Parse(raw)
	if e != nil {
		return Result{}, e
	}
	if u.Protocol() == "http:" {
		u.SetProtocol("https:")
	}
	settings, e := s.options.Settings(ctx)
	if e != nil {
		return Result{}, e
	}
	if !ShouldSkipPreflight(settings) {
		status, _ := s.CheckDomain(ctx, u.Hostname())
		if status == "blocked" {
			return Result{}, &NamedError{"DomainBlockedError", "Claude Code is unable to fetch from " + u.Hostname()}
		}
		if status == "check_failed" {
			return Result{}, &NamedError{"DomainCheckFailedError", "Unable to verify if domain " + u.Hostname() + " is safe to fetch. This may be due to network restrictions or enterprise security policies blocking claude.ai."}
		}
	}
	result, e := s.GetWithPermittedRedirects(ctx, u.String())
	if e == nil && result.Fetched != nil {
		size := result.Fetched.Bytes
		if strings.Contains(result.Fetched.ContentType, "text/html") {
			size = len(result.Fetched.Content)
		}
		s.cacheURL(raw, *result.Fetched, size)
	}
	return result, e
}
