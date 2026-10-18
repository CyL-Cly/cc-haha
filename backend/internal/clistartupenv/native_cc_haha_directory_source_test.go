package clistartupenv

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCcHahaDirectoryReaderAgainstWholeManagedEnvSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned current-directory Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-managed-provider.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"ordinary", "desktop", "host-managed"} {
		t.Run(profile, func(t *testing.T) {
			s, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
			home := filepath.Dir(s.options.ConfigHome)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + s.options.ConfigHome, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_DIR_PROVIDER_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"home": home, "profile": profile, "providers": false})
			command.Stdin = bytes.NewReader(input)
			output, err := command.CombinedOutput()
			var source struct {
				Initial string
				Stages  []struct {
					Phase, Current string
					Merged         map[string]string
				}
				Files []struct{ Path, Content string }
				Caps  struct{ Blocked, SecureReads []string }
			}
			if err != nil || json.Unmarshal(output, &source) != nil || len(source.Stages) != 4 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
				t.Fatalf("whole managed-env reader Source %v %.5500s", err, output)
			}
			for _, file := range source.Files {
				if err := os.WriteFile(file.Path, []byte(file.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			options := s.options
			options.Host.Profile = ResolvedPolicyReleaseProfile
			options.Host.ResolvePolicy = func() (*protocol.JSONObject, error) { return nil, nil }
			options.NativeCcHahaConfigDirectory = func() (string, error) {
				var directory string
				if err := json.Unmarshal(options.Environment.Get("CLAUDE_CONFIG_DIR"), &directory); err != nil {
					return "", err
				}
				return directory, nil
			}
			if err := options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(source.Initial)); err != nil {
				t.Fatal(err)
			}
			reader, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			for _, stage := range source.Stages {
				if stage.Phase == "reread" {
					raw, _ := json.Marshal(map[string]any{"env": map[string]string{"PROBE_HAHA": "physical-reread", "PROBE_ORDER": "reread", "ANTHROPIC_API_KEY": "reread-stale"}, "futureManaged": map[string]int{"keep": 97}})
					if err := os.WriteFile(filepath.Join(stage.Current, "cc-haha", "settings.json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(stage.Current)); err != nil {
					t.Fatal(err)
				}
				encoded, err := reader.ccHahaEnv()
				var actual map[string]string
				if err != nil || json.Unmarshal(encoded, &actual) != nil {
					t.Fatal("actual scalar cc-haha read", err, string(encoded))
				}
				if !reflect.DeepEqual(actual, stage.Merged) {
					t.Errorf("%s actual current-directory read\nGo %s\nSource %s", stage.Phase, encoded, rawForTrusted(stage.Merged))
				}
				if stage.Current != source.Initial {
					if _, err := reader.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
						t.Errorf("ordinary full-entry directory guard changed: %v", err)
					}
				}
			}
		})
	}
}
