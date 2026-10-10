package access

import (
	"net/http"
	"testing"
)

func h5TestRequest(path string, headers map[string]string) Request {
	request := Request{Method: "GET", URL: "http://127.0.0.1:3456" + path, Headers: make(http.Header)}
	for name, value := range headers {
		request.Headers.Set(name, value)
	}
	return request
}

func TestH5TrustGatesPreserveCredentialHostOriginAndSDKBoundaries(t *testing.T) {
	local := H5Context{ClientAddress: "127.0.0.1"}
	desktop := H5Context{ClientAddress: "127.0.0.1", LocalAccessTokenConfigured: true}
	for _, test := range []struct {
		name    string
		request Request
		context H5Context
		kind    RequestKind
		require bool
		block   bool
	}{
		{"loopback navigation", h5TestRequest("/api/status", nil), desktop, RequestLocalTrusted, false, false},
		{"no connection address", h5TestRequest("/api/status", nil), H5Context{}, RequestH5Browser, true, true},
		{"remote spoofed target", h5TestRequest("/api/status", nil), H5Context{ClientAddress: "192.168.0.4"}, RequestH5Browser, true, true},
		{"public host from loopback", Request{URL: "https://public.example/api/status"}, local, RequestH5Browser, true, true},
		{"mapped IPv4 peer", h5TestRequest("/api/status", nil), H5Context{ClientAddress: "::ffff:127.0.0.1"}, RequestLocalTrusted, false, false},
		{"retired Tauri origin", h5TestRequest("/api/status", map[string]string{"Origin": "http://tauri.localhost"}), local, RequestH5Browser, true, true},
		{"desktop file origin", h5TestRequest("/api/status", map[string]string{"Origin": "file://"}), desktop, RequestLocalTrusted, false, false},
		{"tokenless development", h5TestRequest("/api/status", map[string]string{"Origin": "http://localhost:5173"}), local, RequestLocalTrusted, false, false},
		{"configured desktop origin needs token", h5TestRequest("/api/status", map[string]string{"Origin": "http://localhost:5173"}), desktop, RequestH5Browser, true, true},
		{"cross-site image", h5TestRequest("/api/status", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}), local, RequestH5Browser, true, true},
		{"cross-site absent mode legacy navigation", h5TestRequest("/api/status", map[string]string{"Sec-Fetch-Site": "cross-site"}), local, RequestLocalTrusted, false, false},
		{"cross-site empty mode is present", h5TestRequest("/api/status", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": ""}), local, RequestH5Browser, true, true},
		{"cross-site top-level navigation", h5TestRequest("/api/status", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}), local, RequestLocalTrusted, false, false},
		{"remote origin", h5TestRequest("/api/status", map[string]string{"Origin": "https://phone.example"}), local, RequestH5Browser, true, true},
		{"invalid origin", h5TestRequest("/api/status", map[string]string{"Origin": "not a URL"}), local, RequestH5Browser, true, true},
		{"non-web loopback origin", h5TestRequest("/api/status", map[string]string{"Origin": "ftp://localhost"}), local, RequestH5Browser, true, true},
		{"internal SDK loopback", h5TestRequest("/sdk/session", nil), local, RequestInternalSDK, false, false},
		{"remote SDK unauthenticated", h5TestRequest("/sdk/session", nil), H5Context{ClientAddress: "10.0.0.4"}, RequestH5Browser, false, true},
		{"SDK session token", h5TestRequest("/sdk/session", map[string]string{"Via": "proxy"}), H5Context{InternalSDKAuthorized: true}, RequestInternalSDK, false, false},
		{"SDK token cannot unlock filesystem", h5TestRequest("/preview-fs/session/index.html", nil), H5Context{InternalSDKAuthorized: true}, RequestH5Browser, true, true},
		{"bootstrap remains reachable", h5TestRequest("/assets/app.js", nil), H5Context{}, RequestH5Browser, false, false},
		{"process credential is strongest", Request{Method: "POST", URL: "https://public.example/api/status", Headers: http.Header{"Origin": {"https://phone.example"}, "Via": {""}}}, H5Context{LocalAccessAuthorized: true}, RequestLocalTrusted, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind, err := ClassifyH5Request(test.request, test.context)
			if err != nil || kind != test.kind {
				t.Fatalf("request trust changed: %s, %v; want %s", kind, err, test.kind)
			}
			require, err := ShouldRequireH5Token(test.request, test.context, true)
			if err != nil || require != test.require {
				t.Fatalf("enabled token boundary changed: %v, %v", require, err)
			}
			block, err := ShouldBlockDisabledH5Access(test.request, test.context, false, false)
			if err != nil || block != test.block {
				t.Fatalf("disabled capability boundary changed: %v, %v", block, err)
			}
			if value, err := ShouldRequireH5Token(test.request, test.context, false); err != nil || value {
				t.Fatalf("disabled H5 added a token gate: %v, %v", value, err)
			}
			for _, enabled := range []bool{false, true} {
				if value, err := ShouldBlockDisabledH5Access(test.request, test.context, enabled, true); err != nil || value {
					t.Fatalf("H5 gate bypassed explicit deployment authentication: %v, %v", value, err)
				}
			}
		})
	}
}

