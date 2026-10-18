package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type nativeAgentMemoryDirectorySource struct {
	Initial, Next, NextPhysical, Project, Scope, Body string
	OldDirectory, NewDirectory                        string
	InitialText, NextText, RewrittenText              string
	Disabled, Remote                                  *string
	InitialSettings, NextSettings                     json.RawMessage
	SameWarmResult, DifferentClearedResult            bool
	Stages                                            []nativeAgentMemoryDirectoryStage
	Caps                                              struct{ Blocked, SecureReads []string }
}

type nativeAgentMemoryDirectoryStage struct {
	Phase, Directory, Prompt, SourceFilePath string
	Reads                                    []string
	SameDefinition, SameMergedSettings       bool
}

func actualAgentMemoryDirectorySource(t *testing.T, home, profile string) nativeAgentMemoryDirectorySource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Agent memory Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-agent-memory.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONFIG_AGENT_MEMORY_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=0", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"home": home, "profile": profile}))
	output, err := command.CombinedOutput()
	var source nativeAgentMemoryDirectorySource
	decodeErr := json.Unmarshal(output, &source)
	if err != nil || decodeErr != nil || len(source.Stages) != 5 || !source.SameWarmResult || !source.DifferentClearedResult || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole actual Agent memory Source process=%v json=%v stages=%d warm=%v clear=%v caps=%+v tail %.2000s", err, decodeErr, len(source.Stages), source.SameWarmResult, source.DifferentClearedResult, source.Caps, output[max(0, len(output)-2000):])
	}
	return source
}

func TestActualNativeCachedAgentMemoryUsesCurrentDirectoryContentAndOriginalSettingsAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"user-zero", "user-cache-on", "user-cache-off", "user-disabled", "relative-zero", "empty-zero", "project-zero", "local-zero", "remote-zero"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			source := actualAgentMemoryDirectorySource(t, f.home, profile)
			f.n.Environment["CLAUDE_CONFIG_DIR"] = source.Initial
			f.n.Environment["CLAUDE_CODE_SIMPLE"] = "0"
			f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			if source.Disabled == nil {
				delete(f.n.Environment, "CLAUDE_CODE_DISABLE_AUTO_MEMORY")
			} else {
				f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = *source.Disabled
			}
			if source.Remote != nil {
				f.n.Environment["CLAUDE_CODE_REMOTE_MEMORY_DIR"] = *source.Remote
			}
			f.write(t, filepath.Join(source.Initial, "settings.json"), string(source.InitialSettings))
			f.write(t, filepath.Join(source.NextPhysical, "settings.json"), string(source.NextSettings))
			f.write(t, filepath.Join(source.OldDirectory, "MEMORY.md"), source.InitialText)
			if source.NewDirectory != source.OldDirectory {
				f.write(t, filepath.Join(source.NewDirectory, "MEMORY.md"), source.NextText)
			}
			p, err := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			f.n.bindProcessEnvironment(p.environment)
			owner := cooperative.New()
			defer owner.Close()
			service, err := p.AgentCatalog(owner, source.Project, true)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			defer p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial))
			memoryReader, _, err := p.startup.NativeAgentMemorySettingsReader(p.settings, p.settingsState, p.environment)
			if err != nil {
				t.Fatal(err)
			}
			var first *agentcatalog.Result
			var initialDefinition *agentcatalog.Definition
			var cachedSettings any
			capture := func(index int, clear bool) error {
				return owner.Run(ctx, func(scope *cooperative.Scope) error {
					if clear {
						if err := service.Clear(scope); err != nil {
							return err
						}
					}
					current, err := service.List(scope)
					if err != nil {
						return err
					}
					if index == 0 {
						first = current
						cachedSettings = p.settingsState.GetSessionSettingsCache().Settings
					} else if (current == first) != (index < 3) {
						t.Error("actual Agent cache identity differs", source.Stages[index].Phase)
					}
					for i := range current.All {
						definition := &current.All[i]
						if definition.Name != "memory-worker" {
							continue
						}
						if index == 0 {
							initialDefinition = definition
						}
						stage := source.Stages[index]
						prompt, err := service.Prompt(scope, *definition, agentbuiltinmetadata.GuideContext{})
						if err != nil {
							return err
						}
						if _, err := memoryReader(); err != nil {
							return err
						}
						if prompt != stage.Prompt || definition.FilePath != stage.SourceFilePath || (definition == initialDefinition) != stage.SameDefinition || (cachedSettings == p.settingsState.GetSessionSettingsCache().Settings) != stage.SameMergedSettings {
							t.Errorf("%s original memory/cache differs\nGo %.6500s\nSource %.6500s\nGo path %s Source path %s", stage.Phase, prompt, stage.Prompt, definition.FilePath, stage.SourceFilePath)
						}
						return nil
					}
					return mainCap("actual configured memory agent absent")
				})
			}
			if err = capture(0, false); err != nil {
				t.Fatal("initial actual memory consumer", err)
			}
			if err = p.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
				t.Fatal(err)
			}
			if err = capture(1, false); err != nil {
				t.Fatal("changed directory with same actual definition", err)
			}
			f.write(t, filepath.Join(source.NewDirectory, "MEMORY.md"), source.RewrittenText)
			if err = capture(2, false); err != nil {
				t.Fatal("same actual definition reads physical rewrite", err)
			}
			if err = capture(3, true); err != nil {
				t.Fatal("actual Agent clear preserves merged settings", err)
			}
			p.settingsState.ResetSettingsCache()
			if err = capture(4, false); err != nil {
				t.Fatal("actual settings reset with same definition", err)
			}
			if _, err = p.startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR") {
				t.Fatal("ordinary startup guard must remain", err)
			}
		})
	}
}
