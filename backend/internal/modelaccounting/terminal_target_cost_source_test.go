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
)

func TestTerminalTargetCostsOriginalCaptureBeforeProjectOverwriteAndLateRestore(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	project := storedProject(t, `{"lastSessionId":"target","lastCost":2.75,"lastAPIDuration":122,"lastAPIDurationWithoutRetries":111,"lastDecodeDuration":80,"lastTtftDuration":19,"lastToolDuration":7,"lastLinesAdded":4,"lastLinesRemoved":2,"lastDuration":200,"lastModelUsage":{"fixture-model":{"inputTokens":30,"outputTokens":9,"cacheReadInputTokens":7,"cacheCreationInputTokens":3,"webSearchRequests":2,"costUSD":2.75}}}`)
	env := map[string]string{"ANTHROPIC_API_KEY": "fake-captured-cost", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210", "ANTHROPIC_MODEL": "fixture-model"}
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
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "environment": env, "operations": []any{map[string]any{"kind": "capture", "sessionId": "target", "project": project}, map[string]any{"kind": "replaceProject", "project": map[string]any{"lastSessionId": "old", "lastCost": 99, "lastModelUsage": map[string]any{}}}, map[string]any{"kind": "reset"}, map[string]any{"kind": "restore"}}}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source target costs", err, string(output))
	}
	var source struct {
		Output       []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Output) != 3 || len(source.Capabilities.Blocked) != 0 || string(source.Output[0].Get("present")) != "true" {
		t.Fatal("actual Source receipt", err, string(output))
	}
	owner := cooperative.New()
	defer owner.Close()
	ledger, _ := NewOwnedSession(owner, nil)
	settings, global := protocol.NewJSONObject(), protocol.NewJSONObject()
	resolver := RestoredModelResolver{Environment: env, Settings: func() (*protocol.JSONObject, error) { return &settings, nil }, Global: func() (*protocol.JSONObject, error) { return &global, nil }}
	var captured *TerminalTargetCosts
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		captured, err = ledger.CaptureTerminalTargetCosts(scope, "target", project, resolver)
		if err != nil || captured == nil {
			t.Fatal(err, captured)
		}
		project.Set("lastCost", raw(99))
		project.Set("lastSessionId", quote("old"))
		project.Set("lastModelUsage", raw(protocol.NewJSONObject()))
		if err = ledger.ResetForTerminalSwitch(scope, func() float64 { return 1000 }); err != nil {
			return err
		}
		if restored, err := ledger.RestoreTerminalTargetCosts(scope, captured, func() float64 { return 1000 }); err != nil || !restored {
			t.Fatal("captured restore rejected", restored, err)
		}
		usage, err := ledger.UsageSnapshot(scope, CostPresentation{NowMillis: func() float64 { return 1000 }, CanonicalName: resolver.Canonical})
		if err != nil {
			return err
		}
		want, _ := source.Output[2].Object("usage")
		var a, b any
		_ = json.Unmarshal(raw(usage), &a)
		_ = json.Unmarshal(raw(want), &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("actual captured Source %s\nGo %s", raw(want), raw(usage))
		}
		other, _ := NewOwnedSession(owner, nil)
		if _, err := other.RestoreTerminalTargetCosts(scope, captured, func() float64 { return 1000 }); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("different same-owner ledger accepted target", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
