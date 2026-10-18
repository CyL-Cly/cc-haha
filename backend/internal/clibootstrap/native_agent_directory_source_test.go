package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeAgentDirectoryRow struct {
	Fields protocol.JSONObject `json:"fields"`
	Prompt string              `json:"prompt"`
}
type nativeAgentDirectorySource struct {
	Initial, Next, NextPhysical, Project                       string
	Before, Warm, Cleared                                      []nativeAgentDirectoryRow
	SameWarmIdentity, DifferentClearedIdentity, MemoryDisabled bool
	PluginCount                                                int
	WarmSourcesBeforeHold                                      bool
	AllSourcesWarm, SameSourceCacheIdentity                    bool
	Caps                                                       struct{ Blocked, SecureReads []string }
}

// Delegate the real ReadFile first, then hold its completion at the same old
// user file as Source. The synchronous project Stat walk is not an async Source
// cutpoint; no synthetic filesystem result or loader body is substituted.
type nativeAgentDirectoryHeldFiles struct {
	agentcatalog.Files
	target           string
	started, release chan struct{}
	once             sync.Once
}

func (f *nativeAgentDirectoryHeldFiles) ReadFile(path string) ([]byte, error) {
	data, err := f.Files.ReadFile(path)
	if path == f.target {
		f.once.Do(func() {
			close(f.started)
			<-f.release
		})
	}
	return data, err
}

func actualAgentDirectorySource(t *testing.T, home, profile string) nativeAgentDirectorySource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned whole Agent directory Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-agents.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONFIG_DIR_AGENTS_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=0", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"home": home, "profile": profile}))
	output, err := command.CombinedOutput()
	var source nativeAgentDirectorySource
	if err != nil || json.Unmarshal(output, &source) != nil || len(source.Before) != 2 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 || !source.MemoryDisabled || source.PluginCount != 0 || !source.AllSourcesWarm || !source.SameSourceCacheIdentity {
		t.Fatalf("whole Agent directory Source %v %.5000s", err, output)
	}
	return source
}

func nativeAgentDirectoryRows(scope *cooperative.Scope, service *agentcatalog.Service, result *agentcatalog.Result) ([]nativeAgentDirectoryRow, error) {
	rows := []nativeAgentDirectoryRow{}
	for _, definition := range result.All {
		if definition.Source != "userSettings" && definition.Source != "projectSettings" {
			continue
		}
		prompt, err := service.Prompt(scope, definition, agentbuiltinmetadata.GuideContext{})
		if err != nil {
			return nil, err
		}
		rows = append(rows, nativeAgentDirectoryRow{definition.Fields.Clone(), prompt})
	}
	return rows, nil
}

func assertNativeAgentDirectoryRows(t *testing.T, label string, got, want []nativeAgentDirectoryRow) {
	t.Helper()
	var actual, expected any
	if json.Unmarshal(rawMain(got), &actual) != nil || json.Unmarshal(rawMain(want), &expected) != nil || !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s whole Agent fields/prompt differ\nGo%s\nSource%s", label, rawMain(got), rawMain(want))
	}
}

func TestActualNativeAgentCurrentDirectoryWarmIdentityAndFullClearAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"between", "held-after-sampling", "relative"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			source := actualAgentDirectorySource(t, f.n.Home, profile)
			f.n.Environment["CLAUDE_CONFIG_DIR"] = source.Initial
			f.n.Environment["CLAUDE_CODE_SIMPLE"] = "0"
			f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
			f.write(t, filepath.Join(source.Initial, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			f.n.bindProcessEnvironment(p.environment)
			owner := cooperative.New()
			defer owner.Close()
			var held *nativeAgentDirectoryHeldFiles
			bindings, err := p.nativeAgentCatalogOptions(owner, source.Project, true)
			if err != nil {
				t.Fatal(err)
			}
			var release sync.Once
			if profile == "held-after-sampling" {
				held = &nativeAgentDirectoryHeldFiles{Files: bindings.Files, started: make(chan struct{}), release: make(chan struct{})}
				bindings.Files = held
				defer release.Do(func() { close(held.release) })
			}
			settings := bindings.Settings
			bindings.Settings = func(source string) (*protocol.JSONObject, error) {
				value, err := settings(source)
				if err != nil {
					return value, fmt.Errorf("Agent settings %s: %w", source, err)
				}
				return value, nil
			}
			plugins := bindings.Plugins
			bindings.Plugins = func(scope *cooperative.Scope) ([]agentcatalog.Definition, error) {
				value, err := plugins(scope)
				if err != nil {
					return value, fmt.Errorf("Agent plugin producer: %w", err)
				}
				return value, nil
			}
			service, err := agentcatalog.New(bindings)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			defer p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial))
			if held != nil {
				if !source.WarmSourcesBeforeHold {
					t.Fatal("held Source did not warm its sources through a real completed load")
				}
				if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
					if _, err := service.List(scope); err != nil {
						return err
					}
					return service.Clear(scope)
				}); err != nil {
					t.Fatal(err)
				}
				held.target = filepath.Join(source.Initial, "agents", "worker.md")
			}
			var before *agentcatalog.Result
			var beforeRows []nativeAgentDirectoryRow
			loadBefore := func() error {
				return owner.Run(ctx, func(scope *cooperative.Scope) error {
					var err error
					before, err = service.List(scope)
					if err == nil {
						beforeRows, err = nativeAgentDirectoryRows(scope, service, before)
					}
					return err
				})
			}
			if held != nil {
				done := make(chan error, 1)
				go func() { done <- loadBefore() }()
				select {
				case <-held.started:
					if err := p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
						release.Do(func() { close(held.release) })
						<-done
						t.Fatal(err)
					}
				case <-ctx.Done():
					release.Do(func() { close(held.release) })
					<-done
					t.Fatal("real user ReadFile did not reach held completion", ctx.Err())
				}
				release.Do(func() { close(held.release) })
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			} else if err = loadBefore(); err != nil {
				t.Fatal(err)
			}
			assertNativeAgentDirectoryRows(t, "before", beforeRows, source.Before)
			sourceSettings := map[string]*protocol.JSONObject{}
			for _, source := range bindings.EnabledSources {
				value, present := p.settingsState.GetCachedSettingsForSource(source)
				if !present {
					t.Fatalf("real Agent load did not warm source %s", source)
				}
				sourceSettings[source] = value
			}
			if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				if err = p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
					return err
				}
				warm, err := service.List(scope)
				if err != nil {
					return err
				}
				if before != warm || !source.SameWarmIdentity {
					t.Error("same-cwd warm Agent cache identity changed")
				}
				rows, err := nativeAgentDirectoryRows(scope, service, warm)
				if err != nil {
					return err
				}
				assertNativeAgentDirectoryRows(t, "warm", rows, source.Warm)
				if err = service.Clear(scope); err != nil {
					return err
				}
				cleared, err := service.List(scope)
				if err != nil {
					return err
				}
				if before == cleared || !source.DifferentClearedIdentity {
					t.Error("full-clear Agent cache identity retained")
				}
				rows, err = nativeAgentDirectoryRows(scope, service, cleared)
				if err != nil {
					return err
				}
				assertNativeAgentDirectoryRows(t, "cleared", rows, source.Cleared)
				for source, original := range sourceSettings {
					current, present := p.settingsState.GetCachedSettingsForSource(source)
					if !present || current != original {
						t.Errorf("Agent clear changed per-source settings cache identity %s", source)
					}
				}
				if _, err = p.InitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
					t.Error("ordinary dynamic directory guard changed", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
