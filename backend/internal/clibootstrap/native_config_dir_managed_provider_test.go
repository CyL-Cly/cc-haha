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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeConfigProviderSource struct {
	Initial, Second, Third, Project string
	Before                          struct{ Global, CachedLanguage string }
	Stages                          []struct {
		Phase, Current, Global, CachedLanguage string
		Merged, Environment                    map[string]string
	}
	Files            []struct{ Path, Content string }
	Caps             struct{ Blocked, SecureReads []string }
	IndexesUnchanged bool
}

func runNativeConfigProviderSource(t *testing.T, home, profile string) nativeConfigProviderSource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual managed-env Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-managed-provider.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_DIR_PROVIDER_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "profile": profile})
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	var source nativeConfigProviderSource
	if err != nil || json.Unmarshal(output, &source) != nil || len(source.Stages) != 4 || !source.IndexesUnchanged || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole safe/full current-directory Source: %v %.6000s", err, output)
	}
	return source
}

func TestNativeManagedProviderCurrentDirectoryAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"ordinary", "desktop", "host-managed", "missing-index", "invalid-index"} {
		t.Run(profile, func(t *testing.T) {
			var source nativeConfigProviderSource
			m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
				source = runNativeConfigProviderSource(t, n.Home, profile)
				for _, file := range source.Files {
					if err := os.WriteFile(file.Path, []byte(file.Content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// Preserve the ordinary full-entry guard. This consumer test
				// removes only the two directory-changing settings producers
				// before construction, then samples the actual native reader.
				for _, file := range []string{filepath.Join(source.Initial, "settings.json"), filepath.Join(source.Project, ".claude", "settings.json")} {
					if err := os.WriteFile(file, []byte(`{"futurePrivate":{"keep":true}}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, stage := range source.Stages {
				if stage.Global != source.Before.Global || stage.CachedLanguage != source.Before.CachedLanguage {
					t.Fatal("Source re-bound unrelated global/settings memo", stage.Phase)
				}
				if stage.Phase == "reread" {
					raw := rawMain(map[string]any{"env": map[string]string{"PROBE_HAHA": "physical-reread", "PROBE_ORDER": "reread", "ANTHROPIC_API_KEY": "reread-stale"}, "futureManaged": map[string]int{"keep": 97}})
					if err := os.WriteFile(filepath.Join(stage.Current, "cc-haha", "settings.json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if profile == "invalid-index" {
					// Main's older construction bookkeeping is outside this
					// uncached reader proof. Restore the exact observed operand
					// immediately before calling that original reader.
					if err := os.WriteFile(filepath.Join(stage.Current, "cc-haha", "providers.json"), []byte("{invalid JSON"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(stage.Current)); err != nil {
						return err
					}
					directory, err := m.nativeSettings.nativeCcHahaConfigDirectory()
					if err != nil || directory != stage.Current {
						return fmt.Errorf("native settings directory %q, Source %q: %v", directory, stage.Current, err)
					}
					index := filepath.Join(stage.Current, "cc-haha", "providers.json")
					indexBefore, indexErr := os.ReadFile(index)
					raw, err := os.ReadFile(filepath.Join(stage.Current, "cc-haha", "settings.json"))
					if err != nil {
						return err
					}
					var file struct{ Env map[string]string }
					if err = json.Unmarshal(raw, &file); err != nil {
						return err
					}
					legacy := protocol.NewJSONObject()
					for key, value := range file.Env {
						legacy.Set(key, quoteMain(value))
					}
					merged, err := m.nativeMergeCcHahaEnvironment(legacy)
					if err != nil {
						return err
					}
					var actual map[string]string
					encoded, _ := merged.MarshalJSON()
					if err = json.Unmarshal(encoded, &actual); err != nil {
						return err
					}
					if !reflect.DeepEqual(actual, stage.Merged) {
						t.Errorf("%s current=%s provider environment\nGo %s\nSource %s", stage.Phase, stage.Current, encoded, rawMain(stage.Merged))
					}
					indexAfter, readErr := os.ReadFile(index)
					if !bytes.Equal(indexBefore, indexAfter) || os.IsNotExist(indexErr) != os.IsNotExist(readErr) {
						t.Errorf("Source uncached provider reader mutated %s index: %v %s", profile, readErr, indexAfter)
					}
					if stage.Current != source.Initial {
						if _, err = m.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
							t.Errorf("unproved ordinary ConfigDir admission changed: %v", err)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
