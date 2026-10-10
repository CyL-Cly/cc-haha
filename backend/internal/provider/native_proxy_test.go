package provider

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestNativeProxySelectionPreservesSourceOrderAndBypassRules(t *testing.T) {
	env := map[string]string{"https_proxy": "http://lower-https.invalid", "HTTPS_PROXY": "http://upper-https.invalid", "http_proxy": "http://lower-http.invalid", "HTTP_PROXY": "http://upper-http.invalid", "no_proxy": "127.0.0.1", "NO_PROXY": "elsewhere.invalid"}
	before := map[string]string{}
	for k, v := range env {
		before[k] = v
	}
	got := ResolveNativeProxySelection("http://127.0.0.1:123/messages", env)
	if got.ProxyURL != "http://lower-https.invalid" || !got.Bypass {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatal("original env mutated")
	}
	delete(env, "https_proxy")
	if ResolveNativeProxySelection("http://127.0.0.1", env).ProxyURL != "http://upper-https.invalid" {
		t.Fatal("Source HTTPS priority changed for HTTP endpoint")
	}
	if _, e := NativeDirectHTTPExchangeFromEnvironment("http://127.0.0.1", env); e != nil {
		t.Fatal(e)
	}
	if transport, e := NativeDirectHTTPExchangeFromEnvironment("http://elsewhere.invalid", env); e != nil || transport.ProxyURL != "http://upper-https.invalid" {
		t.Fatal("selected active proxy lost its explicit transport", transport, e)
	}
	for _, row := range []struct {
		Endpoint, Pattern string
		Want              bool
	}{
		{"http://127.0.0.1:12", "127.0.0.1", true},
		{"https://example.com/path", ".example.com", true},
		{"https://sub.example.com", ".example.com", true},
		{"https://notexample.com", ".example.com", false},
		{"https://example.com", "example.com:443", true},
		{"https://example.com:444", "example.com:443", false},
		{"http://[::1]:8080", "::1", true},
		{"http://[::1]:8080", "[::1]:8080", true},
		{"http://[::1]:8080", "[::1]:80", false},
		{"http://example.com", "*", true},
		{"http://example.com", "localhost,*", false},
		{"http://example.com", "elsewhere\ufeffexample.com", true},
	} {
		if got := nativeShouldBypassProxy(row.Endpoint, row.Pattern); got != row.Want {
			t.Fatalf("Source bypass %s %s: %v", row.Endpoint, row.Pattern, got)
		}
	}
}

func TestAbsentSDKBaseURLDoesNotApplyTargetAwareNoProxy(t *testing.T) {
	// OAuth spreads the SDK's fetch options, built before its URL remapping.
	// getProxyFetchOptions({ targetUrl: undefined }) keeps the explicit proxy
	// even with NO_PROXY=*, unlike shouldBypassProxy called with a real target.
	for _, pattern := range []string{"*", "chatgpt.com", ".com"} {
		env := map[string]string{"HTTPS_PROXY": "http://127.0.0.1:43210", "NO_PROXY": pattern}
		transport, err := NativeDirectHTTPExchangeFromEnvironment("", env)
		if err != nil || transport.ProxyURL != env["HTTPS_PROXY"] {
			t.Fatalf("absent target bypassed the original explicit proxy: %+v %v", transport, err)
		}
	}
}

func TestNativeProxyResolverMatchesActualSourceFetchOptions(t *testing.T) {
	raw, e := os.ReadFile("testdata/native_proxy_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Observations []struct {
			Scenario struct {
				Name, Endpoint string
				Env            map[string]string
			}
			Observation struct {
				Proxy, FetchProxy string
				Bypass            bool
			}
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if e = json.Unmarshal(raw, &corpus); e != nil {
		t.Fatal(e)
	}
	if len(corpus.Observations) != 16 || len(corpus.SourceOnly.Capabilities.Blocked) != 0 || corpus.SourceOnly.Consumer != "src/utils/proxy.ts getProxyFetchOptions" {
		t.Fatal("missing actual guarded Source capture")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			selection := ResolveNativeProxySelection(row.Scenario.Endpoint, row.Scenario.Env)
			if selection.ProxyURL != row.Observation.Proxy || selection.Bypass != row.Observation.Bypass {
				t.Fatalf("actual Source proxy resolution mismatch: %+v vs %+v", selection, row.Observation)
			}
			transport, e := NativeDirectHTTPExchangeFromEnvironment(row.Scenario.Endpoint, row.Scenario.Env)
			if e != nil || transport.ProxyURL != row.Observation.FetchProxy {
				t.Fatalf("native direct dispatch ignored Source selected proxy: %v Source %+v", e, row.Observation)
			}
		})
	}
}
