package provider

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestBunHeaderClassificationAndOriginalVectors(t *testing.T) {
	if len(bunCommonHeaderNames) != 94 || !bunCommonHeaderNames["strict-transport-security"] || bunCommonHeaderNames["retry-after"] || bunCommonHeaderNames["server"] {
		t.Fatal("pinned generated classification changed")
	}
	pairs := []HeaderPair{{"X-Z", "first"}, {"CONTENT-TYPE", "first"}, {"2", "two"}, {"Set-Cookie", "a=1"}, {"Cookie", "a"}, {"X-Z", "last"}, {"Content-Type", "last"}, {"COOKIE", "b"}, {"SET-cookie", "b=2"}, {"X-empty", ""}, {"1", "one"}}
	constructor, err := NewConstructorHeaders(pairs)
	if err != nil {
		t.Fatal(err)
	}
	fetch, err := NewFetchHeaders(pairs)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		headers  *BunHeaders
		expected string
	}{
		{constructor, `{"1":"one","2":"two","content-type":"first, last","cookie":"a; b","set-cookie":["a=1","b=2"],"x-z":"first, last","x-empty":""}`},
		{fetch, `{"1":"one","2":"two","content-type":"first, last","cookie":"a; b","set-cookie":["a=1","b=2"],"x-z":"last"}`},
	} {
		raw, err := item.headers.MarshalJSON()
		if err != nil || string(raw) != item.expected {
			t.Fatalf("original vectors: %s %v", raw, err)
		}
	}
	if value, ok, err := constructor.Get("SET-COOKIE"); err != nil || !ok || value != "a=1, b=2" {
		t.Fatalf("cookie get: %q %v %v", value, ok, err)
	}
	if value, ok, err := fetch.Get("x-empty"); err != nil || ok || value != "" {
		t.Fatalf("empty fetch value retained: %q %v %v", value, ok, err)
	}
	iterator := fetch.Iterator()
	if !reflect.DeepEqual(iterator[len(iterator)-2:], []HeaderPair{{"set-cookie", "a=1"}, {"set-cookie", "b=2"}}) {
		t.Fatalf("cookie iterator order: %#v", iterator)
	}
	if !reflect.DeepEqual(fetch.HTTPHeader().Values("Set-Cookie"), []string{"a=1", "b=2"}) {
		t.Fatal("policy view discarded cookie rows")
	}
}

