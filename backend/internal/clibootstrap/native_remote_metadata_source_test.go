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
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSourceCCRMetadataRestoreAliasEmptyModeAndNextModelConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	metadata := []protocol.JSONObject{nativeInProcessObject([]byte(`{"permission_mode":"","is_ultraplan_mode":true,"model":" SonNet "}`)), nativeInProcessObject([]byte(`{"permission_mode":"bypassPermissions","model":"fixture-restored"}`)), nativeInProcessObject([]byte(`{"permission_mode":"unknown-future-mode","model":""}`)), nativeInProcessObject([]byte(`{"permission_mode":null,"model":null,"is_ultraplan_mode":"true","pending_action":{"opaque":true}}`)), nativeInProcessObject([]byte(`{"permission_mode":"plan","is_ultraplan_mode":false,"model":"opusplan"}`))}
	env := map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "source-restore-sonnet", "ANTHROPIC_DEFAULT_OPUS_MODEL": "source-restore-opus", "ANTHROPIC_API_KEY": "fake-restore-auth", "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "CLAUDE_CODE_SIMPLE": "1"}
	allocation, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(allocation, "home")
	os.Mkdir(home, 0700)
	fixture, _ := filepath.Abs("../../testdata/native-remoteio-restore.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_REMOTEIO_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"env": env, "metadata": metadata}))
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Results      []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Capabilities.Blocked) > 0 || len(source.Results) != len(metadata) {
		t.Fatal(err, string(data))
	}
	for i, entry := range metadata {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			for key, value := range env {
				f.n.Environment[key] = value
			}
			var mu sync.Mutex
			requested := []string{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body protocol.JSONObject
				json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				requested = append(requested, body.String("model"))
				mu.Unlock()
				emitPlanFixture(w, 1, "", "RESTORED_NEXT_ASK_OK")
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "prior-fixture-model", PermissionMode: "default", SystemPrompt: "Private restored metadata", Tools: []string{""}, MaxTurns: 3, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			if err = m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			var got protocol.JSONObject
			err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				if err := m.Controller.SetSDKPermissionMode(scope, "plan"); err != nil {
					return err
				}
				if err := m.applyNativeRestoredMetadataOnOwner(scope, entry); err != nil {
					return err
				}
				permission, err := m.Controller.PlanModeContext(scope)
				if err != nil {
					return err
				}
				model, err := m.Host.InferenceModel(scope)
				if err != nil {
					return err
				}
				rawModel := "prior-fixture-model"
				if entry.Get("model") != nil && entry.Get("model")[0] == '"' {
					rawModel = entry.String("model")
				}
				got = protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: permission.Get("mode")}, protocol.JSONField{Name: "prePlanMode", Value: permission.Get("prePlanMode")}, protocol.JSONField{Name: "ultraplan", Value: rawMain(m.nativeUltraplan)}, protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "rawModel", Value: quoteMain(rawModel)})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			json.Unmarshal(rawMain(got), &a)
			expected := source.Results[i].Clone()
			expected.Delete("nextModel")
			json.Unmarshal(rawMain(expected), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("Source restore%s Go%s", rawMain(source.Results[i]), rawMain(got))
			}
			if err = m.RunTurn(ctx, nativeInProcessObject([]byte(`{"type":"user","message":{"role":"user","content":"Use the restored model in this actual next ASK"}}`))); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requested) != 1 || requested[0] != source.Results[i].String("nextModel") {
				t.Fatalf("actual next model request %v Source%s", requested, rawMain(source.Results[i]))
			}
		})
	}
}
