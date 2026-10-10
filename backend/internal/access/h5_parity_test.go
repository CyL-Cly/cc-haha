package access

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	weburl "github.com/nlnwa/whatwg-url/url"
)

// TestH5ParityFixture is only a hermetic language-differential consumer. It
// cannot read tokens, config, the real user home, or any provider transport.
func TestH5ParityFixture(t *testing.T) {
	path := os.Getenv("CC_HAHA_H5_FIXTURE")
	if path == "" {
		t.Skip("only used by the TypeScript/Go H5 differential lane")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Requests []struct {
			Request Request   `json:"request"`
			Context H5Context `json:"context"`
		} `json:"requests"`
		Hosts           []string  `json:"hosts"`
		RendererOrigins []*string `json:"rendererOrigins"`
		Paths           []struct {
			Pathname string    `json:"pathname"`
			Context  H5Context `json:"context"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	requests := []any{}
	for _, entry := range fixture.Requests {
		kind, err := ClassifyH5Request(entry.Request, entry.Context)
		if err != nil {
			requests = append(requests, map[string]any{"error": true})
			continue
		}
		url, _ := weburl.Parse(entry.Request.URL)
		require := []bool{}
		block := []bool{}
		for _, enabled := range []bool{false, true} {
			value, err := ShouldRequireH5Token(entry.Request, entry.Context, enabled)
			if err != nil {
				t.Fatal(err)
			}
			require = append(require, value)
			for _, explicit := range []bool{false, true} {
				value, err := ShouldBlockDisabledH5Access(entry.Request, entry.Context, enabled, explicit)
				if err != nil {
					t.Fatal(err)
				}
				block = append(block, value)
			}
		}
		requests = append(requests, map[string]any{
			"kind": kind, "require": require, "block": block, "credential": RequiresLocalAccessCredential(url.Pathname(), entry.Context),
			"url": map[string]string{"href": url.Href(false), "hostname": url.Hostname(), "pathname": url.Pathname(), "origin": h5URLOrigin(url)},
		})
	}
	hosts := []any{}
	for _, host := range fixture.Hosts {
		hosts = append(hosts, map[string]any{"normalized": NormalizeHostname(host), "loopback": IsLoopbackHost(host)})
	}
	origins := []any{}
	for _, origin := range fixture.RendererOrigins {
		var value string
		if origin != nil {
			value = ResolveTrustedRendererOrigin(*origin)
		}
		if value == "" {
			origins = append(origins, nil)
		} else {
			origins = append(origins, value)
		}
	}
	paths := []any{}
	for _, entry := range fixture.Paths {
		paths = append(paths, map[string]bool{"control": IsH5AccessControlPath(entry.Pathname), "localOnly": IsLocalCredentialOnlyPath(entry.Pathname), "credential": RequiresLocalAccessCredential(entry.Pathname, entry.Context)})
	}
	output, err := json.Marshal(map[string]any{"requests": requests, "hosts": hosts, "rendererOrigins": origins, "paths": paths})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("GO_H5_RESULT:%s\n", output)
}