func TestBunHeaderMutationPositionsAndSnapshotIsolation(t *testing.T) {
	headers, _ := NewConstructorHeaders([]HeaderPair{{"Content-Type", "a"}, {"Accept", "b"}, {"X-first", "c"}, {"X-last", "d"}, {"Set-Cookie", "a=1"}})
	clone := headers.Clone()
	for _, operation := range []func() error{func() error { return headers.Set("content-type", "new") }, func() error { return headers.Delete("accept") }, func() error { return headers.Append("Accept", "again") }, func() error { return headers.Delete("x-first") }, func() error { return headers.Append("x-first", "last") }, func() error { return headers.Set("set-cookie", "new=1") }, func() error { return headers.Delete("missing") }} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := headers.MarshalJSON()
	if string(raw) != `{"content-type":"new","accept":"again","set-cookie":["new=1"],"x-last":"d","x-first":"last"}` {
		t.Fatal(string(raw))
	}
	previous, _ := clone.MarshalJSON()
	if string(previous) != `{"content-type":"a","accept":"b","set-cookie":["a=1"],"x-first":"c","x-last":"d"}` {
		t.Fatal("clone shared mutable vectors", string(previous))
	}
	cookies := headers.SetCookies()
	cookies[0] = "escape"
	if headers.SetCookies()[0] != "new=1" {
		t.Fatal("cookie snapshot escaped")
	}
	if err := headers.Delete("SET-COOKIE"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := headers.Get("set-cookie"); ok {
		t.Fatal("cookie delete ineffective")
	}
	var missing *BunHeaders
	if missing.Clone() != nil {
		t.Fatal("nil clone manufactured metadata")
	}
	if _, err := missing.MarshalJSON(); !errors.Is(err, ErrBunHeaderMetadata) {
		t.Fatal(err)
	}
	if err := NewSDKResponseError(nil, `{}`, nil, false); !errors.Is(err, ErrBunHeaderMetadata) {
		t.Fatal(err)
	}
}

func TestBunHeaderValidationAndLatin1WireBoundary(t *testing.T) {
	for _, name := range []string{"", "bad name", "é", "x\n"} {
		if _, err := NewConstructorHeaders([]HeaderPair{{name, "ok"}}); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
		headers, _ := NewConstructorHeaders(nil)
		for _, err := range []error{headers.Set(name, "v"), headers.Append(name, "v"), headers.Delete(name)} {
			var typed *BunHeaderError
			if !errors.As(err, &typed) || !strings.Contains(err.Error(), "Invalid header name") {
				t.Fatalf("lost invalid name %q: %v", name, err)
			}
		}
		if _, _, err := headers.Get(name); err == nil {
			t.Fatal("invalid get accepted")
		}
	}
	for _, value := range []string{"bad\x00value", "bad\rvalue", "bad\nvalue", "😀", string([]byte{0xff})} {
		if _, err := NewConstructorHeaders([]HeaderPair{{"x", value}}); err == nil {
			t.Fatalf("accepted invalid value %q", value)
		}
		headers, _ := NewFetchHeaders(nil)
		if err := headers.Append("x", value); err == nil {
			t.Fatal("invalid append accepted")
		}
		if err := headers.Set("x", value); err == nil {
			t.Fatal("invalid set accepted")
		}
	}
	headers, err := NewConstructorHeaders([]HeaderPair{{"X", " \té\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if value, _, _ := headers.Get("x"); value != "é" {
		t.Fatal(value)
	}
	rows, err := HeaderPairsFromWire([][2][]byte{{[]byte("X-RAW"), {0xe9, 0xff}}})
	if err != nil || !reflect.DeepEqual(rows, []HeaderPair{{"x-raw", "éÿ"}}) {
		t.Fatalf("raw Latin1 boundary %#v %v", rows, err)
	}
	if _, err := HeaderPairsFromWire([][2][]byte{{[]byte("bad name"), []byte("v")}}); err == nil {
		t.Fatal("wire invalid name accepted")
	}
}

func TestBunHeaderConcurrentMutationAndSnapshots(t *testing.T) {
	headers, _ := NewConstructorHeaders(nil)
	var workers sync.WaitGroup
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for count := 0; count < 100; count++ {
				_ = headers.Set("x-shared", "value")
				_ = headers.Append("cookie", "c")
				_ = headers.Delete("cookie")
				_, _, _ = headers.Get("x-shared")
				_, _ = headers.MarshalJSON()
				_ = headers.Iterator()
				_ = headers.HTTPHeader()
				_ = headers.SetCookies()
			}
		}()
	}
	workers.Wait()
}

func TestSDKHeaderValueTracksOriginalsAndRetainsLegacyMapRules(t *testing.T) {
	var missing *SDKStreamError
	if _, ok, err := missing.HeaderValue("retry-after"); ok || err != nil {
		t.Fatal(ok, err)
	}
	failure := &SDKStreamError{Headers: map[string][]string{"Retry-After": {"1", "2"}, "x-custom": {"first", "last"}}}
	for _, item := range []struct{ name, want string }{{"retry-after", "1, 2"}, {"X-CUSTOM", "first, last"}} {
		if value, ok, err := failure.HeaderValue(item.name); err != nil || !ok || value != item.want {
			t.Fatalf("legacy policy %q: %q %v %v", item.name, value, ok, err)
		}
	}
	if _, ok, err := failure.HeaderValue("missing"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, _, err := failure.HeaderValue("bad name"); err == nil {
		t.Fatal("invalid field accepted")
	}
	headers, _ := NewConstructorHeaders([]HeaderPair{{"retry-after", "9"}})
	failure.OriginalHeaders = headers
	_ = headers.Set("retry-after", "19")
	if value, ok, err := failure.HeaderValue("retry-after"); err != nil || !ok || value != "19" {
		t.Fatal("shared policy value stale", value, ok, err)
	}
	if _, _, err := failure.HeaderValue("bad name"); err == nil {
		t.Fatal("invalid original name accepted")
	}
	_ = headers.Delete("retry-after")
	if _, ok, err := failure.HeaderValue("retry-after"); err != nil || ok {
		t.Fatal("map fallback resurrected deleted original", ok, err)
	}
}
