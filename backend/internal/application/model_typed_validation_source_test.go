package application

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTypedCustomModelOriginalNonstreamTLSValidationErrorsSuccessOnlyCache(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]map[string]any{"Source": {}, "Go": {}}
	var cutBodyReady chan struct{}
	cutBodyAcknowledgements := 0
	stop := make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m680-cut-json-body-ready" {
			mu.Lock()
			ready := cutBodyReady
			if r.Method == http.MethodPost && ready != nil {
				cutBodyReady = nil
				cutBodyAcknowledgements++
				close(ready)
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			mu.Unlock()
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		if json.Unmarshal(b, &body) != nil {
			t.Error("actual JSON body", string(b))
			w.WriteHeader(400)
			return
		}
		client := r.Header.Get("X-M680-Client")
		mu.Lock()
		seen[client] = append(seen[client], body)
		mu.Unlock()
		model, _ := body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch model {
		case "Missing-Model":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found_error","message":"private missing"}}`))
		case "Auth-Model":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"private unauthorized"}}`))
		case "Generic-API":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"private limited"}}`))
		case "Body-Not-Found":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"not_found_error","message":"model: private missing"}`))
		case "Connection-Closed":
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
			return
		case "Cut-JSON":
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, out, err := hijacker.Hijack()
				if err == nil {
					var ready chan struct{}
					headers := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n"
					if client == "Source" {
						ready = make(chan struct{})
						mu.Lock()
						cutBodyReady = ready
						mu.Unlock()
						headers += "X-M680-Body-Ready: /m680-cut-json-body-ready\r\n"
					}
					_, _ = out.WriteString(headers + "\r\n{\"partial\":")
					_ = out.Flush()
					if ready != nil {
						select {
						case <-ready:
						case <-stop:
						}
					}
					_ = conn.Close()
				}
			}
			return
		case "Empty-JSON":
		case "Null-JSON":
			_, _ = w.Write([]byte("null"))
		case "Number-JSON":
			_, _ = w.Write([]byte("42"))
		case "Malformed-JSON":
			_, _ = w.Write([]byte(`{"unfinished":`))
		default:
			_, _ = w.Write([]byte(`{"id":"actual-validation","type":"message","role":"assistant","model":"returned-model-does-not-select","content":[{"type":"text","text":"x"}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`))
		}
	}))
	defer func() { close(stop); peer.Close() }()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})
	rows := []typedModelSourceRow{}
	for _, arg := range []string{"Valid-MiXeD", "Valid-MiXeD", "Missing-Model", "Missing-Model", "Auth-Model", "Generic-API", "Body-Not-Found", "Malformed-JSON", "Empty-JSON", "Null-JSON", "Number-JSON", "Connection-Closed", "Cut-JSON", "Cut-JSON", "Valid-MiXeD[1m]", "claude-fable-5-1"} {
		rows = append(rows, typedModelSourceRow{Name: arg, Args: arg, Settings: map[string]any{"model": "sonnet", "future": true}, State: map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil, "fastMode": false}})
	}
	want := runTypedModelSource(t, rows, peer.URL, string(certificate))
	for _, row := range want {
		t.Logf("actual original notice %s => %q", row.Name, row.Notice)
		if row.Name == "Connection-Closed" || row.Name == "Cut-JSON" {
			phase := "headers"
			if row.Name == "Cut-JSON" {
				phase = "body"
			}
			if len(row.TransportErrors) != 1 || row.TransportErrors[0]["phase"] != phase || row.TransportErrors[0]["name"] != "Error" {
				t.Fatal("actual Source TLS error phase", row.Name, row.TransportErrors)
			}
			fields, _ := row.TransportErrors[0]["fields"].(map[string]any)
			if fields["code"] != "ECONNRESET" {
				t.Fatal("actual Source TLS error phase", row.Name, row.TransportErrors)
			}
		}
	}
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Model = "sonnet"
	root := t.TempDir()
	certPath := filepath.Join(root, "root.pem")
	if err := os.WriteFile(certPath, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["NODE_EXTRA_CA_CERTS"] = certPath
		env["ANTHROPIC_CUSTOM_HEADERS"] = "X-M680-Client: Go"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"sonnet","future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for i, row := range rows {
		// Restore the same actual base through the public typed alias consumer;
		// valid-model cache remains process owned across these invocations.
		if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model sonnet"}, func(tui.Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
		var notice string
		if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model " + row.Args}, func(event tui.Event) error {
			if event.Kind == tui.Notice {
				notice = event.Text
			}
			return nil
		}); err != nil {
			t.Fatalf("%s: Source %q Go error %v", row.Name, want[i].Notice, err)
		}
		if notice != want[i].Notice {
			t.Fatalf("%s: Source %q Go %q", row.Name, want[i].Notice, notice)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		_ = json.Unmarshal(b, &saved)
		if !reflect.DeepEqual(saved, want[i].Settings) {
			t.Fatal("original actual persistence after validation", row.Name, saved, want[i].Settings)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if cutBodyAcknowledgements != 2 {
		t.Fatal("actual Source JSON readers did not acknowledge both TLS body truncations", cutBodyAcknowledgements)
	}
	if len(seen["Source"]) != 15 || len(seen["Go"]) != 15 {
		t.Fatal("real success-only cache or zero SDK retries differs", len(seen["Source"]), len(seen["Go"]))
	}
	for _, client := range []string{"Source", "Go"} {
		attempts := map[string]int{}
		for _, body := range seen[client] {
			model, _ := body["model"].(string)
			attempts[model]++
		}
		// Valid-MiXeD[1m] has a distinct cache key and a second wire request;
		// its exact repeat has none. Both repeated failures issue new requests.
		if attempts["Valid-MiXeD"] != 2 || attempts["Missing-Model"] != 2 || attempts["Cut-JSON"] != 2 {
			t.Fatal("actual successful cache and failed validation reentry", client, attempts)
		}
		t.Logf("actual %s nonstream attempts=%d successful model=%d missing model=%d body truncation=%d", client, len(seen[client]), attempts["Valid-MiXeD"], attempts["Missing-Model"], attempts["Cut-JSON"])
	}
	for i, source := range seen["Source"] {
		goBody := seen["Go"][i]
		// Each original process owns a different SessionID/device ID and final
		// CCH signing. Compare actual side-query policy and content fields.
		for _, key := range []string{"model", "max_tokens", "messages", "thinking", "tools", "stream", "output_config"} {
			if !reflect.DeepEqual(source[key], goBody[key]) {
				t.Fatal("actual nonstream Source request", i, key, source[key], goBody[key])
			}
		}
		var sourceSystem, goSystem []any
		sourceSystem, _ = source["system"].([]any)
		goSystem, _ = goBody["system"].([]any)
		if len(sourceSystem) != len(goSystem) || sourceSystem[len(sourceSystem)-1].(map[string]any)["text"] != goSystem[len(goSystem)-1].(map[string]any)["text"] {
			t.Fatal("original CLI side-query system prefix", sourceSystem, goSystem)
		}
		if source["metadata"] == nil || goBody["metadata"] == nil {
			t.Fatal("missing actual API metadata", i)
		}
	}
}
