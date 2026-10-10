package access

import (
	"net/http"
	"strings"

	weburl "github.com/nlnwa/whatwg-url/url"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type RequestKind string

const (
	RequestLocalTrusted RequestKind = "local-trusted"
	RequestInternalSDK  RequestKind = "internal-sdk"
	RequestH5Browser    RequestKind = "h5-browser"
)

// Request is the Fetch request boundary used by the legacy H5 policy. Headers
// remain case-insensitive and duplicate values use Fetch Headers' comma join.
type Request struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers http.Header `json:"headers"`
}

type H5Context struct {
	ClientAddress              string `json:"clientAddress"`
	TrustedRendererOrigin      string `json:"trustedRendererOrigin"`
	LocalAccessTokenConfigured bool   `json:"localAccessTokenConfigured"`
	LocalAccessAuthorized      bool   `json:"localAccessAuthorized"`
	InternalSDKAuthorized      bool   `json:"internalSdkAuthorized"`
}

var proxyTraceHeaders = []string{
	"forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto", "x-real-ip", "via",
}

func NormalizeHostname(hostname string) string {
	// JS toLowerCase uses full Unicode mappings and final-sigma context;
	// strings.ToLower would change İΣ from i̇ς to iσ.
	return cases.Lower(language.Und).String(strings.TrimSuffix(strings.TrimPrefix(trimConfiguredToken(hostname), "["), "]"))
}

func IsLoopbackHost(hostname string) bool {
	normalized := NormalizeHostname(hostname)
	if strings.HasPrefix(normalized, "::ffff:") {
		return IsLoopbackHost(strings.TrimPrefix(normalized, "::ffff:"))
	}
	return normalized == "localhost" || normalized == "::1" || isLoopbackIPv4(normalized)
}

func isLoopbackBrowserOrigin(origin string) bool {
	parsed, err := weburl.Parse(origin)
	return err == nil && (parsed.Scheme() == "http" || parsed.Scheme() == "https") && IsLoopbackHost(parsed.Hostname())
}

// ResolveTrustedRendererOrigin returns an empty string for the TS null result.
// Credentials are forbidden; the WHATWG origin strips path/query/fragment and
// canonicalizes IPv4 spellings, IPv6, IDNA and default ports.
func ResolveTrustedRendererOrigin(value string) string {
	if value == "" || !isLoopbackBrowserOrigin(value) {
		return ""
	}
	parsed, _ := weburl.Parse(value)
	if parsed.Username() != "" || parsed.Password() != "" {
		return ""
	}
	return h5URLOrigin(parsed)
}

func h5URLOrigin(url *weburl.Url) string {
	switch url.Scheme() {
	case "ftp", "http", "https", "ws", "wss":
		return url.Scheme() + "://" + url.Host()
	case "blob":
		inner, err := weburl.Parse(url.Pathname())
		if err == nil && (inner.Scheme() == "http" || inner.Scheme() == "https") {
			return h5URLOrigin(inner)
		}
	}
	return "null"
}

func pathnameDirectory(pathname string) string {
	slash := strings.LastIndex(pathname, "/")
	if slash < 0 {
		return "/"
	}
	return pathname[:slash+1]
}

func pathnameExtension(pathname string) string {
	filename := pathname[strings.LastIndex(pathname, "/")+1:]
	dot := strings.LastIndex(filename, ".")
	if dot < 0 {
		return ""
	}
	return strings.ToLower(filename[dot:])
}

type filesystemScope struct {
	kind string
	root string
}

func filesystemCapabilityScope(pathname string) *filesystemScope {
	if strings.HasPrefix(pathname, "/preview-fs/") {
		sessionEnd := strings.Index(pathname[len("/preview-fs/"):], "/")
		if sessionEnd < 0 {
			return nil
		}
		return &filesystemScope{kind: "preview-fs", root: pathname[:len("/preview-fs/")+sessionEnd+1]}
	}
	if strings.HasPrefix(pathname, "/local-file/") {
		return &filesystemScope{kind: "local-file", root: "/local-file/"}
	}
	return nil
}

