package queryloop_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop/testdata/request-fixture/driver"
)

// These expected bytes come from actual Source query(), not the Go builder.
// The candidate receives original history/system/options only, then constructs,
// signs and sends the request independently and consumes its live envelopes.
func TestActualSourceOriginalRequestIntegration(t *testing.T) {
	raw, err := os.ReadFile("testdata/request-fixture/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Input       driver.Input
			Observation driver.Observation
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil || len(corpus.Cases) != 3 {
		t.Fatalf("actual query request corpus: %v cases=%d", err, len(corpus.Cases))
	}
	for _, item := range corpus.Cases {
		t.Run(item.Input.Name, func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if count != 1 || len(item.Observation.Requests) != 1 {
					t.Error("unexpected model continuation")
					http.Error(w, "unexpected", 400)
					return
				}
				want := item.Observation.Requests[0]
				if r.Method != want.Method || r.URL.RequestURI() != want.Path || string(body) != want.Body {
					t.Errorf("independent prepared request bytes changed: %s %s %s", r.Method, r.URL.RequestURI(), body)
				}
				for _, header := range want.Headers {
					if strings.Join(r.Header.Values(header[0]), ", ") != header[1] {
						t.Errorf("resolved header %s changed", header[0])
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("request-id", "query-request-response")
				_, _ = io.WriteString(w, item.Input.Wire)
			}))
			defer server.Close()
			input := item.Input
			input.Home = t.TempDir()
			input.Origin = server.URL
			actual, err := driver.Observe(input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(item.Observation, actual) {
				want, _ := json.Marshal(item.Observation)
				got, _ := json.Marshal(actual)
				t.Fatalf("complete query request contract diverged\nwant: %s\ngot: %s", want, got)
			}
			if count != 1 {
				t.Fatalf("request count=%d", count)
			}
		})
	}
}
