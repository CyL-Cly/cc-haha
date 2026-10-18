package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeAgentColdBuiltin struct {
	Name   string          `json:"name"`
	Model  json.RawMessage `json:"model"`
	Effort json.RawMessage `json:"effort"`
}

type nativeAgentColdSource struct {
	Initial, Next, NextPhysical, Project, NextSettingsPath string
	FlagPath                                               *string
	InitialSettings, NextSettings, MutatedSettings         json.RawMessage
	SourceSettings, ParsedNext                             protocol.JSONObject
	ColdAtHold                                             map[string]bool
	Before, Warm, Cleared                                  []nativeAgentDirectoryRow
	BuiltinBefore, BuiltinWarm, BuiltinCleared             []nativeAgentColdBuiltin
	SameWarmIdentity, DifferentClearedIdentity             bool
	SameSourceCacheIdentity                                bool
	Caps                                                   struct{ Blocked, SecureReads []string }
}

func actualAgentColdSource(t *testing.T, home, profile string) nativeAgentColdSource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned whole first-cold Agent Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-agent-cold.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONFIG_DIR_AGENT_COLD_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=0", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"home": home, "profile": profile}))
	output, err := command.CombinedOutput()
	var source nativeAgentColdSource
	if err != nil || json.Unmarshal(output, &source) != nil || len(source.Before) != 2 || len(source.BuiltinBefore) != 6 || !source.ColdAtHold["userSettings"] || !source.ColdAtHold["projectSettings"] || !source.ColdAtHold["localSettings"] || !source.ColdAtHold["flagSettings"] || !source.SameSourceCacheIdentity || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole first-cold Agent Source %v %.5000s", err, output)
	}
	return source
}

func nativeAgentColdBuiltins(result *agentcatalog.Result) []nativeAgentColdBuiltin {
	rows := []nativeAgentColdBuiltin{}
	for _, definition := range result.All {
		if definition.Source != "built-in" {
			continue
		}
		model, effort := definition.Fields.Get("model"), definition.Fields.Get("effort")
		if model == nil {
			model = json.RawMessage("null")
		}
		if effort == nil {
			effort = json.RawMessage("null")
		}
		rows = append(rows, nativeAgentColdBuiltin{Name: definition.Name, Model: model, Effort: effort})
	}
	return rows
}

func assertNativeAgentColdJSON(t *testing.T, label string, actual, expected any) {
	t.Helper()
	var a, b any
	if json.Unmarshal(rawMain(actual), &a) != nil || json.Unmarshal(rawMain(expected), &b) != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("%s differs Go%s Source%s", label, rawMain(actual), rawMain(expected))
	}
}

func TestActualNativeAgentFirstColdHeldReadSettingsPathSchemaCacheAndOverridesAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"valid", "schema", "relative", "flag"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			source := actualAgentColdSource(t, f.home, profile)
			f.n.Environment["CLAUDE_CONFIG_DIR"] = source.Initial
			f.n.Environment["CLAUDE_CODE_SIMPLE"] = "0"
			f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
			f.write(t, filepath.Join(source.Initial, "settings.json"), string(source.InitialSettings))
			f.write(t, filepath.Join(source.NextPhysical, "settings.json"), string(source.NextSettings))
			settingsState := DefaultState()
			settingsState.FlagSettingsPath = source.FlagPath
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, settingsState)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			f.n.bindProcessEnvironment(p.environment)
			owner := cooperative.New()
			defer owner.Close()
			bindings, err := p.nativeAgentCatalogOptions(owner, source.Project, true)
			if err != nil {
				t.Fatal(err)
			}
			held := &nativeAgentDirectoryHeldFiles{Files: bindings.Files, target: filepath.Join(source.Initial, "agents", "worker.md"), started: make(chan struct{}), release: make(chan struct{})}
			bindings.Files = held
			var release sync.Once
			defer release.Do(func() { close(held.release) })
			service, err := agentcatalog.New(bindings)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			defer p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial))
			var before *agentcatalog.Result
			var rows []nativeAgentDirectoryRow
			done := make(chan error, 1)
			go func() {
				done <- owner.Run(ctx, func(scope *cooperative.Scope) error {
					var err error
					before, err = service.List(scope)
					if err == nil {
						rows, err = nativeAgentDirectoryRows(scope, service, before)
					}
					return err
				})
			}()
			select {
			case <-held.started:
				for name, cold := range source.ColdAtHold {
					_, present := p.settingsState.GetCachedSettingsForSource(name)
					if present == cold {
						t.Errorf("first-cold source %s before flip differs Source: nativePresent=%v SourceCold=%v", name, present, cold)
					}
				}
				err = p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next))
			case <-ctx.Done():
				err = ctx.Err()
			}
			release.Do(func() { close(held.release) })
			loadErr := <-done
			if err != nil || loadErr != nil {
				t.Fatalf("first-cold actual native load: change=%v load=%v", err, loadErr)
			}
			assertNativeAgentDirectoryRows(t, "first-cold custom", rows, source.Before)
			assertNativeAgentColdJSON(t, "first-cold builtin override", nativeAgentColdBuiltins(before), source.BuiltinBefore)
			cached := map[string]*protocol.JSONObject{}
			for name := range source.ColdAtHold {
				value, present := p.settingsState.GetCachedSettingsForSource(name)
				if !present {
					t.Fatalf("complete real Agent load left source cold %s", name)
				}
				cached[name] = value
				assertNativeAgentColdJSON(t, name+" whole validated source", value, json.RawMessage(source.SourceSettings.Get(name)))
			}
			path, err := p.settings.Path("userSettings", "")
			if err != nil || path != f.n.nativePhysicalPath(source.NextSettingsPath) {
				t.Fatalf("actual next settings path Go%s/%v Source%s", path, err, source.NextSettingsPath)
			}
			f.write(t, filepath.Join(source.NextPhysical, "settings.json"), string(source.MutatedSettings))
			if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				warm, err := service.List(scope)
				if err != nil {
					return err
				}
				if warm != before || !source.SameWarmIdentity {
					t.Error("first-cold catalogue warm identity changed")
				}
				assertNativeAgentColdJSON(t, "cached builtin", nativeAgentColdBuiltins(warm), source.BuiltinWarm)
				if err = service.Clear(scope); err != nil {
					return err
				}
				cleared, err := service.List(scope)
				if err != nil {
					return err
				}
				if cleared == before || !source.DifferentClearedIdentity {
					t.Error("full Agent clear retained catalogue result")
				}
				rows, err = nativeAgentDirectoryRows(scope, service, cleared)
				if err != nil {
					return err
				}
				assertNativeAgentDirectoryRows(t, "cleared custom", rows, source.Cleared)
				assertNativeAgentColdJSON(t, "clear keeps builtin settings memo", nativeAgentColdBuiltins(cleared), source.BuiltinCleared)
				for name, value := range cached {
					current, present := p.settingsState.GetCachedSettingsForSource(name)
					if !present || current != value {
						t.Errorf("full Agent clear changed source identity %s", name)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			parsed, err := p.settings.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertNativeAgentColdJSON(t, "parsed file cache survives physical rewrite", parsed.Settings, source.ParsedNext)
			if _, err = p.InitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
				t.Error("ordinary startup host guard changed", err)
			}
		})
	}
}
