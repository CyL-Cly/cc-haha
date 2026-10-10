package modelaccounting

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestSessionUsageSnapshotReadsActualLedgerAndMatchesSourceCostTracker(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	type usageRecord struct {
		Model             string              `json:"model"`
		Usage             protocol.JSONObject `json:"usage"`
		Added             int                 `json:"added"`
		Removed           int                 `json:"removed"`
		Decode            float64             `json:"decode"`
		TTFT              float64             `json:"ttft"`
		Elapsed           float64             `json:"elapsed"`
		API               float64             `json:"api"`
		APIWithoutRetries float64             `json:"apiWithoutRetries"`
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "fake-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "ANTHROPIC_MODEL": "claude-sonnet-4-6", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"deepseek-flash":1000000}`}
	usage := func(in, out, read, write, search float64) protocol.JSONObject {
		u := ZeroUsage()
		u.Set("input_tokens", raw(in))
		u.Set("output_tokens", raw(out))
		u.Set("cache_read_input_tokens", raw(read))
		u.Set("cache_creation_input_tokens", raw(write))
		u.Set("server_tool_use", raw(map[string]any{"web_search_requests": search, "web_fetch_requests": 0}))
		return u
	}
	records := []usageRecord{
		{"claude-sonnet-4-6", usage(10, 20, 30, 40, 0), 2, 1, 31, 7, 250, 61, 43},
		{"claude-sonnet-4-6", usage(50000, 30000, 12000, 10000, 3), 5, 2, 12, 3, 500, 43, 41},
		{"deepseek-flash", usage(20, 13, 21, 30, 1), 1, 0, 8, 2, 100, 9, 7},
		{"deepseek-flash", usage(0, 0, 0, 0, 0), 0, 0, 0, 0, 25, 0, 0},
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-usage.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_USAGE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "TZ=UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "environment": env, "settings": map[string]any{}, "records": records})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(records)+1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Invalid actual Source evidence", err, source.Capabilities.Blocked, string(output)[:min(800, len(output))])
	}
	owner := cooperative.New()
	defer owner.Close()
	ledger, _ := NewOwnedSession(owner, nil)
	binding, _ := NewMainContext(owner)
	clock := 1000.0
	settings, global := protocol.NewJSONObject(), protocol.NewJSONObject()
	getSettings := func() (*protocol.JSONObject, error) { copy := settings.Clone(); return &copy, nil }
	getGlobal := func() (*protocol.JSONObject, error) { copy := global.Clone(); return &copy, nil }
	resolver := RestoredModelResolver{Environment: env, Settings: getSettings, Global: getGlobal}
	p := CostPresentation{NowMillis: func() float64 { return clock }, StartMillis: 1000, CanonicalName: resolver.Canonical}
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := ledger.InstallNativeCalculator(scope, func(model string, environment map[string]string) (*Calculator, error) {
			return NewNativeCalculator(model, NativeCalculatorOptions{Environment: environment, Settings: getSettings, Global: getGlobal})
		}); err != nil {
			return err
		}
		compare := func(index int) error {
			before, _ := ledger.Snapshot(scope)
			got, err := ledger.UsageSnapshot(scope, p)
			if err != nil {
				return err
			}
			after, _ := ledger.Snapshot(scope)
			if !bytes.Equal(raw(before), raw(after)) {
				t.Fatal("Usage polling changed actual ledger")
			}
			var expected, actual any
			_ = json.Unmarshal(source.Observations[index], &expected)
			_ = json.Unmarshal(raw(got), &actual)
			if !reflect.DeepEqual(expected, actual) {
				t.Errorf("snapshot%d Source%s Go%s", index, source.Observations[index], raw(got))
			}
			return nil
		}
		if err := compare(0); err != nil {
			return err
		}
		for i, row := range records {
			input := originalAccountingInput(row.Model, "sdk")
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			input.Context.Host.Env = env
			attempt, err := ledger.BeginRequest(scope, input, binding)
			if err != nil {
				return err
			}
			message := raw(map[string]any{"type": "message_delta", "usage": row.Usage})
			if err = attempt.Observe(scope, message); err != nil {
				return err
			}
			if err = attempt.Finish(scope, true, nil); err != nil {
				return err
			}
			if err = ledger.AddGenerationDuration(scope, row.Decode, row.TTFT); err != nil {
				return err
			}
			if err = ledger.addDuration(scope, row.API, row.APIWithoutRetries); err != nil {
				return err
			}
			p.LinesAdded += row.Added
			p.LinesRemoved += row.Removed
			clock += row.Elapsed
			if err = compare(i + 1); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
