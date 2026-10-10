package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHTTPHookURLPolicyInterpolationAndSSRFRangesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	urls := []map[string]string{}
	for _, pattern := range []string{"*", "https://*.example.com/*", "http://127.0.0.1:123/a?b=x", "literal[abc]$", "exact"} {
		for _, target := range []string{"https://a.example.com/path", "http://127.0.0.1:123/a?b=x", "literal[abc]$", "exact", "exact\n", "a\rb", "a\u2028b"} {
			urls = append(urls, map[string]string{"pattern": pattern, "target": target})
		}
	}
	headers := []map[string]any{}
	env := map[string]string{"TOKEN": "fake-value", "MULTI": "one\r\ntwo\x00three", "EMPTY": "", "_VALUE": "private-test"}
	for _, value := range []string{"$TOKEN / ${TOKEN}", "$TOKEN_suffix / $UNKNOWN", "${MULTI}", "literal\r\n\x00value", "${_VALUE} / $lower / ${1WRONG}", "$EMPTY", "$$TOKEN"} {
		for _, allowed := range [][]string{{}, {"TOKEN", "MULTI", "_VALUE", "EMPTY"}} {
			headers = append(headers, map[string]any{"value": value, "allowed": allowed})
		}
	}
	addresses := []string{"0.1.2.3", "10.1.2.3", "100.63.1.1", "100.64.1.1", "100.127.0.1", "100.128.0.1", "127.0.0.1", "127.99.1.2", "169.254.169.254", "172.15.1.2", "172.16.1.2", "172.31.1.2", "172.32.1.2", "192.168.1.1", "8.8.8.8", "::", "::1", "fc00::1", "fdff::1", "fe80::1", "febf::1", "fec0::1", "fe80::1%lo0", "::ffff:169.254.169.254", "::ffff:a9fe:a9fe", "0:0:0:0:0:ffff:7f00:1", "not an IP"}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "httpHelpers": map[string]any{"environment": env, "urls": urls, "headers": headers, "addresses": addresses}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		URLs      []bool
		Headers   []string
		Addresses []bool
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range urls {
		if actual := HookURLMatchesPattern(row["target"], row["pattern"]); actual != source.URLs[i] {
			t.Errorf("URL%d %v Source%v %+v", i, actual, source.URLs[i], row)
		}
	}
	for i, row := range headers {
		allowed := map[string]bool{}
		for _, key := range row["allowed"].([]string) {
			allowed[key] = true
		}
		if actual := InterpolateHookHeader(row["value"].(string), allowed, env); actual != source.Headers[i] {
			t.Errorf("header%d %q Source%q", i, actual, source.Headers[i])
		}
	}
	actual := []bool{}
	for _, address := range addresses {
		actual = append(actual, BlockedHookAddress(address))
	}
	if !reflect.DeepEqual(actual, source.Addresses) {
		t.Errorf("SSRF Go%v Source%v", actual, source.Addresses)
	}
}
