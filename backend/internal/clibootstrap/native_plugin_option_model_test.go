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
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestSavedPluginOptionCodeUnitsReachActualSlashShellAndNextModel(t *testing.T) {
	f := newNativePluginFixture(t)
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["ANTHROPIC_API_KEY"] = "owned-loopback-option-key"
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0","userConfig":{"region":{"type":"string","title":"Region","description":"Raw saved region"},"token":{"type":"string","title":"Token","description":"Private option","sensitive":true}}}`)
	f.write(t, filepath.Join(f.plugin, "skills", "sample", "SKILL.md"), "---\ndescription: Actual raw option and Bash\nallowed-tools: Bash\n---\nRAW_OPTION_START:${user_config.region}:RAW_OPTION_END\n${user_config.token}\n !`printf '%s' 'ACTUAL_OPTION_SHELL' > option-shell.txt; printf '%s' 'SHELL_RESULT_$&'`")
	settings, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	secure, err := securestorage.New(securestorage.Options{Home: f.home, ConfigDir: f.config, ConfigDirExplicit: true, Platform: "linux", Environment: f.n.Environment})
	if err != nil {
		t.Fatal(err)
	}
	options, err := settings.PluginOptions(secure)
	if err != nil {
		t.Fatal(err)
	}
	var values, schema protocol.JSONObject
	_ = values.UnmarshalJSON([]byte(`{"region":"left\ud800middle\udc00中文🙂","token":"PRIVATE_OPTION_MUST_NOT_REACH_MODEL"}`))
	_ = schema.UnmarshalJSON([]byte(`{"region":{"type":"string"},"token":{"type":"string","sensitive":true}}`))
	if err := options.Save(context.Background(), "sample@mkt", values, schema); err != nil {
		t.Fatal(err)
	}
	loaded, err := options.Load(context.Background(), "sample@mkt")
	if err != nil || !bytes.Equal(loaded.Get("region"), values.Get("region")) {
		t.Fatal("saved raw option changed", loaded, err)
	}
	if err := settings.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Establish the actual installed-plugin precondition. The test exercises
	// saved option consumption, rather than an unfinished background install.
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(f.config, "plugins"), Store: storage.New(storage.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(f.plugin, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsedManifest protocol.JSONObject
	if err := parsedManifest.UnmarshalJSON(manifest); err != nil {
		t.Fatal(err)
	}
	parsedManifest.Set("installedAt", quoteMain("2026-10-01T00:00:00.000Z"))
	if err := installed.Add(context.Background(), "sample@mkt", "user", f.plugin, parsedManifest, nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), body...))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"raw-option\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"fixture-model\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ACTUAL_OPTION_NEXT\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Sources: DefaultState(), Tools: []string{"Skill,Bash,Read"}, Model: "fixture-model", MaxTurns: 1, SystemPrompt: "private raw plugin option test"}, Native: f.n, PID: os.Getpid(), Platform: runtime.GOOS, Version: "raw-option-test", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"/sample:sample", "next ordinary human"} {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
		if err := m.RunTurn(ctx, input); err != nil {
			t.Fatal("actual user input", text, err)
		}
	}
	artifact, err := os.ReadFile(filepath.Join(f.n.CWD, "option-shell.txt"))
	if err != nil || string(artifact) != "ACTUAL_OPTION_SHELL" {
		t.Fatal("permission-aware Bash did not produce real artifact", string(artifact), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatal("slash and next human model count", len(requests))
	}
	want, _ := protocol.DecodeJavaScriptJSONString(json.RawMessage(`"RAW_OPTION_START:left\ud800middle\udc00中文🙂:RAW_OPTION_END"`))
	for index, body := range requests {
		if bytes.Contains(body, []byte("PRIVATE_OPTION_MUST_NOT_REACH_MODEL")) {
			t.Fatal("sensitive saved option leaked", index)
		}
		var request protocol.JSONObject
		if err := request.UnmarshalJSON(body); err != nil {
			t.Fatal(err)
		}
		var messages []protocol.JSONObject
		if err := json.Unmarshal(request.Get("messages"), &messages); err != nil {
			t.Fatal(err)
		}
		foundRaw, foundShell, foundSecretMask := false, false, false
		for _, message := range messages {
			var blocks []protocol.JSONObject
			if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
				continue
			}
			for _, block := range blocks {
				if block.String("type") != "text" {
					continue
				}
				units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
				if err != nil {
					t.Fatal(err)
				}
				for at := 0; at+len(want) <= len(units); at++ {
					if slicesEqualOptionUnits(units[at:at+len(want)], want) {
						foundRaw = true
						break
					}
				}
				text := string(utf16.Decode(units))
				foundShell = foundShell || strings.Contains(text, "SHELL_RESULT_$&")
				foundSecretMask = foundSecretMask || strings.Contains(text, "[sensitive option 'token' not available in skill content]")
			}
		}
		if !foundRaw || !foundShell || !foundSecretMask {
			t.Fatal("actual model body lost code units/shell/secret boundary", index, foundRaw, foundShell, foundSecretMask)
		}
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}

func slicesEqualOptionUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if b[i] != v {
			return false
		}
	}
	return true
}
