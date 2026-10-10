package cliglobalconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestGetOrCreateUserIDAfterActualSourceExternalRefresh(t *testing.T) {
	for _, initial := range []string{`{}`, `{"userID":""}`, `{"userID":null}`, `{"userID":false}`, `{"userID":0}`, `{"userID":-0}`, `{"userID":"owned-existing-device"}`, `{"userID":42}`, `{"userID":true}`, `{"userID":[]}`, `{"userID":{"future":true}}`} {
		t.Run(initial, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(root, "home")
			if err = os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			sourceRoot := filepath.Join(home, "source")
			if err = os.MkdirAll(sourceRoot, 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../testdata/native-device-id-refresh.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			child.Dir = sourceRoot
			child.Env = []string{"HOME=" + sourceRoot, "USERPROFILE=" + sourceRoot, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceRoot, ".claude"), "TMPDIR=" + sourceRoot, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_DEVICE_ID_REFRESH_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
			input, _ := json.Marshal(map[string]any{"homeDir": sourceRoot, "initial": initial})
			child.Stdin = bytes.NewReader(input)
			var diagnostics bytes.Buffer
			child.Stderr = &diagnostics
			out, err := child.Output()
			if err != nil {
				t.Fatal("actual Source identity", err, diagnostics.String())
			}
			var oracle map[string]any
			if err = json.Unmarshal(out, &oracle); err != nil {
				t.Fatal(err, string(out))
			}
			if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(oracle["second"].(string)) {
				t.Fatal("Source ID not 32-byte hex")
			}
			delete(oracle, "second")
			var watch func(FileStat)
			nativeHome := filepath.Join(home, "go")
			service := enabledService(t, Options{Home: nativeHome, Watcher: func(_ string, interval time.Duration, callback func(FileStat)) func() {
				if interval != time.Second {
					t.Fatal("Original watcher interval changed")
				}
				watch = callback
				return func() {}
			}}, initial)
			first, err := service.GetOrCreateUserID()
			if err != nil {
				t.Fatal(err)
			}
			config, err := service.Get()
			if err != nil {
				t.Fatal(err)
			}
			var initialObject protocol.JSONObject
			_ = initialObject.UnmarshalJSON([]byte(initial))
			firstGenerated := !jsTruthy(initialObject.Get("userID"))
			var firstValue any
			if firstGenerated {
				if !regexp.MustCompile(`^"[0-9a-f]{64}"$`).Match(first) {
					t.Fatal("generated ID shape", string(first))
				}
				firstValue = "<GENERATED32HEX>"
			} else {
				_ = json.Unmarshal(first, &firstValue)
			}
			replacement := []byte(`{"projects":{"owned":{"disabledMcpServers":["owned"]}},"future":{"keep":true}}`)
			if err = os.WriteFile(service.Path(), replacement, 0600); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(time.Second)
			if err = os.Chtimes(service.Path(), future, future); err != nil {
				t.Fatal(err)
			}
			stat, err := service.options.FS.Stat(service.Path())
			if err != nil {
				t.Fatal(err)
			}
			watch(stat)
			second, err := service.GetOrCreateUserID()
			if err != nil {
				t.Fatal(err)
			}
			config, err = service.Get()
			if err != nil {
				t.Fatal(err)
			}
			var preserved, projects any
			_ = json.Unmarshal(config.Get("future"), &preserved)
			_ = json.Unmarshal(config.Get("projects"), &projects)
			actual := map[string]any{"first": firstValue, "firstGenerated": firstGenerated, "generatedHex32": regexp.MustCompile(`^"[0-9a-f]{64}"$`).Match(second), "cached": bytes.Equal(config.Get("userID"), second), "future": preserved, "projects": projects}
			if !reflect.DeepEqual(oracle, actual) {
				t.Fatal("Source/Go identity producer differs", oracle, actual)
			}
			disk, err := os.ReadFile(service.Path())
			if err != nil {
				t.Fatal(err)
			}
			var physical protocol.JSONObject
			_ = physical.UnmarshalJSON(disk)
			if !bytes.Equal(physical.Get("userID"), second) || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(physical.Get("future")), protocol.StringifyValidatedJavaScriptJSON(config.Get("future"))) {
				t.Fatal("actual physical identity/unknown fields missing")
			}
		})
	}
}