func TestH5RendererPreflightIsExactAndProxyPresenceAlwaysCounts(t *testing.T) {
	context := H5Context{ClientAddress: "127.0.0.1", LocalAccessTokenConfigured: true, TrustedRendererOrigin: "http://localhost:1420"}
	request := h5TestRequest("/api/h5-access/enable", map[string]string{"Origin": context.TrustedRendererOrigin, "Access-Control-Request-Method": ""})
	request.Method = "options"
	if kind, err := ClassifyH5Request(request, context); err != nil || kind != RequestLocalTrusted {
		t.Fatalf("exact renderer preflight became inaccessible: %s, %v", kind, err)
	}
	request.Headers.Del("Access-Control-Request-Method")
	if kind, _ := ClassifyH5Request(request, context); kind != RequestH5Browser {
		t.Fatal("ordinary renderer request bypassed process credential")
	}
	for _, name := range proxyTraceHeaders {
		request := h5TestRequest("/api/status", map[string]string{name: ""})
		if kind, _ := ClassifyH5Request(request, H5Context{ClientAddress: "127.0.0.1"}); kind != RequestH5Browser {
			t.Fatalf("empty %s header did not count as proxy evidence", name)
		}
	}
	duplicate := h5TestRequest("/api/status", nil)
	duplicate.Headers = http.Header{"origin": {"file://", "https://attacker.example"}}
	if kind, _ := ClassifyH5Request(duplicate, context); kind != RequestH5Browser {
		t.Fatal("duplicate origin was reduced to its first local value")
	}
}

