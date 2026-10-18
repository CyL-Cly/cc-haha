package modelaccounting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestTerminalSwitchCostsOriginalSaveResetAndLaterActualAttempt(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	env := map[string]string{"ANTHROPIC_API_KEY": "fake-terminal-cost", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "ANTHROPIC_MODEL": "claude-sonnet-4-6"}
	usage := ZeroUsage()
	usage.Set("input_tokens", raw(30))
	usage.Set("output_tokens", raw(9))
	usage.Set("cache_read_input_tokens", raw(7))
	usage.Set("cache_creation_input_tokens", raw(3))
	usage.Set("server_tool_use", raw(map[string]int{"web_search_requests": 2, "web_fetch_requests": 0}))
	charge := map[string]any{"kind": "charge", "model": "claude-sonnet-4-6", "usage": usage, "added": 4, "removed": 2, "decode": 80, "ttft": 19, "api": 122, "apiWithoutRetries": 111, "elapsed": 300}
	ops := []any{charge, map[string]any{"kind": "save"}, map[string]any{"kind": "reset"}, charge, map[string]any{"kind": "save"}}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-switch-cost.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SWITCH_COST_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "TZ=UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "environment": env, "operations": ops})
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Output       []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Output) != 3 || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("Invalid actual Source evidence", err, string(output))
	}
	owner := cooperative.New()
	defer owner.Close()
	requestID := "keep-original-request"
	ledger, _ := NewOwnedSession(owner, &requestID)
	binding, _ := NewMainContext(owner)
	clock := 1000.0
	settings, global := protocol.NewJSONObject(), protocol.NewJSONObject()
	getSettings := func() (*protocol.JSONObject, error) { return &settings, nil }
	getGlobal := func() (*protocol.JSONObject, error) { return &global, nil }
	resolver := RestoredModelResolver{Environment: env, Settings: getSettings, Global: getGlobal}
	p := CostPresentation{NowMillis: func() float64 { return clock }, StartMillis: 1000, CanonicalName: resolver.Canonical}
	compare := func(want, got protocol.JSONObject) {
		t.Helper()
		var a, b any
		_ = json.Unmarshal(raw(want), &a)
		_ = json.Unmarshal(raw(got), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("actual Source %s\nGo %s", raw(want), raw(got))
		}
	}
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := ledger.InstallNativeCalculator(scope, func(model string, environment map[string]string) (*Calculator, error) {
			return NewNativeCalculator(model, NativeCalculatorOptions{Environment: environment, Settings: getSettings, Global: getGlobal})
		}); err != nil {
			return err
		}
		input := originalAccountingInput("claude-sonnet-4-6", "repl_main_thread")
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		// This attempt is really created before reset and finishes afterwards.
		// Source finally still mutates its original process singleton.
		late, err := ledger.BeginRequest(scope, input, binding)
		if err != nil {
			return err
		}
		apply := func(attempt *Attempt) error {
			attempt.fallbackUsage = &usage
			if err := attempt.Finish(scope, true, nil); err != nil {
				return err
			}
			if err := ledger.addDuration(scope, 122, 111); err != nil {
				return err
			}
			if err := ledger.AddGenerationDuration(scope, 80, 19); err != nil {
				return err
			}
			p.LinesAdded, p.LinesRemoved = 4, 2
			clock += 300
			return nil
		}
		first, err := ledger.BeginRequest(scope, input, binding)
		if err != nil {
			return err
		}
		if err = apply(first); err != nil {
			return err
		}
		fields, err := ledger.PersistedCostFields(scope, "source-current-session", p)
		if err != nil {
			return err
		}
		want, _ := source.Output[0].Object("fields")
		compare(want, fields)
		if err = ledger.ResetForTerminalSwitch(scope, p.NowMillis); err != nil {
			return err
		}
		p.LinesAdded, p.LinesRemoved = 0, 0
		got, err := ledger.UsageSnapshot(scope, p)
		if err != nil {
			return err
		}
		want, _ = source.Output[1].Object("usage")
		compare(want, got)
		if ledger.lastRequestID == nil || *ledger.lastRequestID != source.Output[1].String("lastRequestId") {
			t.Error("reset replaced original lastMainRequestId")
		}
		if err = apply(late); err != nil {
			return err
		}
		fields, err = ledger.PersistedCostFields(scope, "source-current-session", p)
		if err != nil {
			return err
		}
		want, _ = source.Output[2].Object("fields")
		compare(want, fields)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := ledger.PersistedCostFields(scope, "x", p); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign save admitted", err)
		}
		if err := ledger.ResetForTerminalSwitch(scope, p.NowMillis); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign reset admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
