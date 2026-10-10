package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceExporterBatchesAuthFailuresAndSpool(t *testing.T) {
	type record struct {
		Event     Event               `json:"event"`
		ID        string              `json:"id"`
		Timestamp int64               `json:"timestamp"`
		Core      protocol.JSONObject `json:"core"`
		User      protocol.JSONObject `json:"user"`
	}
	core := protocol.NewJSONObject(field("model", "fixture"), field("sessionId", "private-record"), field("isInteractive", "false"), protocol.JSONField{Name: "envContext", Value: json.RawMessage(`{}`)})
	records := []record{}
	for i := 0; i < 3; i++ {
		records = append(records, record{Event: Event{Name: "tengu_file_operation", Metadata: protocol.NewJSONObject(field("count", i))}, ID: fmt.Sprint("private-record-", i), Timestamp: 1000, Core: core, User: protocol.NewJSONObject()})
	}
	type oldFile struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	type profile struct {
		Previous    []oldFile  `json:"previous,omitempty"`
		Retry       bool       `json:"retry,omitempty"`
		Statuses    []int      `json:"statuses"`
		Exports     [][]record `json:"exports"`
		BatchSize   int        `json:"batchSize"`
		MaxAttempts int        `json:"maxAttempts"`
		SkipAuth    bool       `json:"skipAuth"`
		Killed      bool       `json:"killed"`
	}
	cases := []profile{{Statuses: []int{204, 204}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 8}, {Statuses: []int{401, 204, 204}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 8}, {Statuses: []int{503}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 1}, {Statuses: []int{204}, Exports: [][]record{records[:1]}, BatchSize: 2, MaxAttempts: 8, SkipAuth: true}, {Statuses: []int{}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 8, Killed: true}}
	cases = append(cases,
		profile{Statuses: []int{204, 204}, BatchSize: 2, MaxAttempts: 8, Previous: []oldFile{{"1p_failed_events.$SID.old.json", "{\"event_type\":\"future\",\"event_data\":{\"retained\":17}}\n[1,2]\nnull\n"}}},
		profile{Statuses: []int{204, 503}, BatchSize: 2, MaxAttempts: 8, Previous: []oldFile{{"1p_failed_events.$SID.old.json", "{\"n\":1}\n{\"n\":2}\n{\"n\":3,\"future\":{\"x\":9}}\n"}}},
		profile{Statuses: []int{}, BatchSize: 2, MaxAttempts: 8, Previous: []oldFile{{"1p_failed_events.other.old.json", "{\"retained\":1}\n"}, {"1p_failed_events.$SID.old.txt", "{\"retained\":2}\n"}, {"1p_failed_events.$SID.old.json", "invalid\n"}}},
		profile{Statuses: []int{503, 204, 204}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 8, Retry: true},
		profile{Statuses: []int{503}, Exports: [][]record{records}, BatchSize: 2, MaxAttempts: 1, Retry: true},
	)
	type request struct {
		Key    string
		Events any
	}
	var mu sync.Mutex
	counts := map[string]int{}
	captured := map[string][]request{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var i int
		_, _ = fmt.Sscanf(r.URL.Path, "/case/%d", &i)
		mu.Lock()
		index := counts[r.URL.Path]
		counts[r.URL.Path]++
		captured[r.URL.Path] = append(captured[r.URL.Path], request{r.Header.Get("x-api-key"), body["events"]})
		mu.Unlock()
		status := 204
		if index < len(cases[i].Statuses) {
			status = cases[i].Statuses[index]
		}
		w.WriteHeader(status)
	}))
	defer sink.Close()
	rawSource := sourceFixture(t, "native-file-analytics-exporter.fixture.ts", map[string]any{"origin": sink.URL, "cases": cases})
	var expected []struct {
		Files []struct {
			Name   string `json:"name"`
			Events any    `json:"events"`
		} `json:"files"`
	}
	if e := json.Unmarshal(rawSource, &expected); e != nil {
		t.Fatal(e, string(rawSource))
	}
	mu.Lock()
	sourceRequests := captured
	captured = map[string][]request{}
	counts = map[string]int{}
	mu.Unlock()
	for i, row := range cases {
		owner := cooperative.New()
		service := testService(t, owner, sink.URL, func(o *Options) {
			o.SessionID = func(*cooperative.Scope) string { return fmt.Sprint("private-source-", i) }
			o.Config = func(_ *cooperative.Scope, name string) protocol.JSONObject {
				if name == "tengu_1p_event_batch_config" {
					return protocol.NewJSONObject(field("baseUrl", sink.URL), field("path", fmt.Sprint("/case/", i)), field("maxExportBatchSize", row.BatchSize), field("maxAttempts", row.MaxAttempts), field("skipAuth", row.SkipAuth), field("scheduledDelayMillis", 60000))
				}
				if name == "tengu_frond_boric" {
					return protocol.NewJSONObject(field("firstParty", row.Killed))
				}
				return protocol.NewJSONObject()
			}
			o.Prepare = func(_ *cooperative.Scope, url string, body []byte, skip bool) (Request, error) {
				headers := http.Header{"Content-Type": []string{"application/json"}}
				if !skip {
					headers.Set("x-api-key", "fake-event-key")
				}
				return Request{URL: url, Headers: headers, Body: body, Timeout: time.Second}, nil
			}
		})
		if len(row.Previous) > 0 {
			directory := filepath.Join(service.o.ConfigDir, "telemetry")
			if e := os.MkdirAll(directory, 0700); e != nil {
				t.Fatal(e)
			}
			for _, file := range row.Previous {
				name := strings.ReplaceAll(file.Name, "$SID", fmt.Sprint("private-source-", i))
				if e := os.WriteFile(filepath.Join(directory, name), []byte(file.Content), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := service.RetryPrevious(scope); e != nil {
					return e
				}
				return service.Flush(scope)
			}); e != nil {
				t.Fatal(e)
			}
		}
		for _, batch := range row.Exports {
			events := []protocol.JSONObject{}
			for _, record := range batch {
				event, e := Record(RecordInput{Event: record.Event, ID: record.ID, Timestamp: time.UnixMilli(record.Timestamp), Core: record.Core, User: record.User})
				if e != nil {
					t.Fatal(e)
				}
				events = append(events, event)
			}
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				future, e := service.Export(scope, events)
				if e != nil {
					return e
				}
				_, e = future.Await(scope)
				return e
			}); e != nil {
				t.Fatal(e)
			}
		}
		if row.Retry {
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if service.retry != nil {
					_ = scope.ClearTimer(service.retry)
					service.retry = nil
				}
				if e := service.retryCurrent(scope); e != nil {
					return e
				}
				return service.Flush(scope)
			}); e != nil {
				t.Fatal(e)
			}
		}
		files := []struct {
			Name   string `json:"name"`
			Events any    `json:"events"`
		}{}
		directory := filepath.Join(service.o.ConfigDir, "telemetry")
		entries, _ := os.ReadDir(directory)
		for _, entry := range entries {
			raw, e := os.ReadFile(filepath.Join(directory, entry.Name()))
			if e != nil {
				t.Fatal(e)
			}
			events := []any{}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var value any
				if e = json.Unmarshal([]byte(line), &value); e != nil {
					t.Fatal(e)
				}
				events = append(events, value)
			}
			files = append(files, struct {
				Name   string `json:"name"`
				Events any    `json:"events"`
			}{normalizedSpoolName(entry.Name(), fmt.Sprint("private-source-", i)), events})
		}
		if !reflect.DeepEqual(files, expected[i].Files) {
			a, _ := json.Marshal(files)
			b, _ := json.Marshal(expected[i].Files)
			t.Errorf("profile%d Sourcefiles%s Gofiles%s", i, b, a)
		}
		mu.Lock()
		if !reflect.DeepEqual(sourceRequests[fmt.Sprint("/case/", i)], captured[fmt.Sprint("/case/", i)]) {
			a, _ := json.Marshal(sourceRequests[fmt.Sprint("/case/", i)])
			b, _ := json.Marshal(captured[fmt.Sprint("/case/", i)])
			t.Errorf("profile%d Sourcerequests%s Gorequests%s", i, a, b)
		}
		mu.Unlock()
		_ = owner.Run(context.Background(), func(scope *cooperative.Scope) error { return service.Shutdown(scope) })
		owner.Close()
	}
}

func normalizedSpoolName(name, sid string) string {
	if strings.HasPrefix(name, "1p_failed_events."+sid+".") && !strings.HasSuffix(name, ".old.json") {
		return "1p_failed_events.$SID.$PROCESS.json"
	}
	return strings.ReplaceAll(name, sid, "$SID")
}
