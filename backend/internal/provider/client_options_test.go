package provider

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinalFetchHeadersCloneAndEmptyInitialHeaders(t *testing.T) {
	ids := 0
	options := ResolveDirectClientOptions(DirectClientInput{RequestID: func() string { ids++; return "fixture-id" }})
	got, err := options.finalFetchHeaders("https://api.anthropic.com/v1/messages", nil)
	if err != nil || got.Get("X-Client-Request-Id") != "fixture-id" || ids != 1 {
		t.Fatal("missing init headers did not produce first-party ID", got, err, ids)
	}
	headers := http.Header{"x-client-request-id": {""}, "X-Future": {"before"}}
	got, err = options.finalFetchHeaders("https://api.anthropic.com/v1/messages", headers)
	if err != nil || ids != 1 {
		t.Fatal("present empty header allocated another ID", got, err, ids)
	}
	got.Set("X-Future", "after")
	if headers.Get("X-Future") != "before" {
		t.Fatal("final fetch mutated caller header values")
	}
}

func TestDirectClientMatchesActualSourceContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/client_options_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		directClientFixtureInput
		Results         []json.RawMessage
		ResolverResults []json.RawMessage
	}
	if err := json.Unmarshal(raw, &contract); err != nil || len(contract.Steps) == 0 || len(contract.Steps) != len(contract.Results) {
		t.Fatalf("invalid real-source contract: %v", err)
	}
	for index, step := range contract.Steps {
		t.Run(step.Name, func(t *testing.T) {
			observed := observeDirectClientOptions(t, directClientFixtureInput{Runtime: contract.Runtime, Steps: []directClientFixtureStep{step}})[0]
			actualRaw, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			_ = json.Unmarshal(actualRaw, &actual)
			_ = json.Unmarshal(contract.Results[index], &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("source construction/request changed:\nactual %s\nsource %s", actualRaw, contract.Results[index])
			}
		})
	}
	if len(contract.Resolvers) == 0 || len(contract.Resolvers) != len(contract.ResolverResults) {
		t.Fatal("real-source resolver contract is missing")
	}
	for index, resolver := range contract.Resolvers {
		t.Run(resolver.Name, func(t *testing.T) {
			actualRaw, _ := json.Marshal(observeClientResolvers([]clientResolverFixture{resolver})[0])
			var actual, expected any
			_ = json.Unmarshal(actualRaw, &actual)
			_ = json.Unmarshal(contract.ResolverResults[index], &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("final-fetch credential boundary changed:\nactual %s\nsource %s", actualRaw, contract.ResolverResults[index])
			}
		})
	}
}

func TestDefaultRequestIDHasUUIDv4ShapeAndIsFresh(t *testing.T) {
	first, second := randomRequestID(), randomRequestID()
	for _, value := range []string{first, second} {
		decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
		if err != nil || len(decoded) != 16 || len(value) != 36 || decoded[6]>>4 != 4 || decoded[8]>>6 != 2 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
			t.Fatalf("request ID changed UUID encoding/version/variant: %q, %v", value, err)
		}
	}
	if first == second {
		t.Fatal("different requests reused the default ID")
	}
}

func TestClientCredentialResolutionDoesNotAliasFallbackOrExplicit(t *testing.T) {
	key, empty := "fixture", ""
	for _, fixture := range []struct {
		explicit, fallback *string
		envToken, envKey   string
		want               *string
	}{
		{nil, &key, "token", "", nil},
		{&empty, &key, "token", "", nil},
		{&key, nil, "token", "", &key},
		{nil, &key, "token", "env-key", &key},
		{nil, nil, "", "", nil},
		{nil, &empty, "", "", &empty},
	} {
		before := key
		got := ResolveAnthropicClientAPIKey(fixture.explicit, fixture.envToken, fixture.envKey, fixture.fallback)
		if !reflect.DeepEqual(got, fixture.want) {
			t.Fatalf("token-only/empty-key selection changed: %#v", got)
		}
		if got != nil {
			*got = "consumer mutation"
			if key != before || empty != "" {
				t.Fatal("consumer mutated original credentials")
			}
		}
	}
}

func TestClientPlatformWireIdentityIsSourceProfile(t *testing.T) {
	for _, fixture := range []struct{ input, want string }{
		{"darwin", "MacOS"}, {"LINUX", "Linux"}, {"win32", "Windows"}, {"android", "Android"},
		{"freebsd", "FreeBSD"}, {"openbsd", "OpenBSD"}, {"mobile-ios", "iOS"}, {"", "Unknown"}, {"aix", "Other:aix"},
	} {
		if got := sourceOS(fixture.input); got != fixture.want {
			t.Errorf("source OS identity %q = %q, want %q", fixture.input, got, fixture.want)
		}
	}
	for _, fixture := range []struct{ input, want string }{
		{"x32", "x32"}, {"x64", "x64"}, {"x86_64", "x64"}, {"aarch64", "arm64"},
		{"arm", "arm"}, {"arm64", "arm64"}, {"", "unknown"}, {"ia32", "other:ia32"},
	} {
		if got := sourceArch(fixture.input); got != fixture.want {
			t.Errorf("source arch identity %q = %q, want %q", fixture.input, got, fixture.want)
		}
	}
	options := ResolveDirectClientOptions(DirectClientInput{Runtime: SourceRuntime{Platform: "linux", Arch: "x64", Version: "source-version"}})
	if options.BaseURL != "https://api.anthropic.com" || options.StreamConfig().DefaultHeaders.Get("X-Stainless-Runtime-Version") != "source-version" {
		t.Fatal("construction silently selected Go runtime metadata")
	}
}

func TestClientTimeoutValidationIsDeferredUntilBetaRequest(t *testing.T) {
	for _, fixture := range []struct {
		value string
		want  float64
	}{
		{"", 600000}, {"-0", math.Copysign(0, -1)}, {"  +42.9ms", 42}, {"0x20", 0},
		{"\ufeff123ms\u2028", 123}, {"\u0085123", math.NaN()}, {"-1", -1}, {"1e3", 1},
	} {
		options := ResolveDirectClientOptions(DirectClientInput{Env: map[string]string{"API_TIMEOUT_MS": fixture.value}})
		if math.IsNaN(fixture.want) {
			if !math.IsNaN(options.TimeoutMillis) {
				t.Fatal("malformed constructor timeout became a default")
			}
		} else if options.TimeoutMillis != fixture.want || math.Signbit(options.TimeoutMillis) != math.Signbit(fixture.want) {
			t.Fatalf("parseInt(%q) = %v, want %v", fixture.value, options.TimeoutMillis, fixture.want)
		}
		client := NewAnthropic(options.StreamConfig())
		duration, err := client.requestTimeout()
		if math.IsNaN(fixture.want) || fixture.want < 0 {
			if err == nil {
				t.Fatalf("invalid beta request timeout accepted: %v", fixture.want)
			}
		} else if err != nil || fixture.want > 0 && duration != time.Duration(fixture.want)*time.Millisecond || fixture.want == 0 && duration != time.Millisecond {
			t.Fatalf("beta request timer changed: %v, %v", duration, err)
		}
	}
}
