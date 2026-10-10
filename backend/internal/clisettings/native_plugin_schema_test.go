package clisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePluginSettingsSchemaAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	cases := []string{
		`{"permissions":{"allow":["Skill(same-\ud800:reader)"],"deny":["Skill(/same-\udc00:*)"],"ask":["Skill(same-\ud800:reader)"]},"future":{"keep":3}}`,
		`{"permissions":{"allow":["Read","Skill(same-\ud800:reader)","Skill(same-\udc00:reader)"]}}`,

		`{"allowedMcpServers":[],"deniedMcpServers":[{"serverName":"owned","extra":true}],"allowManagedMcpServersOnly":false}`,
		`{"allowedMcpServers":[{"serverCommand":["node","--arg"]},{"serverUrl":"https://*.test/*"}]}`,
		`{"allowedMcpServers":[{}, {"serverName":"bad.name","serverUrl":"x"}, {"serverCommand":[]}]}`,
		`{"deniedMcpServers":[{"serverName":false,"serverUrl":"x"},{"serverCommand":[1],"serverName":"x"}]}`,
		`{"deniedMcpServers":[null,false,[],"x"],"allowManagedMcpServersOnly":null}`,
		`{"allowedMcpServers":null,"deniedMcpServers":{},"allowManagedMcpServersOnly":"true"}`,
		`{"allowedMcpServers":[{"serverName":"","serverCommand":[]},{}],"deniedMcpServers":[{"serverName":"normal"}],"model":"x"}`,
		`{}`, `{"enabledPlugins":{"sample@mkt":true,"off@mkt":false,"version@mkt":[">=1.0","<2"]},"agent":"Plan","future":{"keep":true}}`,
		`{"enabledPlugins":{"sample@mkt":[]}}`, `{"enabledPlugins":null}`, `{"enabledPlugins":[]}`, `{"enabledPlugins":{"sample@mkt":1}}`, `{"enabledPlugins":{"sample@mkt":[null]}}`,
		`{"enabledPlugins":{"10":true,"2":false,"__proto__":true,"constructor":false,"bad\ud800":true}}`,
		`{"agent":false}`, `{"agent":null}`, `{"agent":""}`, `{"agent":"Plan\ud800"}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"directory","path":"/tmp/fixture"},"autoUpdate":true,"future":"strip"}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"github","repo":"owner/market","sparsePaths":["plugins"]},"installLocation":"cache"}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"mkt","plugins":[]}}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"other","plugins":[]}}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"other","plugins":[]},"autoUpdate":"bad"}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"wrong name","plugins":[]}}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"","plugins":[]}}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"anthropic-plugins","plugins":[]}}}}`,
		`{"extraKnownMarketplaces":{"mkt":{"source":{"source":"settings","name":"other","plugins":[{"name":"sample","source":"./local"}]}}}}`,
		`{"strictKnownMarketplaces":[{"source":"npm","package":"../../bad"}]}`,
		`{"strictKnownMarketplaces":[{"source":"settings","name":"builtin","plugins":[]}]}`,
		`{"extraKnownMarketplaces":null}`, `{"extraKnownMarketplaces":{"mkt":false}}`, `{"extraKnownMarketplaces":{"mkt":{"source":{"source":"unknown"}}}}`,
		`{"strictKnownMarketplaces":[]}`, `{"strictKnownMarketplaces":[{"source":"git","url":"https://example.test/repo.git","ref":"main"}]}`,
		`{"strictKnownMarketplaces":[{"source":"url","url":"bad"}]}`, `{"blockedMarketplaces":[{"source":"hostPattern","hostPattern":"*.test"},{"source":"pathPattern","pathPattern":"/tmp/*"}]}`,
		`{"blockedMarketplaces":false}`, `{"blockedMarketplaces":[null]}`, `{"extraKnownMarketplaces":{"mkt":{"source":{"source":"directory","path":false},"autoUpdate":"bad"}},"agent":false}`,
		`{"disableAgentSkillsDirectory":true}`, `{"disableAgentSkillsDirectory":false}`, `{"disableAgentSkillsDirectory":null}`,
		`{"disableAgentSkillsDirectory":[]}`, `{"disableAgentSkillsDirectory":0}`, `{"disableAgentSkillsDirectory":"false"}`,
		`{"enabledPlugins":{"sample@mkt":true},"disableAgentSkillsDirectory":false,"future":{"keep":true}}`,
		`{"agent":false,"disableAgentSkillsDirectory":null,"outputStyle":false,"respectGitignore":null}`,
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-settings.fixture.ts")
	input, _ := json.Marshal(map[string]any{"schemas": cases})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_PLUGIN_SETTINGS_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source schema: %v %s", err, output)
	}
	var source struct {
		Schemas []struct {
			Settings *string
			Errors   string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Schemas) != len(cases) {
		t.Fatal("Source schema observations missing")
	}
	for i, content := range cases {
		t.Run(content, func(t *testing.T) {
			got, err := ValidateNativeJSONContent(content, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			want := source.Schemas[i]
			if want.Settings == nil {
				if got.Settings != nil {
					t.Fatal("Source invalid configuration was accepted")
				}
			} else {
				if got.Settings == nil {
					t.Fatal("Source valid configuration was rejected", string(rawJSON(got.Errors)))
				}
				raw, _ := got.Settings.MarshalJSON()
				if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw), protocol.StringifyValidatedJavaScriptJSON([]byte(*want.Settings))) {
					t.Fatalf("settings/order/UTF16 differ: Go %s Source %s", raw, *want.Settings)
				}
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(rawJSON(got.Errors)), protocol.StringifyValidatedJavaScriptJSON([]byte(want.Errors))) {
				t.Fatalf("validation differs: Go %s Source %s", rawJSON(got.Errors), want.Errors)
			}
		})
	}
}

func TestNativePluginSchemaDoesNotBroadenFiniteProfile(t *testing.T) {
	for _, content := range []string{`{"agent":"Plan"}`, `{"enabledPlugins":{}}`, `{"extraKnownMarketplaces":{}}`, `{"strictKnownMarketplaces":[]}`, `{"blockedMarketplaces":[]}`, `{"disableAgentSkillsDirectory":true}`, `{"disableAgentSkillsDirectory":false}`} {
		if _, err := ValidateJSONContent(content, "finite"); err == nil {
			t.Fatal("finite profile was broadened", content)
		}
	}
}
