package httphook

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceHTTPHookPatternHeaderInterpolationAndAddressGuard(t *testing.T) {
	addresses := []string{"0.0.0.0", "0.1.2.3", "10.0.0.1", "127.0.0.1", "127.255.255.254", "100.63.255.255", "100.64.0.0", "100.127.255.255", "100.128.0.0", "169.254.169.254", "172.15.255.255", "172.16.0.0", "172.31.255.255", "172.32.0.0", "192.168.1.1", "192.169.1.1", "8.8.8.8", "255.255.255.255", "::", "::1", "0:0:0:0:0:0:0:0", "0:0:0:0:0:0:0:1", "FC00::1", "fdff::1", "fe7f::1", "fe80::1", "febf::1", "fec0::1", "2001:db8::1", "ff00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:a9fe:a9fe", "0:0:0:0:0:ffff:0a00:0001", "::ffff:8.8.8.8", "::ffff:192.168.1.1%en0", "::ffff:c0a8:0101%en0", "fe80::1%en0", "::%en0", "bad-address", "010.1.2.3", "[::1]"}
	patterns := []string{"*", "http://127.0.0.1/*", "http://127.0.0.1/exact", "http://127.0.0.1/a.b?c", "*😀*", "line", "line*", "line\n*", "", "a**b"}
	urls := []string{"http://127.0.0.1/exact", "http://127.0.0.1/a.b?c", "http://127.0.0.1/aXb?c", "line", "line\n", "line\r\n", "line\r", "line\u2028", "line\nnext", "", "ab", "axxb"}
	values := []string{"Bearer $HTTP_HOOK_FIXTURE_TOKEN", "${HTTP_HOOK_FIXTURE_TOKEN}:$HTTP_HOOK_FIXTURE_DENIED", "$lowercase:${9BAD}:$9BAD:$HTTP_HOOK_FIXTURE_TOKEN_more", "${HTTP_HOOK_FIXTURE_TOKEN", "safe\r\n\x00value", "$HTTP_HOOK_FIXTURE_INJECTION", "$HTTP_HOOK_FIXTURE_MISSING"}
	environment := map[string]string{"HTTP_HOOK_FIXTURE_TOKEN": "fixture-secret-only", "HTTP_HOOK_FIXTURE_DENIED": "not-authorized", "HTTP_HOOK_FIXTURE_INJECTION": "a\r\n\x00b"}
	allowed := []string{"HTTP_HOOK_FIXTURE_TOKEN", "HTTP_HOOK_FIXTURE_INJECTION", "HTTP_HOOK_FIXTURE_MISSING"}
	rows := []map[string]any{}
	type result struct {
		URLMatches []bool            `json:"urlMatches"`
		Values     []json.RawMessage `json:"values"`
		Blocked    []bool            `json:"blocked"`
	}
	native := []result{}
	quote := func(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
	for _, url := range urls {
		row := map[string]any{"url": url, "patterns": patterns, "values": values, "environment": environment, "allowed": allowed, "addresses": addresses}
		rows = append(rows, row)
		out := result{URLMatches: []bool{}, Values: []json.RawMessage{}, Blocked: []bool{}}
		for _, pattern := range patterns {
			match, err := URLMatchesPattern(quote(url), quote(pattern))
			if err != nil {
				t.Fatal(err)
			}
			out.URLMatches = append(out.URLMatches, match)
		}
		allowRaw := []json.RawMessage{}
		for _, value := range allowed {
			allowRaw = append(allowRaw, quote(value))
		}
		for _, value := range values {
			expanded, err := InterpolateEnvVars(quote(value), allowRaw, environment)
			if err != nil {
				t.Fatal(err)
			}
			out.Values = append(out.Values, expanded)
		}
		for _, address := range addresses {
			out.Blocked = append(out.Blocked, IsBlockedAddress(address))
		}
		native = append(native, out)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-http-hook-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_HOOK_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct{ Rows []result }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(native) {
		t.Fatal(err, string(out))
	}
	for i, got := range native {
		if !reflect.DeepEqual(got.URLMatches, source.Rows[i].URLMatches) {
			t.Errorf("Source URL %q native%v Source%v", urls[i], got.URLMatches, source.Rows[i].URLMatches)
		}
		if !reflect.DeepEqual(got.Blocked, source.Rows[i].Blocked) {
			for j, value := range got.Blocked {
				if value != source.Rows[i].Blocked[j] {
					t.Errorf("Source address %q native%v Source%v", addresses[j], value, source.Rows[i].Blocked[j])
				}
			}
		}
		for j, value := range got.Values {
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(value), protocol.StringifyValidatedJavaScriptJSON(source.Rows[i].Values[j])) {
				t.Errorf("Source header %q native%s Source%s", values[j], value, source.Rows[i].Values[j])
			}
		}
	}
}
