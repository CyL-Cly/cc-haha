package streamhistory

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type observedHTTPRequest struct {
	Method    string             `json:"method"`
	Path      string             `json:"path"`
	BodyBytes string             `json:"bodyBytes"`
	Headers   map[string]*string `json:"headers"`
}

// These expected bytes and complete live-envelope observations were recorded
// from real Source query/SDK HTTP calls, never synthesized by the Go candidate.
func TestActualSourceHTTPControllerGoldens(t *testing.T) {
	raw, err := os.ReadFile("testdata/http_source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Name        string              `json:"name"`
			Request     observedHTTPRequest `json:"request"`
			Observation json.RawMessage     `json:"observation"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus.Cases) != 4 {
		t.Fatalf("missing real Source HTTP goldens: cases=%d error=%v", len(corpus.Cases), err)
	}
	for _, fixture := range corpus.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			var expected fixtureObservation
			if err := json.Unmarshal(fixture.Observation, &expected); err != nil {
				t.Fatal(err)
			}
			requests := make(chan observedHTTPRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read actual HTTP request: %v", err)
				}
				actual := observedHTTPRequest{Method: request.Method, Path: request.URL.RequestURI(), BodyBytes: hex.EncodeToString(body), Headers: map[string]*string{}}
				for name := range fixture.Request.Headers {
					var value *string
					if _, present := request.Header[http.CanonicalHeaderKey(name)]; present {
						text := request.Header.Get(name)
						value = &text
					}
					actual.Headers[name] = value
				}
				requests <- actual
				writer.Header().Set("content-type", "text/event-stream")
				writer.Header().Set("request-id", "fixed-request")
				_, _ = writer.Write([]byte(expected.Wire))
			}))
			defer server.Close()
			model, nonInteractive := expected.Profile["model"].(string), expected.Profile["nonInteractive"].(bool)
			input := httpFixtureInput{fixtureInput: fixtureInput{Wire: expected.Wire, Model: &model, NonInteractive: &nonInteractive}, HTTPOrigin: server.URL}
			input.Prepared.Path, input.Prepared.BodyBytes = fixture.Request.Path, fixture.Request.BodyBytes
			input.Prepared.Headers = map[string]string{}
			for _, name := range []string{"anthropic-beta", "x-stainless-helper"} {
				if value := fixture.Request.Headers[name]; value != nil {
					input.Prepared.Headers[name] = *value
				}
			}
			actual, err := observeHTTPFixture(input)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case request := <-requests:
				if !reflect.DeepEqual(fixture.Request, request) {
					t.Fatalf("captured Source request bytes/declared headers changed\nwant: %+v\ngot: %+v", fixture.Request, request)
				}
			case <-time.After(time.Second):
				t.Fatal("Go did not send its actual HTTP request")
			}
			if len(requests) != 0 {
				t.Fatal("transport retried the prepared request")
			}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(fixture.Observation, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("real HTTP Source envelope/history differs\nwant: %s\ngot: %s", fixture.Observation, encoded)
			}
		})
	}
}

func TestHTTPFixtureRefusesUnmigratedExternalFailureFeedback(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP API failure", http.StatusTooManyRequests, `{"error":{"message":"quota fixture"}}`},
		{"SDK malformed JSON", http.StatusOK, "event: message_start\ndata: {\n\n"},
		{"SDK SSE error", http.StatusOK, "event: error\ndata: {\"error\":{\"message\":\"stream fixture\"}}\n\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("content-type", "text/event-stream")
				writer.WriteHeader(fixture.status)
				_, _ = writer.Write([]byte(fixture.body))
			}))
			defer server.Close()
			input := httpFixtureInput{HTTPOrigin: server.URL}
			input.Prepared.Path, input.Prepared.BodyBytes = "/v1/messages?beta=true", hex.EncodeToString([]byte(`{"stream":true}`))
			actual, err := observeHTTPFixture(input)
			if err == nil || !strings.Contains(err.Error(), "outside HTTP history profile") {
				t.Fatalf("unported external failure was disguised as a compatible response: %v", err)
			}
			if len(actual.Assistants) != 0 || len(actual.Events) != 0 || actual.UUIDCalls != 0 {
				t.Fatalf("published counterfeit transport feedback/history: %+v", actual)
			}
		})
	}
}

func TestHTTPFixturePrivateEntryGuardsBeforeTransport(t *testing.T) {
	for _, fault := range []string{"matching sandbox", "missing allocation root", "mismatched HOME", "dangling config symlink", "wrong provider origin", "public origin", "embedded credentials", "absolute request path", "wrong production profile"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			config := filepath.Join(home, ".claude")
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			original, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(home); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(original) })
			for name, value := range map[string]string{
				"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config,
				"BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root,
				"NODE_ENV": "production", "CC_HAHA_STREAM_HTTP_FIXTURE": "1", "CC_HAHA_STREAM_HISTORY_FIXTURE": "1",
				"ANTHROPIC_BASE_URL": "http://127.0.0.1:43210",
			} {
				t.Setenv(name, value)
			}
			input := httpFixtureInput{fixtureInput: fixtureInput{HomeDir: home}, HTTPOrigin: "http://127.0.0.1:43210"}
			input.Prepared.Path = "/v1/messages?beta=true"
			switch fault {
			case "missing allocation root":
				t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", "")
			case "mismatched HOME":
				input.HomeDir = filepath.Join(root, "other")
			case "dangling config symlink":
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "missing"), config); err != nil {
					t.Fatal(err)
				}
			case "wrong provider origin":
				t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:43211")
			case "public origin":
				input.HTTPOrigin = "http://fixture.invalid:43210"
			case "embedded credentials":
				input.HTTPOrigin = "http://fixture@127.0.0.1:43210"
			case "absolute request path":
				input.Prepared.Path = "http://fixture.invalid/v1/messages"
			case "wrong production profile":
				t.Setenv("NODE_ENV", "test")
			}
			err = validateHTTPFixture(input)
			if fault == "matching sandbox" && err != nil || fault != "matching sandbox" && err == nil {
				t.Fatalf("HTTP entry guard %s: %v", fault, err)
			}
		})
	}
}
