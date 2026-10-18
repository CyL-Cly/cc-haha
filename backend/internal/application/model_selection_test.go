package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeModelSelectionReachesActualHarnessRequest(t *testing.T) {
	cases := []struct{ name, requested, envModel, settings, sonnetOverride, want string }{
		{"flag-priority", " FlagModel ", "EnvModel", `{"model":"SettingModel"}`, "", "FlagModel"},
		{"environment-priority", "", " EnvModel ", `{"model":"SettingModel"}`, "", "EnvModel"},
		{"saved-setting", "", "", `{"model":" SavedModel "}`, "", "SavedModel"},
		{"relay-alias", "sonnet", "", `{"availableModels":["sonnet"]}`, "DeepSeekRelay", "DeepSeekRelay"},
		{"settings-override", "sonnet", "", `{"modelOverrides":{"claude-sonnet-4-5-20250929":"OverriddenRelay"}}`, "", "OverriddenRelay"},
		{"empty-allowlist", "RejectedModel", "", `{"availableModels":[]}`, "DefaultRelay", "DefaultRelay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var model string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				var request protocol.JSONObject
				if err = request.UnmarshalJSON(body); err != nil {
					t.Error(err)
				}
				model = request.String("model")
				response(w, "model-selection", "", nil, "MODEL SELECTION OK")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Model = tc.requested
			o.Arguments.Tools = []string{"Read"}
			o.Environment["ANTHROPIC_MODEL"] = tc.envModel
			o.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = tc.sonnetOverride
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(tc.settings), 0600); err != nil {
				t.Fatal(err)
			}
			managed := o.Native.ManagedRoot
			o.Native = nil
			o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if err = s.Submit(context.Background(), tui.Input{Text: "Resolve the actual model."}, nil); err != nil {
				t.Fatal(err)
			}
			if model != tc.want {
				t.Fatalf("actual model request %q want %q", model, tc.want)
			}
			persisted, err := os.ReadFile(filepath.Join(config, "settings.json"))
			if err != nil || !json.Valid(persisted) || string(persisted) != tc.settings {
				t.Fatalf("selection modified saved input: %s %v", persisted, err)
			}
		})
	}
}
