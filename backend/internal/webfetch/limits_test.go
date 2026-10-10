package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestURLAndDomainCacheTTLPreflightOverrideAndCancellation(t *testing.T) {
	var domains, gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/web/domain_info" {
			domains.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"can_fetch":true}`)
			return
		}
		gets.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "body")
	}))
	defer server.Close()
	now := time.UnixMilli(1770000000000)
	skip := false
	service, e := New(Options{HTTPClient: &http.Client{Transport: fixtureRoute{server.URL, http.DefaultTransport}}, Settings: func(context.Context) (Settings, error) {
		return Settings{SkipPreflight: &skip, DesktopServerURL: "http://desktop"}, nil
	}, Now: func() time.Time { return now }, UserAgent: "fixture", PersistID: func() string { return "id" }, PersistBinary: func(context.Context, []byte, string, string) (Persisted, error) {
		return Persisted{}, errors.New("unexpected binary")
	}, ApplyPrompt: func(context.Context, json.RawMessage, bool) (json.RawMessage, error) {
		return nil, errors.New("unexpected model")
	}})
	if e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"https://one.fixture/a", "https://one.fixture/b", "https://one.fixture/a"} {
		if _, e = service.Fetch(context.Background(), raw); e != nil {
			t.Fatal(e)
		}
	}
	if domains.Load() != 1 || gets.Load() != 2 {
		t.Fatalf("initial caches domain%d pages%d", domains.Load(), gets.Load())
	}
	now = now.Add(5 * time.Minute)
	if _, e = service.Fetch(context.Background(), "https://one.fixture/c"); e != nil {
		t.Fatal(e)
	}
	if domains.Load() != 2 {
		t.Fatal("domain TTL not expired")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = service.Fetch(ctx, "https://one.fixture/a"); e != nil {
		t.Fatal("Source cached hit runs before aborted fetch", e)
	}
	now = now.Add(15 * time.Minute)
	skip = true
	if _, e = service.Fetch(context.Background(), "https://one.fixture/a"); e != nil {
		t.Fatal(e)
	}
	if domains.Load() != 2 || gets.Load() != 4 {
		t.Fatal("URL TTL or override", domains.Load(), gets.Load())
	}
	service.ClearCache()
	if service.cachedURL("https://one.fixture/a") != nil {
		t.Fatal("clearURL")
	}
	if !ShouldSkipPreflight(Settings{DesktopServerURL: "http://desktop"}) || ShouldSkipPreflight(Settings{SkipPreflight: &skip}) == false {
		t.Fatal("settings precedence")
	}
	skip = false
	if ShouldSkipPreflight(Settings{SkipPreflight: &skip, DesktopServerURL: "http://desktop"}) {
		t.Fatal("explicit false lost")
	}
}
func TestModelPromptTruncationDirectMarkdownAndNoCacheOfCrossHostRedirect(t *testing.T) {
	var called int
	now := time.UnixMilli(1770000000000)
	service, e := New(Options{HTTPClient: &http.Client{}, Settings: func(context.Context) (Settings, error) { return Settings{}, nil }, Now: func() time.Time { return now }, UserAgent: "fixture", PersistID: func() string { return "id" }, PersistBinary: func(context.Context, []byte, string, string) (Persisted, error) { return Persisted{}, nil }, ApplyPrompt: func(context.Context, json.RawMessage, bool) (json.RawMessage, error) {
		return nil, errors.New("unused")
	}})
	if e != nil {
		t.Fatal(e)
	}
	apply := func(ctx context.Context, prompt json.RawMessage, nonInteractive bool) (json.RawMessage, error) {
		called++
		var message string
		_ = json.Unmarshal(prompt, &message)
		if !strings.Contains(message, "[Content truncated due to length...]") {
			t.Error("not truncated at100k")
		}
		return quote("summary"), nil
	}
	big := strings.Repeat("x", 99999) + "😀 tail"
	output, e := service.Process(context.Background(), "https://one.fixture/a", quote("detail"), true, Result{Fetched: &FetchedContent{Content: big, Bytes: len(big), ContentType: "text/plain", Code: 200, CodeText: "OK"}}, now, apply)
	if e != nil || string(output.Result) != `"summary"` || called != 1 {
		t.Fatal(output, e)
	}
	output, e = service.Process(context.Background(), "https://go.dev/doc", quote("detail"), true, Result{Fetched: &FetchedContent{Content: "# doc", ContentType: "text/markdown", Code: 200, CodeText: "OK"}}, now, apply)
	if e != nil || string(output.Result) != `"# doc"` || called != 1 {
		t.Fatal(output, e)
	}
	if string(TruncateContent(big))[99998:] == "" {
		t.Fatal("truncation")
	}
}
