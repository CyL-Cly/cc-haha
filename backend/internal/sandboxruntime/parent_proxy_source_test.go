package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceParentProxyURLsAuthenticationNoProxyAndNullishEnvironment(t *testing.T) {
	urls := []string{"", "proxy.test:8080", "HTTP://PROXY.test:80/", "https://proxy.test:443/path?q=1#hash", "socks5://proxy.test:80", "http://127.1", "http://[::1]:80", "http://例子.test:8080", "http://user:p%40ss@proxy.test:8080", "http://%FF:p%40ss@proxy.test", "http://%zz:p%40ss@proxy.test", "http://user:%@proxy.test", "http://:@proxy.test", "http://proxy.test:99999", "http://", "  proxy.test:8080  ", "http:proxy.test", "http://a\\b.test"}
	hosts := []string{"localhost", "LOCALHOST.", "127.2.3.4", "::ffff:127.9.8.7", "::1", "[::1]", "allowed.test", "sub.allowed.test", "x.notallowed.test", "127.1", "10.2.3.4", "2001:db8::1", "[2001:db8::1]", "host.test", "host.test."}
	rows := []map[string]any{}
	for _, u := range urls {
		for _, rules := range []string{"", "*", ".allowed.test,host.test:80,10.0.0.0/8,2001:db8::/32", "*.allowed.test,[2001:db8::1]:80,127.1", "10.0.0.0/-1,[2001:db8::1]:not-port,host.test:99999999999999999999999"} {
			for _, mode := range []int{0, 1, 2, 3} {
				env := map[string]string{"NO_PROXY": rules}
				switch mode {
				case 0:
					env["HTTP_PROXY"] = u
				case 1:
					env["HTTPS_PROXY"] = u
				case 2:
					env["HTTP_PROXY"] = ""
					env["http_proxy"] = u
					env["https_proxy"] = u
				case 3:
					env["http_proxy"] = u
					env["HTTPS_PROXY"] = ""
					env["no_proxy"] = "*"
				}
				rows = append(rows, map[string]any{"environment": env, "hosts": hosts})
			}
		}
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	fixture, _ := filepath.Abs("testdata/parent-proxy.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	type call struct {
		Host        string
		Bypass      *bool
		HTTP, HTTPS *string
	}
	type observation struct {
		HTTP, HTTPS, HTTPAuth, HTTPSAuth *string
		Calls                            []call
	}
	var source []observation
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	ptr := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	for i, row := range rows {
		proxy := parseParentProxy(row["environment"].(map[string]string))
		actual := observation{Calls: []call{}}
		if proxy != nil {
			if proxy.http != nil {
				actual.HTTP = ptr(proxy.http.String())
				actual.HTTPAuth = ptr(proxyAuth(proxy.http))
			}
			if proxy.https != nil {
				actual.HTTPS = ptr(proxy.https.String())
				actual.HTTPSAuth = ptr(proxyAuth(proxy.https))
			}
		}
		for _, host := range hosts {
			c := call{Host: host}
			if proxy != nil {
				bypass := proxy.bypass(host)
				c.Bypass = &bypass
				if proxy.http != nil {
					c.HTTP = ptr(proxy.http.String())
				}
				selected := proxy.https
				if selected == nil {
					selected = proxy.http
				}
				if selected != nil {
					c.HTTPS = ptr(selected.String())
				}
			}
			actual.Calls = append(actual.Calls, c)
		}
		if !reflect.DeepEqual(actual, source[i]) {
			t.Fatalf("actual Source parent proxy row%d env%v\nSource%+v\nGo%+v", i, row["environment"], source[i], actual)
		}
	}
}
