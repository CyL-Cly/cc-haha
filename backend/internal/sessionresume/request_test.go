package sessionresume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func testReducer(t *testing.T) *Reducer {
	t.Helper()
	var id atomic.Int64
	factory, err := messages.NewFactory(func() string { return fmt.Sprint(id.Add(1)) }, func() time.Time { return time.UnixMilli(1700000000000) })
	mustNative(t, err)
	reducer, err := New(Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) {
		return RelativePath("/private/fixture", "/private/fixture", raw)
	}, OnDiagnostic: func(diagnostic Diagnostic) { t.Errorf("unexpected diagnostic %+v", diagnostic) }, OnError: func(err error) { t.Errorf("unexpected error %v", err) }})
	mustNative(t, err)
	return reducer
}
func nativeLoader(t *testing.T) *sessionhistory.Loader {
	t.Helper()
	loader, err := sessionhistory.New(sessionhistory.Options{Files: sessionstore.OSFileSystem{}, Stat: func(path string) (int64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: os.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) { t.Errorf("unexpected loader diagnostic %+v", event) }})
	mustNative(t, err)
	return loader
}
func TestActualSourceNextRequestFromPrivateOriginalJSONL(t *testing.T) {
	raw, err := os.ReadFile("testdata/first_request_contract.json")
	mustNative(t, err)
	var contracts []struct {
		Input  struct{ Entries []json.RawMessage }
		Source struct {
			Observation map[string]any
			SourceOnly  struct {
				Runtime             provider.SourceRuntime
				PrefetchAllocations []any
			}
		}
	}
	mustNative(t, json.Unmarshal(raw, &contracts))
	if len(contracts) != 8 {
		t.Fatal("missing actual release Source requests")
	}
	for index, contract := range contracts {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			mustNative(t, os.WriteFile(path, []byte(strings.Join(rawStrings(contract.Input.Entries), "\n")+"\n"), 0600))
			var wire []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				mustNative(t, err)
				wire = append(wire, map[string]any{"path": r.URL.RequestURI(), "method": r.Method, "body": string(body)})
				w.Header().Set("Content-Type", "text/event-stream")
				_, err = io.WriteString(w, "event: message_stop\ndata: {}\n\n")
				mustNative(t, err)
			}))
			defer server.Close()
			calls := 0
			allocations := []map[string]any{}
			uuid := func() string {
				calls++
				value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
				allocations = append(allocations, map[string]any{"kind": "uuid", "value": value})
				return value
			}
			now := func() time.Time {
				allocations = append(allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
				return time.UnixMilli(1700000000000)
			}
			factory, err := messages.NewFactory(uuid, now)
			mustNative(t, err)
			reducer, err := New(Options{Factory: factory, TranscriptClassifier: true, RelativePath: func(raw json.RawMessage) (json.RawMessage, error) {
				return RelativePath(filepath.Dir(path), filepath.Dir(path), raw)
			}, OnDiagnostic: func(event Diagnostic) { t.Errorf("unexpected diagnostic %+v", event) }, OnError: func(err error) { t.Error(err) }})
			mustNative(t, err)
			loaded, err := reducer.LoadPath(nativeLoader(t), path)
			mustNative(t, err)
			resultSource := nativeSource(t, loaded)
			next, err := reducer.AppendUser(loaded.Result, json.RawMessage(`"next original prompt\ud800"`))
			mustNative(t, err)
			generation := append([]map[string]any{}, allocations...)
			history := []string{}
			for _, message := range next.Messages {
				history = append(history, nativeSource(t, message))
			}
			var sid string
			mustNative(t, json.Unmarshal(loaded.SessionID, &sid))
			env := map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "offline-fixture-key"}
			prepared, err := BuildRequest(next, requestcontext.Context{SystemPrompt: []json.RawMessage{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model", QuerySource: "sdk", IsNonInteractiveSession: true}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: env, DeviceID: "fixture-resume-device", SessionID: sid, SDKBetas: []string{}, TTLAllowlist: []string{}}}, requestcontext.Dependencies{UUID: uuid, Now: now})
			mustNative(t, err)
			key := "offline-fixture-key"
			options := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: env, FallbackAPIKey: &key, SessionID: sid, Runtime: contract.Source.SourceOnly.Runtime, RequestID: uuid})
			config := options.StreamConfig()
			config.HTTPClient = server.Client()
			requests, prefetch := []map[string]any{}, []any{}
			finalize := config.FinalizeHeaders
			config.FinalizeHeaders = func(target string, headers http.Header) (http.Header, error) {
				resolved, err := finalize(target, headers)
				if err != nil {
					return nil, err
				}
				pairs := [][2]string{}
				for name, values := range resolved {
					pairs = append(pairs, [2]string{strings.ToLower(name), strings.Join(values, ", ")})
				}
				sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
				requests = append(requests, map[string]any{"path": "/v1/messages?beta=true", "method": "POST", "body": string(prepared.PreparedRequest.Body), "headers": pairs})
				prefetch = append(prefetch, append([]map[string]any{}, allocations...))
				return resolved, nil
			}
			stream, err := provider.NewAnthropic(config).OpenMessagesStream(context.Background(), prepared.PreparedRequest)
			mustNative(t, err)
			for {
				_, err := stream.Next()
				if err == io.EOF {
					break
				}
				mustNative(t, err)
			}
			mustNative(t, stream.Close())
			actual := map[string]any{"resultSource": resultSource, "generation": generation, "historyBefore": history, "requests": requests}
			var normalized map[string]any
			mustNative(t, json.Unmarshal([]byte(nativeSource(t, actual)), &normalized))
			if !reflect.DeepEqual(normalized, contract.Source.Observation) {
				t.Fatalf("Source next request differs\nexpected %s\nactual %s", nativeSource(t, contract.Source.Observation), nativeSource(t, normalized))
			}
			if nativeSource(t, prefetch) != nativeSource(t, contract.Source.SourceOnly.PrefetchAllocations) {
				t.Fatal("pre-fetch allocation order differs")
			}
			if len(wire) != 1 || wire[0]["body"] != string(prepared.PreparedRequest.Body) || wire[0]["path"] != "/v1/messages?beta=true" || wire[0]["method"] != "POST" {
				t.Fatal("actual loopback wire did not receive original independently built request")
			}
			if original, err := os.ReadFile(path); err != nil || string(original) != strings.Join(rawStrings(contract.Input.Entries), "\n")+"\n" {
				t.Fatal("resume changed original persisted bytes")
			}
		})
	}
}
func rawStrings(raws []json.RawMessage) []string {
	result := []string{}
	for _, raw := range raws {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			panic(err)
		}
		result = append(result, compact.String())
	}
	return result
}
func TestRequiredCapabilitiesAndUnsupportedNextRequestStop(t *testing.T) {
	reducer := testReducer(t)
	for _, remove := range []func(*Options){func(o *Options) { o.Factory = nil }, func(o *Options) { o.RelativePath = nil }, func(o *Options) { o.OnError = nil }, func(o *Options) { o.OnDiagnostic = nil }} {
		options := reducer.options
		remove(&options)
		if _, err := New(options); err == nil {
			t.Fatal("missing capability accepted")
		}
	}
	if _, err := ParseMessages([]json.RawMessage{json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid raw envelope accepted")
	}
	if _, err := reducer.LoadPath(nil, "injected"); err == nil {
		t.Fatal("missing loader accepted")
	}
	if loaded, err := reducer.LoadPath(nativeLoader(t), filepath.Join(t.TempDir(), "missing.jsonl")); err != nil || len(loaded.Messages) != 0 {
		t.Fatal("Source missing-file empty-history behavior changed")
	}
	path := filepath.Join(t.TempDir(), "unsupported.jsonl")
	mustNative(t, os.WriteFile(path, []byte(`{"parentUuid":null,"type":"user","message":{"role":"user","content":"raw"},"uuid":"root","timestamp":"2025-01-01T00:00:00Z","snipMetadata":{"removedUuids":{"future":true}}}`+"\n"), 0600))
	if _, err := reducer.LoadPath(nativeLoader(t), path); !errors.Is(err, sessionhistory.ErrUnsupportedSnipIterator) {
		t.Fatalf("unsupported history did not propagate real loader failure: %v", err)
	}
	if _, err := reducer.AppendUser(Result{}, json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid original prompt accepted")
	}
	for _, content := range []json.RawMessage{json.RawMessage(`""`), json.RawMessage(`null`), json.RawMessage(`[]`)} {
		next, err := reducer.AppendUser(Result{}, content)
		mustNative(t, err)
		if len(next.Messages) != 1 {
			t.Fatal("real factory missing")
		}
	}
	if _, err := BuildRequest(Result{}, requestcontext.Context{History: []protocol.JSONObject{}}, requestcontext.Dependencies{}); err == nil {
		t.Fatal("prepared substitute history accepted")
	}
	for _, raw := range []string{`null`, `[]`, `3`, `{"type":"attachment","attachment":{"type":"future"}}`} {
		history, err := ParseMessages([]json.RawMessage{json.RawMessage(raw)})
		mustNative(t, err)
		_, err = BuildRequest(Result{Messages: history}, requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model"}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, SessionID: "sid", DeviceID: "device"}}, requestcontext.Dependencies{UUID: func() string { return "id" }, Now: time.Now})
		var capability *requestcontext.CapabilityError
		if !errors.As(err, &capability) {
			t.Fatalf("unsupported restored envelope accepted: %v", err)
		}
	}
}
func TestIndependentHistoriesShareFactoryWithoutSharedMutation(t *testing.T) {
	reducer := testReducer(t)
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			history, err := ParseMessages([]json.RawMessage{json.RawMessage(`{"type":"user","message":{"role":"user","content":"original"},"permissionMode":"auto"}`)})
			if err != nil {
				t.Error(err)
				return
			}
			result, err := reducer.Deserialize(history)
			if err != nil {
				t.Error(err)
				return
			}
			if result.TurnInterruptionState.Message != history[0] || len(result.Messages) != 2 || string(history[0].Field("permissionMode")) != `"auto"` {
				t.Error("independent input identity/mutation drift")
			}
		}()
	}
	workers.Wait()
}
