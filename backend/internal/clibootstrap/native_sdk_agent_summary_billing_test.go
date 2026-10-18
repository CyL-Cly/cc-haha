package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var sdkAgentSummaryCCH = regexp.MustCompile(`\bcch=[0-9a-f]{5};`)

// Source signs exactly one structured billing text field at final fetch. Only
// that field's request-body checksum may be normalized for cache comparison.
// The raw body/system bytes outside those five hex digits remain unchanged.
func sdkAgentSummaryUnsignedBilling(body []byte) ([]byte, []byte, error) {
	var request protocol.JSONObject
	if err := request.UnmarshalJSON(body); err != nil {
		return nil, nil, err
	}
	system := request.Get("system")
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(system, &blocks); err != nil || len(blocks) == 0 {
		return nil, nil, fmt.Errorf("missing structured billing system")
	}
	billingCount := 0
	for _, block := range blocks {
		if block.String("type") == "text" && strings.HasPrefix(block.String("text"), "x-anthropic-billing-header:") {
			billingCount++
		}
	}
	first := blocks[0]
	if billingCount != 1 || first.String("type") != "text" || !strings.HasPrefix(first.String("text"), "x-anthropic-billing-header: ") || len(first.OwnProperties()) != 2 || first.Get("cache_control") != nil {
		return nil, nil, fmt.Errorf("unique uncached first billing text required")
	}
	text := first.String("text")
	matches := sdkAgentSummaryCCH.FindAllStringIndex(text, -1)
	if len(matches) != 1 {
		return nil, nil, fmt.Errorf("one exact five-hex billing CCH required")
	}
	token := []byte(text[matches[0][0]:matches[0][1]])
	originalJSON := first.Get("text")
	if bytes.Count(body, originalJSON) != 1 || bytes.Count(system, originalJSON) != 1 {
		return nil, nil, fmt.Errorf("billing field literal must occur exactly once")
	}
	if bytes.Count(originalJSON, token) != 1 {
		return nil, nil, fmt.Errorf("billing CCH must have its exact literal encoding")
	}
	unsignedJSON := bytes.Replace(originalJSON, token, []byte("cch=00000;"), 1)
	return bytes.Replace(body, originalJSON, unsignedJSON, 1), bytes.Replace(system, originalJSON, unsignedJSON, 1), nil
}
func sdkAgentSummaryComparableSystem(body []byte) ([]byte, error) {
	_, system, err := sdkAgentSummaryUnsignedBilling(body)
	return system, err
}

func verifySDKAgentSummaryBillingWithActualSource(t *testing.T, ctx context.Context, home string, bodies []string) {
	t.Helper()
	if len(bodies) < 2 {
		t.Fatal("actual parent/fork HTTP bodies", len(bodies))
	}
	// Negative controls cannot normalize missing/duplicate/nonmatching billing,
	// and an unrelated user CCH literal must remain byte-for-byte intact.
	valid := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=fixture; cch=abcde;"},{"type":"text","text":"cached prefix","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"literal cch=abcde;"}]}`
	for _, bad := range []string{
		`{}`, `{"system":null}`, `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=fixture;"}]}`, `{"system":[{"type":"text","text":"unrelated cch=abcde;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde; cch=12345;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde;"},{"type":"text","text":"x-anthropic-billing-header: cch=12345;"}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcde;","cache_control":{"type":"ephemeral"}}]}`,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cch=abcdeg;"}]}`,
	} {
		if _, _, err := sdkAgentSummaryUnsignedBilling([]byte(bad)); err == nil {
			t.Fatal("invalid billing was normalized", bad)
		}
	}
	control, _, err := sdkAgentSummaryUnsignedBilling([]byte(valid))
	if err != nil || !bytes.Contains(control, []byte(`"content":"literal cch=abcde;"`)) || bytes.Count(control, []byte("cch=00000;")) != 1 {
		t.Fatal("normalization escaped unique billing field", string(control), err)
	}
	home, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unsigned := []string{}
	for _, body := range bodies {
		value, _, err := sdkAgentSummaryUnsignedBilling([]byte(body))
		if err != nil {
			t.Fatal("actual request billing", err)
		}
		unsigned = append(unsigned, string(value))
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required for per-body CCH")
	}
	fixture := os.Getenv("CC_HAHA_AGENT_SUMMARY_SOURCE")
	if fixture != "" {
		fixture = filepath.Join(filepath.Dir(fixture), "native-sdk-prompt-suggestion-billing.fixture.ts")
	} else {
		fixture, _ = filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-prompt-suggestion-billing.fixture.ts")
	}
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_SUGGESTION_BILLING_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "ANTHROPIC_API_KEY=fake-billing", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(unsigned))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("actual Source body signing/cache split", err, stderr.String())
	}
	var source struct {
		Signed []string
		Blocks [][]struct {
			Text       string
			CacheScope *string
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &source); err != nil || len(source.Signed) != len(bodies) || len(source.Blocks) != len(bodies) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("Source billing evidence", err, string(data))
	}
	for i, body := range bodies {
		if source.Signed[i] != body || provider.SignClaudeCodeCCHInString(unsigned[i]) != body {
			t.Fatal("per-body CCH diverged from original Source", i)
		}
		var request protocol.JSONObject
		_ = request.UnmarshalJSON([]byte(unsigned[i]))
		var system []protocol.JSONObject
		_ = json.Unmarshal(request.Get("system"), &system)
		if len(source.Blocks[i]) != len(system) {
			t.Fatal("Source split changed system block boundaries", i)
		}
		for j, block := range source.Blocks[i] {
			if block.Text != system[j].String("text") {
				t.Fatal("Source system text projection", i, j)
			}
			if j == 0 {
				if block.CacheScope != nil || system[j].Get("cache_control") != nil {
					t.Fatal("Source billing entered cache prefix", i)
				}
			} else {
				if block.CacheScope == nil || *block.CacheScope != "org" || string(system[j].Get("cache_control")) != `{"type":"ephemeral"}` {
					t.Fatal("Source cached prefix differs", i, j)
				}
			}
		}
	}
	// The exact signed peer bodies and original Source results survive TempDir
	// cleanup in the original Go JSON log; no mutable output fixture is a proof.
	t.Logf("actual_agent_summary_billing_evidence=%s", rawMain(map[string]any{"originalHTTPBodies": bodies, "unsignedHTTPBodies": unsigned, "sourceSignedHTTPBodies": source.Signed, "sourceCacheBlocks": source.Blocks, "sourceStdout": string(data)}))
}
