package clisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The full Source producer really applies settings.env before these getters
// and writes. This test isolates its settings consumer; admitting the entire
// native startup environment is a separate MainPrint regression.
func TestNativeSettingsCurrentConfigDirectoryAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-directory-runtime.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, warm := range []bool{false, true} {
		name := "cold-project"
		if warm {
			name = "cached-project"
		}
		t.Run(name, func(t *testing.T) {
			allocation := t.TempDir()
			home := filepath.Join(allocation, "home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_CONFIG_DIRECTORY_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"home": home, "warmProject": warm})
			cmd.Stdin = bytes.NewReader(input)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual Source producer %v %.6500s", err, output)
			}
			var source struct {
				Initial, Redirected, Project string
				Before, After                struct{ Settings, CachedLanguage string }
				UncachedLanguage             string
				InitialSettings              string
				RedirectedSettings           string
				Caps                         struct{ Blocked, SecureReads []json.RawMessage }
			}
			if err := json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
				t.Fatalf("Source escaped fixture or invalid result %v %.6500s", err, output)
			}
			if err := os.WriteFile(source.Before.Settings, []byte(source.InitialSettings), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source.After.Settings, []byte("{\"language\":\"new-location\",\"future\":{\"keep\":73}}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			current := source.Initial
			state, err := NewState(func() float64 { return 1700000000000 })
			if err != nil {
				t.Fatal(err)
			}
			options := Options{State: state, ConfigHome: source.Initial, OriginalCWD: source.Project, Profile: NativeProcessProfile, Now: func() float64 { return 1700000000000 }, PID: os.Getpid(), NativeUserSettingsPath: func() (string, error) { return filepath.Join(current, "settings.json"), nil }}
			service, err := NewService(options)
			if err != nil {
				t.Fatal(err)
			}
			before, err := service.GetEditableSource("userSettings", "")
			if err != nil || before == nil || before.String("language") != source.Before.CachedLanguage {
				t.Fatal("initial actual settings", before, err)
			}
			current = source.Redirected
			path, err := service.Path("userSettings", "")
			if err != nil || path != source.After.Settings {
				t.Fatalf("current native user settings path %q, Source %q: %v", path, source.After.Settings, err)
			}
			after, err := service.GetEditableSource("userSettings", "")
			if err != nil || after != before || after.String("language") != source.After.CachedLanguage {
				t.Fatal("Source cache identity should survive environment assignment", after, err)
			}
			state.ResetSettingsCache()
			uncached, err := service.GetEditableSource("userSettings", "")
			if err != nil || uncached == nil || uncached.String("language") != source.UncachedLanguage {
				t.Fatal("reset must consume current settings path", uncached, err)
			}
			patch := protocol.NewJSONObject(protocol.JSONField{Name: "effortLevel", Value: json.RawMessage(`"high"`)})
			result, err := service.UpdateEditableSource("userSettings", "", Patch{Object: patch})
			if err != nil || result.Error != nil {
				t.Fatal("actual write", result.Error, err)
			}
			for path, want := range map[string]string{source.Before.Settings: source.InitialSettings, source.After.Settings: source.RedirectedSettings} {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != want {
					t.Fatalf("physical Source settings bytes %q: %v\ngot %s\nSource %s", path, err, raw, want)
				}
			}
			options.Profile = ExternalReleaseProfile
			if _, err := NewService(options); err == nil {
				t.Fatal("finite settings profile admitted native path producer")
			}
		})
	}
}
