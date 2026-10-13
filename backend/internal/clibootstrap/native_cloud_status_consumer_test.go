package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type cloudStatusOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (output *cloudStatusOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.Write(data)
}
func (output *cloudStatusOutput) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return bytes.Clone(output.data.Bytes())
}
func TestActualVertexRefreshPrivateADCCommandCacheSDKEventsAndUnsubscribe(t *testing.T) {
	output := &cloudStatusOutput{}
	var path, marker string
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(native *NativeBoundary) {
		path = filepath.Join(native.CWD, "private-google-adc.json")
		marker = filepath.Join(native.CWD, "refresh-count")
		if err := os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"broken-before-refresh"}`), 0600); err != nil {
			t.Fatal(err)
		}
		valid := `{"type":"authorized_user","client_id":"CLIENT_PRIVATE","client_secret":"SECRET_PRIVATE","refresh_token":"REFRESH_PRIVATE","future":{"kept":true}}`
		command := "printf r >> " + settingsShellQuote(marker) + "; printf '%s' " + settingsShellQuote(valid) + " > " + settingsShellQuote(path) + "; printf '  PRIVATE_BROWSER_CHALLENGE\\n'"
		if err := os.WriteFile(filepath.Join(native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"gcpAuthRefresh": command, "future": true}), 0600); err != nil {
			t.Fatal(err)
		}
		native.Environment["CLAUDE_CODE_USE_VERTEX"] = "1"
		native.Environment["GOOGLE_APPLICATION_CREDENTIALS"] = path
		native.Environment["GOOGLE_CLOUD_PROJECT"] = "PRIVATE_PROJECT"
		native.Environment["ANTHROPIC_VERTEX_BASE_URL"] = "http://127.0.0.1:1"
	}, func(options *MainPrintOptions) {
		options.Arguments.Tools = []string{"Read"}
		options.Arguments.EnableAuthStatus = true
		options.Output = output
		options.OnOutput = func(data []byte) error { _, err := output.Write(data); return err }
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		binding, err := m.nativeVertexBinding(scope)
		if err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			client, err := binding.Client(ctx, "fixture-model")
			if err != nil {
				return err
			}
			if client.Config == nil || client.Config.Headers == nil {
				return fmt.Errorf("Vertex lost actual Google ADC token consumer")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "r" {
		t.Fatal("one-hour refresh cache lost actual producer", string(raw), err)
	}
	if _, cached := m.gcpCredentials.Cached(); !cached {
		t.Fatal("GCP refresh cache never settled")
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	before := output.Bytes()
	rows := []protocol.JSONObject{}
	for _, line := range bytes.Split(bytes.TrimSpace(before), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var row protocol.JSONObject
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err, string(before))
		}
		if row.String("type") == "auth_status" {
			rows = append(rows, row)
		}
	}
	if len(rows) != 3 || string(rows[0].Get("isAuthenticating")) != "true" || string(rows[0].Get("output")) != "[]" || string(rows[2].Get("isAuthenticating")) != "false" || string(rows[2].Get("output")) != "[]" {
		t.Fatalf("actual refresh lifecycle not consumed by SDK FIFO: %s", before)
	}
	var lines []string
	_ = json.Unmarshal(rows[1].Get("output"), &lines)
	if len(lines) != 1 || lines[0] != "PRIVATE_BROWSER_CHALLENGE" {
		t.Fatal("actual subprocess output was not streamed", lines)
	}
	for _, row := range rows {
		if row.String("session_id") != m.Controller.SessionID() || row.String("uuid") == "" {
			t.Fatal("SDK auth status lost originating session", row)
		}
	}
	m.options.CloudAuthStatus.Start()
	if !bytes.Equal(before, output.Bytes()) {
		t.Fatal("retired host still consumed authentication events")
	}
	if _, err := m.gcpCredentials.Begin(func(context.Context) (json.RawMessage, error) {
		t.Fatal("closed GCP cache executed producer")
		return nil, nil
	}); err == nil {
		t.Fatal("closed GCP cache accepted work")
	}
}
