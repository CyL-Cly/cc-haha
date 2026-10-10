package h5access

import (
	"encoding/json"
	"reflect"
	"testing"
)

func ptr(value string) *string { return &value }
func sameString(actual, expected *string) bool {
	return actual == nil && expected == nil || actual != nil && expected != nil && *actual == *expected
}

func TestLANSelectionRetainsSourceScoringAndStableTies(t *testing.T) {
	interfaces := []NetworkInterface{
		{Name: "Docker", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "192.168.2.2"}}},
		{Name: "Wi-Fi", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "10.0.0.2"}, {Family: "IPv4", Address: "10.0.0.3"}}},
		{Name: "Ethernet", Addresses: []InterfaceAddress{{Family: "IPv6", Address: "::1"}, {Family: "IPv4", Internal: true, Address: "192.168.1.1"}, {Family: "IPv4", Address: "169.254.1.2"}}},
		{Name: "vpn", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "172.16.1.2"}}},
		{Name: "lo0", Addresses: []InterfaceAddress{{Family: "IPv4", Internal: true, Address: "127.0.0.1"}}},
		{Name: "unknown", Addresses: []InterfaceAddress{{Family: "IPv4", Address: "8.8.8.8"}, {Family: "IPv4", Address: "invalid"}}},
	}
	if got := FindPrivateLanAddress(interfaces); !sameString(got, ptr("10.0.0.2")) {
		t.Fatal(got)
	}
	if hosts := CollectLocalIPv4Hosts(interfaces); !reflect.DeepEqual(hosts, []string{"192.168.2.2", "10.0.0.2", "10.0.0.3", "169.254.1.2", "172.16.1.2", "8.8.8.8", "invalid"}) {
		t.Fatal(hosts)
	}
	if FindPrivateLanAddress(nil) != nil || preferredLanHost(nil) != nil || len(CollectLocalIPv4Hosts(nil)) != 0 {
		t.Fatal("empty interfaces invented host")
	}
	for _, fixture := range []struct {
		hosts []string
		want  string
	}{
		{[]string{"10.0.0.2", "192.168.1.1"}, "192.168.1.1"}, {[]string{"172.16.0.1", "10.0.0.2"}, "10.0.0.2"},
		{[]string{"8.8.8.8", "172.31.0.1"}, "172.31.0.1"}, {[]string{"8.8.8.8", "169.254.0.1"}, "8.8.8.8"},
	} {
		if got := preferredLanHost(fixture.hosts); !sameString(got, ptr(fixture.want)) {
			t.Fatal(got, fixture)
		}
	}
	for _, fixture := range []struct {
		address string
		private bool
	}{
		{"10.999.888.777", true}, {"010.0.0.1", true}, {"172.16.0.1", true}, {"172.31.0.1", true}, {"172.15.0.1", false}, {"172.32.0.1", false},
		{"192.168.0.1", true}, {"169.254.0.1", true}, {"127.0.0.1", false}, {"1.2.3", false}, {"1.-2.3.4", false}, {"1.2.3.a", false},
	} {
		if got := isPrivateIPv4(fixture.address); got != fixture.private {
			t.Fatalf("%s=%v", fixture.address, got)
		}
	}
}

func TestPublicURLClassificationValidationAndEffectivePrecedence(t *testing.T) {
	for _, fixture := range []struct{ url, kind string }{
		{"bad", "invalid"}, {"file:///path", "invalid"}, {"http://user@192.168.1.1", "invalid"}, {"http://192.168.1.1:3000////", "plain-lan"},
		{"http://0xc0a80101:3000", "plain-lan"}, {"https://192.168.1.1:3000", "proxy"}, {"http://192.168.1.1/path", "proxy"}, {"http://server.test:3000", "proxy"},
	} {
		if got := ClassifyPublicBaseURL(fixture.url); got != fixture.kind {
			t.Fatalf("%s=%s", fixture.url, got)
		}
	}
	for _, fixture := range []struct {
		url          string
		hosts        []string
		ok           bool
		kind, reason string
		suggested    *string
	}{
		{"http://192.168.1.1:3000", []string{"192.168.1.1"}, true, "plain-lan", "", nil}, {"https://proxy.test/path", nil, true, "proxy", "", nil},
		{"invalid", []string{"10.0.0.2"}, false, "", "Invalid H5 publicBaseUrl: invalid", ptr("10.0.0.2")},
		{"http://192.168.1.1", nil, false, "", "H5 host 192.168.1.1 is not bound to any local network interface on this machine. Available LAN IPv4: none", nil},
	} {
		got := ValidatePublicBaseURL(fixture.url, fixture.hosts)
		if got.OK != fixture.ok || got.Kind != fixture.kind || got.Reason != fixture.reason || !sameString(got.SuggestedHost, fixture.suggested) {
			t.Fatalf("got=%+v fixture=%+v", got, fixture)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]json.RawMessage
		_ = json.Unmarshal(raw, &object)
		_, hasSuggested := object["suggestedHost"]
		if hasSuggested == fixture.ok {
			t.Fatalf("rejected union key omitted or success has rejection key: %s", raw)
		}
	}
	for _, fixture := range []struct {
		input EffectiveURLInput
		want  *string
	}{
		{EffectiveURLInput{StoredPublicBaseURL: ptr("http://stored"), ConfiguredPublicBaseURL: ptr("http://configured"), AutoPublicBaseURL: ptr("http://auto")}, ptr("http://stored")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://stored"), ConfiguredPublicBaseURL: ptr("http://configured"), AutoPublicBaseURL: ptr("http://auto")}, ptr("http://configured")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://stored")}, ptr("http://stored")},
		{EffectiveURLInput{Enabled: true, AutoPublicBaseURL: ptr("http://10.0.0.1:3000")}, ptr("http://10.0.0.1:3000")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://localhost:9999"), AutoPublicBaseURL: ptr("http://10.0.0.1:3000")}, ptr("http://10.0.0.1:3000")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://192.168.1.1:9999"), AutoPublicBaseURL: ptr("http://10.0.0.1:3000"), LocalInterfaceHosts: []string{}}, ptr("http://10.0.0.1:3000")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://192.168.1.1:9999"), AutoPublicBaseURL: ptr("http://10.0.0.1:3000")}, ptr("http://192.168.1.1:3000")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("http://192.168.1.1:9999"), AutoPublicBaseURL: ptr("http://10.0.0.1:80")}, ptr("http://192.168.1.1:9999")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("https://proxy.test/path"), AutoPublicBaseURL: ptr("http://10.0.0.1:3000"), LocalInterfaceHosts: []string{}}, ptr("https://proxy.test/path")},
		{EffectiveURLInput{Enabled: true, StoredPublicBaseURL: ptr("invalid"), AutoPublicBaseURL: ptr("invalid")}, ptr("invalid")},
	} {
		if got := ResolveEffectivePublicBaseURL(fixture.input); !sameString(got, fixture.want) {
			t.Fatalf("input=%+v got=%v want=%v", fixture.input, got, fixture.want)
		}
	}
	for _, url := range []string{"http://[::1]", "http://[::]", "http://0.0.0.0", "http://127.0.0.1"} {
		if !isLocalPublicBaseURL(url) {
			t.Fatal(url)
		}
	}
}
