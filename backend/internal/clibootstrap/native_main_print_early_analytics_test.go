package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeMainPrintMigrationsUseOneEarlyAnalyticsServiceAndBootstrapSID(t *testing.T) {
	var mu sync.Mutex
	records := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var envelope struct {
			Events []protocol.JSONObject `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		records = append(records, envelope.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()
	f := nativeCommandMigrationFixture(t, "sonnet-pro")
	delete(f.n.Environment, "DISABLE_TELEMETRY")
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{SessionID: "a1a1a1a1-1111-4111-8111-111111111111", Model: "haiku", ComputerUse: &disabled, SystemPrompt: "owned early analytics consumer", Tools: []string{""}, MaxTurns: 1, Sources: DefaultState()}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Version: "private-early-same-service", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard,
		AnalyticsHTTP: func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL, strings.NewReader(string(input.Body)))
			if err != nil {
				return analytics.Response{}, err
			}
			request.Header = input.Headers.Clone()
			response, err := peer.Client().Do(request)
			if err != nil {
				return analytics.Response{}, err
			}
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			return analytics.Response{Status: response.StatusCode}, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	early := m.fileAnalytics
	if early == nil {
		t.Fatal("actual constructor migration had no attached analytics Service")
	}
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.initializeNativeFileAnalytics(scope); err != nil {
			return err
		}
		if m.fileAnalytics != early {
			return fmt.Errorf("later init replaced process analytics Service")
		}
		return m.fileAnalytics.Flush(scope)
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	actual := append([]protocol.JSONObject(nil), records...)
	mu.Unlock()
	if len(actual) != 2 {
		t.Fatal("actual migration exporter rows", len(actual))
	}
	for index, expected := range []string{"claude-sonnet-4-5-20250929", "claude-sonnet-5-5"} {
		var data protocol.JSONObject
		if err := data.UnmarshalJSON(actual[index].Get("event_data")); err != nil {
			t.Fatal(err)
		}
		if data.String("model") != expected || data.String("session_id") != m.state.SessionID() {
			t.Fatal("preAction model/late bootstrap SID consumer", mustMigrationJSON(data))
		}
	}
	if err := m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.fileAnalytics != early {
		t.Fatal("SDK activation replaced early service")
	}
	m.Close()
	if err := m.fileAnalytics.Log(nil, analytics.Event{Name: "foreign"}); err != cooperative.ErrRestricted {
		t.Fatal("invalid analytics scope", err)
	}
}
