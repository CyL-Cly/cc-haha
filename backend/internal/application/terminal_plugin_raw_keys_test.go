package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalRawPluginManifest() []byte {
	return []byte(`{"name":"zeta","version":"1.0.0","mcpServers":{"chat":{"type":"http","url":"http://127.0.0.1:9/unexecuted-channel"}},"channels":[{"server":"chat","displayName":"Raw channel","userConfig":{"\ud800":{"type":"string","title":"High A","description":"First original UTF16 property","required":true},"\ud801":{"type":"string","title":"High B","description":"Distinct original UTF16 property","required":true},"\udc00":{"type":"string","title":"Low","description":"Original low surrogate property","required":true},"\ufffd":{"type":"string","title":"Replacement character","description":"Actual replacement character is a separate property","required":true},"$plugin-option:d800":{"type":"string","title":"Literal binding name","description":"Ordinary property colliding with an internal-looking name","required":true},"amount":{"type":"number","title":"Number","description":"Actual numeric conversion","required":true}}}]}`)
}

func terminalRawPluginOldSettings() []byte {
	return []byte(`{"futureUser":{"keep":true},"pluginConfigs":{"zeta@mkt":{"mcpServers":{"chat":{"\ud800":"old-a","\ud801":"old-b","\udc00":"old-c","\ufffd":"old-d","$plugin-option:d800":"old-e","amount":"invalid"}}},"other@mkt":{"options":{"\ud802":"untouched old raw","\ufffd":"separate old replacement"}}}}`)
}

func terminalRawPluginOptionsValid(settings protocol.JSONObject) bool {
	options := obj(obj(obj(obj(settings.Get("pluginConfigs")).Get("zeta@mkt")).Get("mcpServers")).Get("chat"))
	for _, row := range [][2]string{{`"\ud800"`, `"alpha"`}, {`"\ud801"`, `"beta"`}, {`"\udc00"`, `"gamma"`}, {`"\ufffd"`, `"delta"`}, {`"$plugin-option:d800"`, `"epsilon"`}, {`"amount"`, `16`}} {
		value, err := options.GetPropertyJSON([]byte(row[0]))
		if err != nil || string(value) != row[1] {
			return false
		}
	}
	other := obj(obj(obj(settings.Get("pluginConfigs")).Get("other@mkt")).Get("options"))
	value, err := other.GetPropertyJSON([]byte(`"\ud802"`))
	return err == nil && string(value) == `"untouched old raw"` && other.String("�") == "separate old replacement" && string(obj(settings.Get("futureUser")).Get("keep")) == "true"
}

func TestActualCLIPluginRawUTF16SchemaKeysSourceFlowSaveOldFixtureAndNextHumanPTY(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source and verified MCPB reference required")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; actual ConPTY host required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work, config := filepath.Join(home, "work"), filepath.Join(home, ".claude")
	files, _, _ := prepareTerminalBulkFixture(t, work, config)
	files[filepath.Join(work, "market/plugins/zeta/.claude-plugin/plugin.json")] = base64.StdEncoding.EncodeToString(terminalRawPluginManifest())
	files[filepath.Join(config, "settings.json")] = base64.StdEncoding.EncodeToString(terminalRawPluginOldSettings())
	keys := []string{}
	for _, row := range [][2]string{{"old-a", "alpha"}, {"old-b", "beta"}, {"old-c", "gamma"}, {"old-d", "delta"}, {"old-e", "epsilon"}, {"invalid", "0x10"}} {
		for range row[0] {
			keys = append(keys, "\x7f")
		}
		keys = append(keys, "\x1b[200~"+row[1]+"\x1b[201~", "\r")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-reload.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_RELOAD_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "USER_TYPE=external", "COLUMNS=100", "LINES=35"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "entrypoint": "cli", "capturedOptions": "enable", "files": files, "keys": keys}))
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source raw-key flow %v %.6000s", err, output)
	}
	var source struct {
		Settings protocol.JSONObject
		Outcomes [][]string
		Caps     struct{ Blocked []string }
		Mounted  string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Outcomes) != 1 || source.Outcomes[0][0] != "configured" || !terminalRawPluginOptionsValid(source.Settings) || len(source.Caps.Blocked) > 0 || !strings.Contains(source.Mounted, "old-a") {
		t.Fatal("actual Source raw-key/old-fixture save", err, string(output))
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	if err = os.WriteFile(filepath.Join(private, "disposable-captured-options-pty"), []byte("raw"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, python, "testdata/tui_plugin_captured_options_pty.py", binary, private, "raw")
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + filepath.Join(private, ".claude"), "TMPDIR=" + private, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_CAPTURED_PTY_HELPER=raw"}
	capturePublicGoCoverage(t, cmd)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual raw-key CLI %v %.14000s", err, output)
	}
	t.Log(string(output))
}