func isPreviewStaticDestination(destination string) bool {
	switch destination {
	case "audio", "font", "image", "manifest", "script", "style", "track", "video", "worker":
		return true
	}
	return false
}

func allowsRefererParent(extension string) bool {
	switch extension {
	case ".css", ".cjs", ".js", ".mjs":
		return true
	}
	return false
}

// Fetch Headers get() combines duplicate field values. Presence is separate
// from an empty value, which matters for proxy traces and Fetch Metadata.
func h5Header(headers http.Header, name string) (string, bool) {
	var values []string
	present := false
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			present = true
			for _, entry := range entries {
				values = append(values, strings.Trim(entry, " \t\r\n"))
			}
		}
	}
	return strings.Join(values, ", "), present
}

func h5RequestMethod(method string) string {
	if method == "" {
		return "GET"
	}
	switch strings.ToUpper(method) {
	case "DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT":
		return strings.ToUpper(method)
	}
	return method
}

// Passive browser subresources retain the preview capability and directory.
// The HTML entry can access its own descendants. CSS/modules may additionally
// load one parent directory, without crossing the filesystem capability root.
func isSameOriginFilesystemAsset(request Request, url *weburl.Url, origin string) bool {
	method := h5RequestMethod(request.Method)
	if method != "GET" && method != "HEAD" {
		return false
	}
	site, _ := h5Header(request.Headers, "Sec-Fetch-Site")
	destination, _ := h5Header(request.Headers, "Sec-Fetch-Dest")
	if site != "same-origin" || !isPreviewStaticDestination(destination) {
		return false
	}
	referer, _ := h5Header(request.Headers, "Referer")
	refererURL, err := weburl.Parse(referer)
	if err != nil || h5URLOrigin(refererURL) != h5URLOrigin(url) {
		return false
	}
	if origin != "" {
		originURL, err := weburl.Parse(origin)
		if err != nil || h5URLOrigin(originURL) != h5URLOrigin(url) {
			return false
		}
	}
	requestScope := filesystemCapabilityScope(url.Pathname())
	refererScope := filesystemCapabilityScope(refererURL.Pathname())
	if requestScope == nil || refererScope == nil || requestScope.kind != refererScope.kind || requestScope.root != refererScope.root {
		return false
	}
	refererDirectory := pathnameDirectory(refererURL.Pathname())
	if strings.HasPrefix(url.Pathname(), refererDirectory) {
		return true
	}
	if !allowsRefererParent(pathnameExtension(refererURL.Pathname())) {
		return false
	}
	parentDirectory := pathnameDirectory(strings.TrimSuffix(refererDirectory, "/"))
	return strings.HasPrefix(parentDirectory, requestScope.root) && strings.HasPrefix(url.Pathname(), parentDirectory)
}

func isCrossSiteSubresource(headers http.Header) bool {
	site, _ := h5Header(headers, "Sec-Fetch-Site")
	if site != "cross-site" && site != "same-site" {
		return false
	}
	mode, present := h5Header(headers, "Sec-Fetch-Mode")
	return present && mode != "navigate"
}

func isLocalDesktopOrNavigationOrigin(request Request, origin string, context H5Context) bool {
	if origin == "" {
		return !isCrossSiteSubresource(request.Headers)
	}
	if origin == "file://" {
		return true
	}
	_, preflight := h5Header(request.Headers, "Access-Control-Request-Method")
	if h5RequestMethod(request.Method) == "OPTIONS" && origin == context.TrustedRendererOrigin && preflight {
		return true
	}
	if context.LocalAccessTokenConfigured {
		return false
	}
	return isLoopbackBrowserOrigin(origin)
}

func hasProxyTraceHeaders(headers http.Header) bool {
	for _, name := range proxyTraceHeaders {
		if _, present := h5Header(headers, name); present {
			return true
		}
	}
	return false
}

