package clibootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePolicyObservationComesFromActualSelectedBranchAndDetachedCache(t *testing.T) {
	for _, kind := range []string{"absent", "plist", "hklm", "file", "drop-ins", "file + drop-ins", "hkcu", "remote"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			config, managed := filepath.Join(home, ".claude"), filepath.Join(home, "managed")
			_ = os.MkdirAll(config, 0700)
			_ = os.MkdirAll(managed, 0700)
			var admin, user *protocol.JSONObject
			object := protocol.NewJSONObject(protocol.JSONField{Name: "language", Value: []byte(`"Deutsch"`)})
			platform := "darwin"
			env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}
			expectedOrigin := kind
			expectedBase, expectedDrop := false, false
			switch kind {
			case "absent":
				expectedOrigin = ""
			case "plist":
				admin = &object
			case "hklm":
				platform = "windows"
				admin = &object
			case "hkcu":
				platform = "windows"
				user = &object
			case "file", "file + drop-ins":
				expectedOrigin = "file"
				expectedBase = true
				_ = os.WriteFile(filepath.Join(managed, "managed-settings.json"), []byte(`{"language":"Deutsch"}`), 0600)
			case "drop-ins":
				expectedOrigin = "file"
			case "remote":
				env["ANTHROPIC_BASE_URL"] = "https://api.anthropic.com"
				env["ANTHROPIC_API_KEY"] = "fake-never-network"
				_ = os.WriteFile(filepath.Join(config, "remote-settings.json"), []byte(`{"language":"Deutsch"}`), 0600)
			}
			if kind == "drop-ins" || kind == "file + drop-ins" {
				expectedDrop = true
				_ = os.MkdirAll(filepath.Join(managed, "managed-settings.d"), 0700)
				_ = os.WriteFile(filepath.Join(managed, "managed-settings.d", "01.json"), []byte(`{"language":"Deutsch"}`), 0600)
			}
			// Windows path producers require Windows absolute paths. This branch test
			// keeps physical fixtures on the host and invokes the already-bound native
			// decision function, with its actual recorded platform primitive.
			n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return admin, user, nil }})
			if err != nil {
				t.Fatal(err)
			}
			n.platform = platform
			value, err := n.ResolvePolicy()
			if err != nil {
				t.Fatal(err)
			}
			observation := n.PolicyObservation()
			if observation.Origin != expectedOrigin || observation.HasBase != expectedBase || observation.HasDropIns != expectedDrop {
				t.Fatal(observation)
			}
			if kind != "absent" && (value == nil || value.String("language") != "Deutsch" || observation.Settings == nil || observation.Settings.String("language") != "Deutsch") {
				t.Fatal(value, observation)
			}
			if observation.Settings != nil {
				observation.Settings.Set("language", []byte(`"mutated"`))
				if n.PolicyObservation().Settings.String("language") != "Deutsch" {
					t.Fatal("presentation exposed active policy reference")
				}
			}
			// No observer is fabricated for a caller-injected policy callback.
			injected := &NativeBoundary{ResolvePolicy: func() (*protocol.JSONObject, error) { return &object, nil }}
			_, _ = injected.ResolvePolicy()
			if injected.PolicyObservation().Origin != "" {
				t.Fatal("mock callback invented origin")
			}
		})
	}
}
