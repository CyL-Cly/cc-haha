package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The separate whole Original fixture proves that the real StructuredIO env
// update and get_settings ACK leave file analytics enabled. This consumer uses
// the ordinary native stdin, actual Read implementation and two socket requests.
func TestNativeSDKEnvironmentGetSettingsAndReadKeepActualFirstPartyEvent(t *testing.T) {
	f := newNativePluginFixture(t)
	for _, key := range []string{"DISABLE_TELEMETRY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"} {
		delete(f.n.Environment, key)
	}
	f.n.Environment["NODE_ENV"] = "production"
	f.n.Environment["USER_TYPE"] = "external"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-analytics-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"directory":"INITIAL"}}`)
	f.write(t, filepath.Join(f.config, ".claude.json"), `{"migrationVersion":11,"userID":"owned-sdk-analytics-device","customApiKeyResponses":{"approved":["ke-sdk-analytics-key"],"rejected":[]}}`)
	next := filepath.Join(f.cwd, "sdk-next")
	f.write(t, filepath.Join(next, "settings.json"), `{"future":{"directory":"NEXT"}}`)
	f.write(t, filepath.Join(next, ".claude.json"), `{"migrationVersion":11,"userID":"owned-sdk-analytics-device"}`)
	file := filepath.Join(f.cwd, "observed.txt")
	content := "ACTUAL_SDK_ANALYTICS_READ\n"
	f.write(t, file, content)
	var mu sync.Mutex
	models := [][]byte{}
	records := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body", 400)
			return
		}
		if r.URL.Path == "/events" {
			var envelope struct {
				Events []protocol.JSONObject `json:"events"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				http.Error(w, "events", 400)
				return
			}
			mu.Lock()
			records = append(records, envelope.Events...)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "foreign", 400)
			return
		}
		mu.Lock()
		models = append(models, body)
		round := len(models)
		mu.Unlock()
		var block protocol.JSONObject
		if round == 1 {
			block = webObject(`{"type":"tool_use","id":"owned-sdk-read","name":"Read","input":{}}`)
			input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(file)})
			block.Set("input", rawMain(input))
		} else {
			block = webObject(`{"type":"text","text":"SDK_ANALYTICS_DONE"}`)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, webWire("claude-sonnet-4-5-20250929", []protocol.JSONObject{block}))
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	frames := make(chan protocol.JSONObject, 16)
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "claude-sonnet-4-5-20250929", SystemPrompt: "Read the owned file and report its contents.", Tools: []string{"Read"}, AllowedTools: []string{"Read"}, MaxTurns: 3, Sources: State{AllowedSettingSources: []string{"userSettings"}, EnabledSettingSources: []string{"userSettings"}}}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "owned-sdk-analytics", Input: reader, Output: io.Discard, Error: io.Discard,
		OnOutput: func(body []byte) error {
			var row protocol.JSONObject
			if err := row.UnmarshalJSON(body); err != nil {
				return err
			}
			if row.String("type") == "control_response" || row.String("type") == "result" {
				frames <- row
			}
			return nil
		},
		AnalyticsHTTP: func(ctx context.Context, input analytics.Request) (analytics.Response, error) {
			if input.URL != "https://api.anthropic.com/api/event_logging/batch" {
				return analytics.Response{}, fmt.Errorf("unowned actual exporter target: %s", input.URL)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/events", bytes.NewReader(input.Body))
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
	done := make(chan error, 1)
	go func() { _, err := m.Run(ctx); done <- err }()
	t.Cleanup(func() { cancel(); _ = writer.Close(); m.Close() })
	send := func(raw string) {
		t.Helper()
		if _, err := io.WriteString(writer, raw+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	send(`{"type":"update_environment_variables","variables":{"CLAUDE_CONFIG_DIR":"sdk-next"}}`)
	send(`{"type":"control_request","request_id":"sdk-analytics-settings","request":{"subtype":"get_settings"}}`)
	select {
	case frame := <-frames:
		var response protocol.JSONObject
		_ = response.UnmarshalJSON(frame.Get("response"))
		if response.String("subtype") != "success" {
			t.Fatal("real SDK get_settings failed", mustMigrationJSON(frame))
		}
		var body, effective, future protocol.JSONObject
		_ = body.UnmarshalJSON(response.Get("response"))
		_ = effective.UnmarshalJSON(body.Get("effective"))
		_ = future.UnmarshalJSON(effective.Get("future"))
		if future.String("directory") != "NEXT" {
			t.Fatal("SDK did not consume new settings directory", mustMigrationJSON(frame))
		}
	case err := <-done:
		t.Fatal("SDK ended before actual ACK", err)
	case <-ctx.Done():
		t.Fatal("SDK ACK deadline", ctx.Err())
	}
	send(string(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Read the owned file: " + file}})))
	select {
	case frame := <-frames:
		if frame.String("type") != "result" || frame.String("subtype") != "success" {
			t.Fatal("real Read/next model result", mustMigrationJSON(frame))
		}
	case err := <-done:
		t.Fatal("SDK ended before Read/next model result", err)
	case <-ctx.Done():
		t.Fatal("Read/model deadline", ctx.Err())
	}
	_ = writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	m.Close()
	mu.Lock()
	actualModels := append([][]byte(nil), models...)
	actualRecords := append([]protocol.JSONObject(nil), records...)
	mu.Unlock()
	if len(actualModels) != 2 || !strings.Contains(string(actualModels[1]), "ACTUAL_SDK_ANALYTICS_READ") {
		t.Fatalf("actual two model requests lost Read result: count=%d bodies=%s", len(actualModels), actualModels)
	}
	fileEvents := []protocol.JSONObject{}
	for _, row := range actualRecords {
		var data protocol.JSONObject
		if err := data.UnmarshalJSON(row.Get("event_data")); err != nil {
			t.Fatal(err)
		}
		if data.String("event_name") == "tengu_file_operation" {
			fileEvents = append(fileEvents, data)
		}
	}
	if len(fileEvents) != 1 {
		t.Fatal("SDK environment/get_settings dropped real first-party file event", len(fileEvents), len(actualRecords))
	}
	if fileEvents[0].String("model") != "claude-sonnet-4-5-20250929" || fileEvents[0].String("betas") != "claude-code-20250219" {
		t.Fatal("real file metadata fields", mustMigrationJSON(fileEvents[0]))
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != content {
		t.Fatal("owned Read mutated file", err)
	}
}