func isLocalTrustedRequest(request Request, url *weburl.Url, context H5Context, origin string) bool {
	if context.LocalAccessAuthorized {
		return true
	}
	if isSameOriginFilesystemAsset(request, url, origin) {
		return true
	}
	site, _ := h5Header(request.Headers, "Sec-Fetch-Site")
	destination, _ := h5Header(request.Headers, "Sec-Fetch-Dest")
	if filesystemCapabilityScope(url.Pathname()) != nil && site == "same-origin" && isPreviewStaticDestination(destination) {
		return false
	}
	if context.ClientAddress == "" || hasProxyTraceHeaders(request.Headers) {
		return false
	}
	return IsLoopbackHost(context.ClientAddress) && IsLoopbackHost(url.Hostname()) && isLocalDesktopOrNavigationOrigin(request, origin, context)
}

func isFilesystemCapabilityPath(pathname string) bool {
	return strings.HasPrefix(pathname, "/local-file/") || strings.HasPrefix(pathname, "/preview-fs/")
}

func classifyParsedH5Request(request Request, url *weburl.Url, context H5Context) RequestKind {
	origin, _ := h5Header(request.Headers, "Origin")
	localTrusted := isLocalTrustedRequest(request, url, context, origin)
	if isFilesystemCapabilityPath(url.Pathname()) {
		if localTrusted {
			return RequestLocalTrusted
		}
		return RequestH5Browser
	}
	if strings.HasPrefix(url.Pathname(), "/sdk/") && (localTrusted || context.InternalSDKAuthorized) {
		return RequestInternalSDK
	}
	if localTrusted {
		return RequestLocalTrusted
	}
	return RequestH5Browser
}

func ClassifyH5Request(request Request, context H5Context) (RequestKind, error) {
	url, err := weburl.Parse(request.URL)
	if err != nil {
		return "", err
	}
	return classifyParsedH5Request(request, url, context), nil
}

func ShouldRequireH5Token(request Request, context H5Context, h5Enabled bool) (bool, error) {
	if !h5Enabled {
		return false, nil
	}
	url, err := weburl.Parse(request.URL)
	if err != nil {
		return false, err
	}
	path := url.Pathname()
	if !strings.HasPrefix(path, "/api/") && !isFilesystemCapabilityPath(path) && !strings.HasPrefix(path, "/proxy/") && !strings.HasPrefix(path, "/ws/") {
		return false, nil
	}
	return classifyParsedH5Request(request, url, context) == RequestH5Browser, nil
}

func ShouldBlockDisabledH5Access(request Request, context H5Context, h5Enabled, explicitAuthRequired bool) (bool, error) {
	if h5Enabled || explicitAuthRequired {
		return false, nil
	}
	url, err := weburl.Parse(request.URL)
	if err != nil {
		return false, err
	}
	path := url.Pathname()
	if !strings.HasPrefix(path, "/api/") && !isFilesystemCapabilityPath(path) && !strings.HasPrefix(path, "/proxy/") && !strings.HasPrefix(path, "/ws/") && !strings.HasPrefix(path, "/sdk/") {
		return false, nil
	}
	return classifyParsedH5Request(request, url, context) == RequestH5Browser, nil
}

func IsH5AccessControlPath(pathname string) bool {
	return strings.HasPrefix(pathname, "/api/h5-access") && pathname != "/api/h5-access/verify"
}

func IsLocalCredentialOnlyPath(pathname string) bool {
	parts := strings.Split(pathname, "/")
	var nonempty []string
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return "/"+strings.Join(nonempty, "/") == "/api/settings/session-cleanup"
}

func RequiresLocalAccessCredential(pathname string, context H5Context) bool {
	return context.LocalAccessTokenConfigured && (IsH5AccessControlPath(pathname) || IsLocalCredentialOnlyPath(pathname)) && !context.LocalAccessAuthorized
}
