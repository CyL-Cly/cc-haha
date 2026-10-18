package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestCLIChromeFlagNoninteractiveEnvironmentAndGlobalPrecedence(t *testing.T) {
	on, off := true, false
	global := nativeInProcessObject([]byte(`{"claudeInChromeDefaultEnabled":true}`))
	for _, row := range []struct {
		flag        *bool
		interactive bool
		env         string
		want        bool
	}{
		{nil, false, "1", false}, {&on, false, "0", true}, {&off, true, "1", false},
		{nil, true, "1", true}, {nil, true, "false", false}, {nil, true, "unknown", true}, {nil, true, "", true},
	} {
		if actual := nativeChromeRequested(row.flag, row.interactive, map[string]string{"CLAUDE_CODE_ENABLE_CFC": row.env}, global); actual != row.want {
			t.Fatal(row, actual)
		}
	}
}

func TestCLIChromeEligibleSubscriberUsesActualAuthPromptConfigAndNativeManifest(t *testing.T) {
	for _, subscriber := range []bool{false, true} {
		t.Run(map[bool]string{false: "api-key-ineligible", true: "subscriber-enabled"}[subscriber], func(t *testing.T) {
			m := openAIAuthMainFixture(t)
			on := true
			m.options.Arguments.Chrome = &on
			m.options.Arguments.AppendSystemPrompt = "OWNED_APPENDIX"
			m.options.Arguments.PermissionMode = "bypassPermissions"
			if subscriber {
				if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					if err := m.environment.SetProperty(quoteMain("ANTHROPIC_API_KEY"), nil); err != nil {
						return err
					}
					if !m.secure.UpdateSourceJSON(context.Background(), rawMain(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "private-chrome-fixture", "scopes": []string{"user:inference"}}})).Success {
						return errors.New("private fixture credentials unavailable")
					}
					return m.openAIAuth.ClearClaudeTokenCache(scope)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.initializeNativeChrome(); err != nil {
				t.Fatal(err)
			}
			m.joinNativeChromeInstallation()
			server := nativeInProcessObject(m.nativeDynamicMCP.Get("claude-in-chrome"))
			if !subscriber {
				if m.nativeDynamicMCP.Get("claude-in-chrome") != nil || m.options.Arguments.AppendSystemPrompt != "OWNED_APPENDIX" || m.chromeInstallation != nil {
					t.Fatal("third party/API key flag bypassed Source subscription entitlement", server)
				}
				return
			}
			if server.String("type") != "stdio" || !strings.Contains(string(server.Get("args")), "--claude-in-chrome-mcp") || nativeInProcessObject(server.Get("env")).String("CLAUDE_CHROME_PERMISSION_MODE") != "skip_all_permission_checks" || m.options.Arguments.AppendSystemPrompt != nativeChromePrompt+"\n\nOWNED_APPENDIX" {
				t.Fatal("actual Chrome startup config or prompt missing", server)
			}
			wrapper := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "chrome", "chrome-native-host")
			body, err := os.ReadFile(wrapper)
			if err != nil || !strings.Contains(string(body), "--chrome-native-host") {
				t.Fatal("actual Chrome wrapper not installed", err, string(body))
			}
			if info, err := os.Stat(wrapper); err != nil || info.Mode().Perm() != 0755 {
				t.Fatal("native wrapper is not executable", info, err)
			}
			for _, base := range chromeBrowserDirectories(m.options.Native.Home, "darwin") {
				path := filepath.Join(base, "NativeMessagingHosts", "com.anthropic.claude_code_browser_extension.json")
				data, err := os.ReadFile(path)
				manifest := nativeInProcessObject(data)
				if err != nil || manifest.String("path") != wrapper || manifest.String("name") != "com.anthropic.claude_code_browser_extension" || !strings.Contains(string(manifest.Get("allowed_origins")), "fcoeoabgfenejglbffodgkkbkcdhcgfn") {
					t.Fatal("actual browser native host manifest missing", path, err, manifest)
				}
			}
		})
	}
}

func TestCLIChromeInstallIsIdempotentAndDetectionChecksOwnedBrowserProfiles(t *testing.T) {
	f := newNativePluginFixture(t)
	if err := installNativeChromeHost(f.n, "darwin", "/private/fixture/claude-haha"); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(f.config, "chrome", "chrome-native-host")
	info, err := os.Stat(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if err := installNativeChromeHost(f.n, "darwin", "/private/fixture/claude-haha"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(wrapper)
	if err != nil || !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("idempotent install rewrote unchanged user host", err)
	}
	if nativeChromeExtensionInstalled(f.n, "darwin") {
		t.Fatal("Chrome extension fabricated without a browser profile")
	}
	extension := filepath.Join(chromeBrowserDirectories(f.home, "darwin")[0], "Profile 2", "Extensions", "fcoeoabgfenejglbffodgkkbkcdhcgfn")
	if err := os.MkdirAll(extension, 0700); err != nil {
		t.Fatal(err)
	}
	if !nativeChromeExtensionInstalled(f.n, "darwin") {
		t.Fatal("actual owned Chrome profile extension was not detected")
	}
	if nativeChromeExtensionInstalled(f.n, "unsupported") || nativeChromeRequested(nil, true, nil, protocol.NewJSONObject()) {
		t.Fatal("unsupported/default Chrome profile advertised integration")
	}
}

func TestCLIChromeBackgroundPositiveCachePreservesOldGlobalAndNeverPersistsNegative(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale-positive-without-local-browser", true: "positive-detected"}[installed], func(t *testing.T) {
			m := openAIAuthMainFixture(t)
			m.options.Interactive = true
			if err := m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
				return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					next.Set("futureChromeFixture", []byte(`{"keep":true}`))
					next.Set("cachedChromeExtensionInstalled", rawMain(!installed))
					return &next, nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			if installed {
				path := filepath.Join(chromeBrowserDirectories(m.options.Native.Home, "darwin")[0], "Default", "Extensions", "fcoeoabgfenejglbffodgkkbkcdhcgfn")
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.initializeNativeChrome(); err != nil {
				t.Fatal(err)
			}
			m.joinNativeChromeInstallation()
			if installed && !nativeChromeExtensionInstalled(m.options.Native, "darwin") {
				t.Fatal("owned extension not detected", m.options.Native.Home)
			}
			if err := m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
				current, err := m.Global.Get()
				if err != nil {
					return err
				}
				future, _ := current.Object("futureChromeFixture")
				if string(current.Get("cachedChromeExtensionInstalled")) != "true" || string(future.Get("keep")) != "true" {
					return fmt.Errorf("Chrome positive-only cache poisoned old shared global: %s", rawMain(current))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if m.nativeDynamicMCP.Get("claude-in-chrome") != nil {
				t.Fatal("fresh detection changed current startup's cached auto enable decision")
			}
		})
	}
}
