package access

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCORSOriginsAndReferenceURLParsing(t *testing.T) {
	for _, origin := range []string{"", "file://", "http://localhost:3000", "http://127.0.1.1:2024", "http://[::1]:5173", "http://127.1", "http://0x7f000001", "http://0177.0.0.1", "http://[0:0:0:0:0:0:0:1]"} {
		calls := 0
		result, err := ResolveCORS(context.Background(), origin, CORSOptions{H5Enabled: true, IsOriginAllowed: func(context.Context, string) (bool, error) { calls++; return false, nil }})
		if err != nil || !result.Allowed || result.Rejected || calls != 0 || !reflect.DeepEqual(result.Headers, CORSHeaders(origin)) {
			t.Fatalf("origin=%q result=%+v calls=%d error=%v", origin, result, calls, err)
		}
	}
	for _, origin := range []string{"http://192.168.0.20:5173", "https://10.0.0.5", "http://127.example.com", "http://127.bad.0.1", "not-a-url", "http://tauri.localhost", "https://tauri.localhost", "tauri://localhost", "http://[::ffff:127.0.0.1]", "http://127.0.0.999", "http://localhost."} {
		result, err := ResolveCORS(context.Background(), origin, CORSOptions{H5Enabled: true})
		if err != nil || result.Allowed || !result.Rejected || !reflect.DeepEqual(result.Headers, baseCORSHeaders()) {
			t.Fatalf("origin=%q result=%+v error=%v", origin, result, err)
		}
	}
	if CORSHeaders("")["Access-Control-Allow-Origin"] != "http://localhost:3000" {
		t.Fatal("missing origin default changed")
	}
}

func TestCORSH5CallbackAndInactiveMode(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		result, err := ResolveCORS(context.Background(), "https://phone.example", CORSOptions{H5Enabled: true, IsOriginAllowed: func(_ context.Context, origin string) (bool, error) {
			if origin != "https://phone.example" {
				t.Fatal("callback received normalized origin")
			}
			return allowed, nil
		}})
		if err != nil || result.Allowed != allowed || result.Rejected == allowed {
			t.Fatal(result, err)
		}
	}
	result, err := ResolveCORS(context.Background(), "https://arbitrary.example", CORSOptions{IsOriginAllowed: func(context.Context, string) (bool, error) {
		t.Fatal("inactive mode read H5 settings")
		return false, nil
	}})
	if err != nil || !result.Allowed || result.Headers["Access-Control-Allow-Origin"] != "https://arbitrary.example" {
		t.Fatal(result, err)
	}
	want := errors.New("settings unavailable")
	if _, err := ResolveCORS(context.Background(), "https://phone.example", CORSOptions{H5Enabled: true, IsOriginAllowed: func(context.Context, string) (bool, error) { return false, want }}); !errors.Is(err, want) {
		t.Fatal("callback failure was suppressed")
	}
}

func TestIPv4HostLexicalRules(t *testing.T) {
	for _, hostname := range []string{"127.0.0.1", "127.00.1.255"} {
		if !isLoopbackIPv4(hostname) {
			t.Fatal(hostname)
		}
	}
	for _, hostname := range []string{"127.1", "127..0.1", "127.x.0.1", "127.256.0.1", "126.0.0.1", "127.999999999999999999999999999999.0.1"} {
		if isLoopbackIPv4(hostname) {
			t.Fatal(hostname)
		}
	}
}
