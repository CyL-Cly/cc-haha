package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOrdinaryMainCommandCurrentProviderDirectoryAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"ordinary", "desktop", "missing-index", "invalid-index"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			source := runNativeConfigProviderSource(t, f.n.Home, profile)
			for _, file := range source.Files {
				if err := os.WriteFile(file.Path, []byte(file.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{filepath.Join(source.Initial, "settings.json"), filepath.Join(source.Project, ".claude", "settings.json")} {
				if err := os.WriteFile(file, []byte(`{"futurePrivate":{"keep":true}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: f.n, Platform: runtime.GOOS, PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
			if err != nil {
				t.Fatal("actual non-model Main constructor", err)
			}
			defer command.Close()
			for _, stage := range source.Stages {
				if stage.Phase == "reread" {
					raw := rawMain(map[string]any{"env": map[string]string{"PROBE_HAHA": "physical-reread", "PROBE_ORDER": "reread", "ANTHROPIC_API_KEY": "reread-stale"}, "futureManaged": map[string]int{"keep": 97}})
					if err := os.WriteFile(filepath.Join(stage.Current, "cc-haha", "settings.json"), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := command.Settings.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(stage.Current)); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(filepath.Join(stage.Current, "cc-haha", "settings.json"))
				if err != nil {
					t.Fatal(err)
				}
				var contents struct{ Env map[string]string }
				if err = json.Unmarshal(raw, &contents); err != nil {
					t.Fatal(err)
				}
				legacy := protocol.NewJSONObject()
				for key, value := range contents.Env {
					legacy.Set(key, quoteMain(value))
				}
				merged, err := command.mergeCLIProviderEnvironment(legacy)
				var actual map[string]string
				encoded, _ := merged.MarshalJSON()
				if err != nil || json.Unmarshal(encoded, &actual) != nil {
					t.Fatal("actual command provider reader", err, string(encoded))
				}
				if !reflect.DeepEqual(actual, stage.Merged) {
					t.Errorf("%s current command provider\nGo %s\nSource %s", stage.Phase, encoded, rawMain(stage.Merged))
				}
				if stage.Current != source.Initial {
					if _, err := command.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
						t.Error("ordinary full-entry guard changed", err)
					}
				}
			}
		})
	}
}
