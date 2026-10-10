package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSDKGetSettingsFreshDiskAndAppliedEffortOriginalSource(t *testing.T) {
	for _, effortEnvironment := range []string{"", "max", "xhigh", "unset", "120"} {
		t.Run("effort_env_"+effortEnvironment, func(t *testing.T) { runActualSDKSettingsControlSource(t, effortEnvironment) })
	}
}

func runActualSDKSettingsControlSource(t *testing.T, effortEnvironment string) {
	f := newNativePluginFixture(t)
	oldHome := f.home
	home, e := filepath.EvalSymlinks(f.home)
	if e != nil {
		t.Fatal(e)
	}
	f.home = home
	project := filepath.Join(home, "project")
	if e = os.MkdirAll(project, 0700); e != nil {
		t.Fatal(e)
	}
	f.cwd = project
	f.config = filepath.Join(home, ".claude")
	f.n.Home = home
	f.n.CWD = project
	f.n.Environment["HOME"] = home
	f.n.Environment["CLAUDE_CONFIG_DIR"] = f.config
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-settings"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	if effortEnvironment != "" {
		f.n.Environment["CLAUDE_CODE_EFFORT_LEVEL"] = effortEnvironment
	}
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	known, e := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
	if e != nil {
		t.Fatal(e)
	}
	f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), strings.ReplaceAll(string(known), oldHome, home))
	f.write(t, filepath.Join(project, ".git", "HEAD"), "ref: refs/heads/private-sdk-settings\n")
	f.write(t, filepath.Join(project, ".claude", "settings.json"), `{"env":{"SOURCE_ORDER":"project","PROJECT_ONLY":"yes"},"permissions":{"allow":["Read(project)"]}}`)
	f.write(t, filepath.Join(project, ".claude", "settings.local.json"), `{"env":{"SOURCE_ORDER":"local","LOCAL_ONLY":"yes"},"permissions":{"deny":["Read(local-deny)"]}}`)
	flagPath := filepath.Join(home, "flag-settings.json")
	f.write(t, flagPath, `{"env":{"SOURCE_ORDER":"flag","FLAG_ONLY":"yes"}}`)
	sources := DefaultState()
	sources.FlagSettingsPath = &flagPath
	var modelCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		http.Error(w, "unexpected model request", 500)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	rows := []map[string]any{
		{"id": "settings-0", "model": "deepseek-flash[1m]", "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": false}, "env": map[string]any{"SETTINGS_VERSION": "old"}, "permissions": map[string]any{"allow": []string{"Read"}}}},
		{"id": "settings-1", "model": "gpt-5.4", "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": false}, "env": map[string]any{"SETTINGS_VERSION": "new"}, "permissions": map[string]any{"allow": []string{"Read", "Write"}}}},
		{"id": "settings-grok", "model": "grok-4.5", "settings": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": false}}},
		{"id": "settings-2", "model": "deepseek-flash[1m]", "settings": map[string]any{}},
	}
	ctx, finish := context.WithTimeout(context.Background(), 25*time.Second)
	defer finish()
	fixture, _ := filepath.Abs("../../testdata/native-sdk-get-settings.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
	c.Dir = home
	c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_SETTINGS_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "ANTHROPIC_API_KEY=fake-sdk-settings", "ANTHROPIC_BASE_URL=" + peer.URL, "PATH=/usr/bin:/bin"}
	if effortEnvironment != "" {
		c.Env = append(c.Env, "CLAUDE_CODE_EFFORT_LEVEL="+effortEnvironment)
	}
	c.Stdin = bytes.NewReader(rawMain(map[string]any{"rows": rows, "effort": "low", "project": project, "flagPath": flagPath}))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	data, e := c.Output()
	if e != nil {
		t.Fatal("actual Source get_settings", e, stderr.String())
	}
	var source struct{ Frames []json.RawMessage }
	if e = json.Unmarshal(data, &source); e != nil || len(source.Frames) != len(rows) {
		t.Fatal(e, string(data))
	}
	// Return to the first physical fixture before constructing the actual Go host.
	f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(rows[0]["settings"])))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	frames := make(chan json.RawMessage, 16)
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "deepseek-flash[1m]", PermissionMode: "default", MaxTurns: 100400, Sources: sources, EffortLevel: "low"}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error {
		var value map[string]any
		if e := json.Unmarshal(data, &value); e != nil {
			return e
		}
		if value["type"] == "control_response" {
			frames <- append(json.RawMessage{}, data...)
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	runDone := make(chan error, 1)
	runFinished := make(chan struct{})
	go func() { defer close(runFinished); _, err := m.Run(ctx); runDone <- err }()
	t.Cleanup(func() {
		_ = writer.Close()
		select {
		case <-runFinished:
		case <-time.After(3 * time.Second):
			t.Error("input did not finish before cleanup")
		}
		_ = m.ShutdownSession(context.Background())
	})
	send := func(id string, request any) json.RawMessage {
		t.Helper()
		if _, e := writer.Write(append(rawMain(map[string]any{"type": "control_request", "request_id": id, "request": request}), '\n')); e != nil {
			t.Fatal(e)
		}
		select {
		case frame := <-frames:
			return frame
		case e := <-runDone:
			t.Fatal("actual SDK input ended before ACK", e)
		case <-ctx.Done():
			t.Fatal("control ACK missing", id, ctx.Err())
		}
		return nil
	}
	_ = send("init-settings", map[string]any{"subtype": "initialize"})
	for i, row := range rows {
		f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(row["settings"])))
		_ = send("model-settings", map[string]any{"subtype": "set_model", "model": row["model"]})
		actual := send(row["id"].(string), map[string]any{"subtype": "get_settings"})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Frames[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("original Source settings control row %d mismatch\nGo %s\nSource %s", i, actual, source.Frames[i])
		}
	}
	_ = writer.Close()
	select {
	case e := <-runDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("normal EOF", ctx.Err())
	}
	if modelCalls.Load() != 0 {
		t.Fatal("read-only controls invoked model", modelCalls.Load())
	}
}