func TestH5PassiveAssetsStayWithinRefererCapabilityAndOneDependencyParent(t *testing.T) {
	context := H5Context{ClientAddress: "192.168.0.4", LocalAccessTokenConfigured: true}
	base := "http://127.0.0.1:3456"
	for _, test := range []struct {
		path, referer, origin, method, destination string
		trusted                                    bool
	}{
		{"/preview-fs/s/site/assets/app.js", "/preview-fs/s/site/index.html", base, "GET", "script", true},
		{"/local-file/Users/a/site/assets/app.js", "/local-file/Users/a/site/index.html", "", "HEAD", "style", true},
		{"/preview-fs/s/site/font.woff", "/preview-fs/s/site/assets/app.MJS", "", "GET", "font", true},
		{"/preview-fs/s/site/font.woff", "/preview-fs/s/site/assets/app.css", "", "GET", "font", true},
		{"/preview-fs/s/site/font.woff", "/preview-fs/s/site/assets/app.cjs", "", "GET", "font", true},
		{"/preview-fs/s/site/font.woff", "/preview-fs/s/site/assets/app.js", "blob:" + base + "/fixture", "GET", "font", true},
		{"/preview-fs/s/site/font.woff", "/preview-fs/s/site/assets/app.js", "blob:invalid", "GET", "font", false},
		{"/preview-fs/s/site/a.js", "", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "invalid", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "https://attacker.example/preview-fs/s/site/index.html", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s/site/index.html", "invalid", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s/site/index.html", "https://attacker.example", "GET", "script", false},
		{"/preview-fs/other/site/a.js", "/preview-fs/s/site/index.html", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "/local-file/Users/a/site/index.html", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s", "", "GET", "script", false},
		{"/preview-fs/s", "/preview-fs/s/site/index.html", base, "GET", "script", false},
		{"/preview-fs/s/secret.js", "/preview-fs/s/site/index.html", "", "GET", "script", false},
		{"/preview-fs/s/secret.js", "/preview-fs/s/site/index", "", "GET", "script", false},
		{"/preview-fs/s/secret.js", "/preview-fs/s/site/assets/app.js", "", "GET", "script", false},
		{"/preview-fs/other/secret.js", "/preview-fs/s/app.js", "", "GET", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s/site/index.html", "", "POST", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s/site/index.html", "", "PATCH", "script", false},
		{"/preview-fs/s/site/a.js", "/preview-fs/s/site/index.html", "", "GET", "empty", false},
		{"/api/status", "/preview-fs/s/site/index.html", base, "GET", "empty", false},
	} {
		request := h5TestRequest(test.path, map[string]string{"Origin": test.origin, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": test.destination})
		request.Method = test.method
		if test.referer != "" {
			referer := test.referer
			if referer[0] == '/' {
				referer = base + referer
			}
			request.Headers.Set("Referer", referer)
		}
		request.Headers.Set("Via", "") // Passive asset exception precedes proxy/local-host checks.
		kind, err := ClassifyH5Request(request, context)
		if err != nil || (kind == RequestLocalTrusted) != test.trusted {
			t.Fatalf("passive capability boundary changed for %s <- %s: %s, %v", test.path, test.referer, kind, err)
		}
	}
	for _, destination := range []string{"audio", "font", "image", "manifest", "script", "style", "track", "video", "worker"} {
		request := h5TestRequest("/preview-fs/s/site/a", map[string]string{"Referer": base + "/preview-fs/s/site/index.html", "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": destination})
		if kind, _ := ClassifyH5Request(request, context); kind != RequestLocalTrusted {
			t.Fatalf("legacy passive destination %s rejected", destination)
		}
	}
}

func TestH5URLAndHostCanonicalizationPreservesWHATWGBoundary(t *testing.T) {
	if normalized := NormalizeHostname("İΣ"); normalized != "i̇ς" {
		t.Fatalf("JS full Unicode hostname lowercase changed: %q", normalized)
	}
	for _, test := range []struct {
		host string
		want bool
	}{
		{"localhost", true}, {"\ufeff[::1]\ufeff", true}, {"127.000.0.001", true}, {"127.0.0.256", false},
		{"127.bad.0.1", false}, {"127.1", false}, {"127.example.com", false}, {"[::ffff:127.0.0.1]", true},
		{"::ffff:7f00:1", false}, {"\u0085localhost\u0085", false}, {"localhost.", false},
	} {
		if value := IsLoopbackHost(test.host); value != test.want {
			t.Fatalf("host %q trust changed: %v", test.host, value)
		}
	}
	for _, test := range []struct{ input, want string }{
		{"http://localhost:1420/app?x=1#f", "http://localhost:1420"}, {"https://127.1:443/path", "https://127.0.0.1"},
		{"http://[::1]:80/", "http://[::1]"}, {"http://%6cocalhost:80/", "http://localhost"},
		{"", ""}, {"invalid", ""}, {"file://", ""}, {"https://remote.example", ""}, {"http://user:pass@localhost:1420", ""},
	} {
		if value := ResolveTrustedRendererOrigin(test.input); value != test.want {
			t.Fatalf("renderer origin %q changed: %q; want %q", test.input, value, test.want)
		}
	}
	for _, url := range []string{"http://127.1/api/status", "http://0x7f000001/api/status", "http://2130706433/api/status"} {
		if kind, err := ClassifyH5Request(Request{URL: url}, H5Context{ClientAddress: "127.0.0.1"}); err != nil || kind != RequestLocalTrusted {
			t.Fatalf("WHATWG IPv4 canonicalization changed: %s, %v", kind, err)
		}
	}
	invalid := Request{URL: "not a URL"}
	if _, err := ClassifyH5Request(invalid, H5Context{}); err == nil {
		t.Fatal("invalid URL classified")
	}
	if _, err := ShouldRequireH5Token(invalid, H5Context{}, true); err == nil {
		t.Fatal("invalid enabled-gate URL accepted")
	}
	if _, err := ShouldBlockDisabledH5Access(invalid, H5Context{}, false, false); err == nil {
		t.Fatal("invalid disabled-gate URL accepted")
	}
}

func TestH5ControlAndBulkDeletePathsKeepDistinctCredentialRules(t *testing.T) {
	for _, pathname := range []string{"/api/h5-access", "/api/h5-access/enable", "/api/h5-access/verify/", "/api/settings/session-cleanup", "/api/settings/session-cleanup/", "//api/settings//session-cleanup//"} {
		if !RequiresLocalAccessCredential(pathname, H5Context{LocalAccessTokenConfigured: true}) {
			t.Fatalf("privileged path %s lost its process-token gate", pathname)
		}
		if RequiresLocalAccessCredential(pathname, H5Context{}) || RequiresLocalAccessCredential(pathname, H5Context{LocalAccessTokenConfigured: true, LocalAccessAuthorized: true}) {
			t.Fatalf("tokenless or authorized components locked out at %s", pathname)
		}
	}
	for _, pathname := range []string{"/api/h5-access/verify", "//api/h5-access/enable", "/api/settings/user", "/api/settings/session-cleanup-extra", "/api/settings%2fsession-cleanup"} {
		if RequiresLocalAccessCredential(pathname, H5Context{LocalAccessTokenConfigured: true}) {
			t.Fatalf("credential policy silently expanded to %s", pathname)
		}
	}
}
