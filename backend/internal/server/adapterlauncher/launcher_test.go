package adapterlauncher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	config := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	return Options{HomeDir: home, WorkDir: home, Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, Stderr: &bytes.Buffer{}}
}

func TestLauncherConsumesOriginalDesktopModeArgumentsAndKeepsMissingValueSemantics(t *testing.T) {
	invocation := Parse([]string{"--telegram", "--app-root", "/private/app.asar", "--telegram", "--unknown", ""}, map[string]string{})
	if invocation.AppRoot == nil || *invocation.AppRoot != "/private/app.asar" || len(invocation.Enabled) != 1 || !invocation.Enabled["--telegram"] || len(invocation.Warnings) != 2 {
		t.Fatalf("invocation=%+v", invocation)
	}
	if result := Parse([]string{"--app-root", "--telegram"}, map[string]string{}); result.AppRoot == nil || *result.AppRoot != "--telegram" || len(result.Enabled) != 0 {
		t.Fatalf("consumed flag was enabled: %+v", result)
	}
	if result := Parse([]string{"--telegram", "--app-root"}, map[string]string{"CLAUDE_APP_ROOT": "previous"}); result.AppRoot != nil {
		t.Fatalf("missing value did not clear old appRoot: %+v", result)
	}
}

func TestEveryMissingCredentialUsesTheOriginalSkipGateWithoutPlatformStartup(t *testing.T) {
	for _, adapter := range adapters {
		t.Run(adapter.key, func(t *testing.T) {
			options := fixture(t)
			code, err := Run(context.Background(), []string{"--app-root", "/private/app.asar", adapter.flag}, options)
			if code != 1 || err != nil {
				t.Fatalf("code=%d error=%v", code, err)
			}
			output := options.Stderr.(*bytes.Buffer).String()
			if !strings.Contains(output, adapter.missing) || !strings.Contains(output, "no adapter could be started") || strings.Contains(output, "unknown option") {
				t.Fatalf("gate=%s", output)
			}
			if _, err := os.Stat(filepath.Join(options.Environment["CLAUDE_CONFIG_DIR"], "adapters.json")); !os.IsNotExist(err) {
				t.Fatalf("gate wrote config: %v", err)
			}
		})
	}
}

func TestConfiguredPlatformsFailWithTypedCapabilityAndPreserveOldUnknownFields(t *testing.T) {
	for _, adapter := range adapters {
		t.Run(adapter.key, func(t *testing.T) {
			options := fixture(t)
			section := map[string]any{adapter.first: "fake-one", adapter.second: "fake-two"}
			if adapter.key == "whatsapp" {
				auth := filepath.Join(options.HomeDir, "auth")
				if err := os.Mkdir(auth, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(auth, "creds.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				section = map[string]any{"authDir": auth}
			}
			raw, _ := json.Marshal(map[string]any{adapter.key: section, "future": map[string]any{"keep": 17}})
			path := filepath.Join(options.Environment["CLAUDE_CONFIG_DIR"], "adapters.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			code, err := Run(context.Background(), []string{adapter.flag}, options)
			var capability *CapabilityError
			if code != 1 || !errors.As(err, &capability) || capability.Adapter != adapter.label {
				t.Fatalf("code=%d err=%v", code, err)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("legacy config changed: %s %v", after, err)
			}
		})
	}
}

func TestSourceCredentialTruthinessAndWhatsAppExistingNativePath(t *testing.T) {
	options := fixture(t)
	for _, value := range []string{`null`, `false`, `0`, `-0`, `0e0`, `""`} {
		if jsonTruthy(json.RawMessage(value)) {
			t.Fatalf("truthy %s", value)
		}
	}
	for _, value := range []string{`true`, `[]`, `{}`, `" "`, `1e400`, `"\ud800"`} {
		if !jsonTruthy(json.RawMessage(value)) {
			t.Fatalf("falsey %s", value)
		}
	}
	options.Environment["TELEGRAM_BOT_TOKEN"] = " "
	_, err := Run(context.Background(), []string{"--telegram"}, options)
	var capability *CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("whitespace env incorrectly treated as absent: %v", err)
	}
	if !reflect.DeepEqual(options.Environment, map[string]string{"HOME": options.HomeDir, "CLAUDE_CONFIG_DIR": filepath.Join(options.HomeDir, ".claude"), "TELEGRAM_BOT_TOKEN": " "}) {
		t.Fatal("caller environment mutated")
	}
	delete(options.Environment, "TELEGRAM_BOT_TOKEN")
	options.Environment["WHATSAPP_AUTH_DIR"] = "\uFEFF~/auth\uFEFF"
	if err := os.MkdirAll(filepath.Join(options.HomeDir, "auth/creds.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), []string{"--whatsapp"}, options)
	if !errors.As(err, &capability) || capability.Adapter != "WhatsApp" {
		t.Fatalf("existing directory did not follow existsSync: %v", err)
	}
}

func TestActualSourceUnconfiguredLauncher(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source oracle requires pinned Bun1.3.14")
	}
	source, err := filepath.Abs("../../../../desktop/sidecars/claude-sidecar.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"--app-root", "/private/app.asar"}, {"--unknown", "--telegram"}, {"--app-root", "/private/app.asar", "--telegram"}, {"--app-root", "/private/app.asar", "--feishu", "--dingtalk", "--wechat", "--wecom", "--qq", "--slack"}, {"--whatsapp"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			options := fixture(t)
			goCode, goErr := Run(context.Background(), args, options)
			if goErr != nil {
				t.Fatal(goErr)
			}
			command := exec.Command(bun, append([]string{"--feature=TRANSCRIPT_CLASSIFIER", source, "adapters"}, args...)...)
			command.Dir = options.WorkDir
			command.Env = []string{"HOME=" + options.HomeDir, "USERPROFILE=" + options.HomeDir, "CLAUDE_CONFIG_DIR=" + options.Environment["CLAUDE_CONFIG_DIR"], "PATH=" + os.Getenv("PATH"), "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NO_PROXY=*"}
			var out, stderr bytes.Buffer
			command.Stdout = &out
			command.Stderr = &stderr
			err := command.Run()
			sourceCode := 0
			if exit, ok := err.(*exec.ExitError); ok {
				sourceCode = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if sourceCode != goCode || stderr.String() != options.Stderr.(*bytes.Buffer).String() {
				t.Fatalf("Source code=%d Go=%d\nSource=%s\nGo=%s\nstdout=%s", sourceCode, goCode, stderr.String(), options.Stderr.(*bytes.Buffer).String(), out.String())
			}
		})
	}
}
