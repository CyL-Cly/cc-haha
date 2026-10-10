package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type remoteStep struct {
	Action        string              `json:"action"`
	Configuration map[string]string   `json:"configuration,omitempty"`
	Behavior      protocol.JSONObject `json:"behavior"`
	Installation  *Installation       `json:"installation,omitempty"`
	Cancelled     bool                `json:"cancelled,omitempty"`
}
type remoteScenario struct {
	Definition   protocol.JSONObject            `json:"definition"`
	Recipe       RemoteRecipe                   `json:"recipe"`
	Root         string                         `json:"root"`
	Options      map[string]protocol.JSONObject `json:"options"`
	Environment  map[string]string              `json:"environment"`
	Installation Installation                   `json:"installation"`
	Operations   []remoteStep                   `json:"operations"`
}
type remoteObservation struct {
	Value  any     `json:"value"`
	Error  *string `json:"error"`
	Phases [][]any `json:"phases"`
}
type remoteOutcomes struct {
	Observations []remoteObservation            `json:"observations"`
	Calls        [][]any                        `json:"calls"`
	Options      map[string]protocol.JSONObject `json:"options"`
}

func remoteFixture(t *testing.T, auth string) (string, remoteScenario) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "owned-connectors")
	r := RemoteRecipe{ID: "remote-fixture", PluginID: "office-remote-fixture@haha-connectors", Version: "1.0.0", Endpoint: "https://official.example/mcp?format=0&key=replace-me", Transport: "http"}
	r.Auth.Type = auth
	r.Auth.In = "query"
	r.Auth.Name = "key"
	if auth == "oauth" {
		r.Auth.ClientID = "public-client"
	}
	d := record(field("id", r.ID), field("pluginId", r.PluginID), field("version", r.Version))
	return home, remoteScenario{Definition: d, Recipe: r, Root: root, Options: map[string]protocol.JSONObject{"another-plugin@other": record(field("apiKey", "keep-other-secret"))}, Environment: map[string]string{}, Installation: Installation{Directory: filepath.Join(root, "remote", r.ID, r.Version), Args: []string{}, Environment: map[string]string{}}, Operations: []remoteStep{}}
}
func runRemoteScenario(t *testing.T, home string, row remoteScenario) (remoteOutcomes, *RemoteBridge) {
	t.Helper()
	slots := map[string]protocol.JSONObject{}
	for id, value := range row.Options {
		slots[id] = value.Clone()
	}
	out := remoteOutcomes{Observations: []remoteObservation{}, Calls: [][]any{}, Options: slots}
	behavior := record(field("status", "connected"), field("toolCount", 2), field("authUrl", "https://accounts.example/authorize?state=fixture"), field("fail", ""))
	deps := RemoteDependencies{
		Probe: func(ctx context.Context, name string, config protocol.JSONObject) (RemoteProbe, error) {
			out.Calls = append(out.Calls, []any{"probe", name, config})
			if ctx.Err() != nil {
				return RemoteProbe{}, ctx.Err()
			}
			if behavior.String("fail") == "probe" {
				return RemoteProbe{}, errors.New("sensitive-detail-must-be-hidden")
			}
			var count int
			_ = json.Unmarshal(behavior.Get("toolCount"), &count)
			return RemoteProbe{behavior.String("status"), count}, nil
		},
		OAuth: func(ctx context.Context, name string, config protocol.JSONObject, url func(string) error) error {
			out.Calls = append(out.Calls, []any{"oauth", name, config})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if behavior.String("fail") == "oauth" {
				return errors.New("sensitive-detail-must-be-hidden")
			}
			return url(behavior.String("authUrl"))
		},
		Disconnect: func(_ context.Context, name string, config protocol.JSONObject) error {
			out.Calls = append(out.Calls, []any{"disconnect", name, config})
			if behavior.String("fail") == "disconnect" {
				return errors.New("sensitive-detail-must-be-hidden")
			}
			return nil
		},
		LoadOptions: func(_ context.Context, id string) (protocol.JSONObject, error) {
			if o, ok := slots[id]; ok {
				return o.Clone(), nil
			}
			return protocol.NewJSONObject(), nil
		},
		SaveOptions: func(_ context.Context, id string, value, schema protocol.JSONObject) error {
			out.Calls = append(out.Calls, []any{"save", id, value, schema})
			slots[id] = value.Clone()
			return nil
		},
		DeleteOptions: func(_ context.Context, id string) error {
			out.Calls = append(out.Calls, []any{"delete", id})
			delete(slots, id)
			return nil
		},
		ClearOAuth: func(_ context.Context, name string, config protocol.JSONObject) error {
			out.Calls = append(out.Calls, []any{"clearOAuth", name, config})
			return nil
		},
		Environment: func(key string) (string, bool) { value, exists := row.Environment[key]; return value, exists },
		PluginDataDirectory: func(_ context.Context, id string) (string, error) {
			path := filepath.Join(home, ".claude", "plugins", "data", strings.ReplaceAll(id, "@", "-"))
			return path, os.MkdirAll(path, 0755)
		},
	}
	bridge, err := NewRemoteBridge(row.Recipe, deps)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := RemoteAdapter(row.Definition, row.Root, row.Recipe, deps)
	if err != nil {
		t.Fatal(err)
	}
	installation := row.Installation
	for _, op := range row.Operations {
		behavior.Assign(op.Behavior)
		ctx, cancel := context.WithCancel(context.Background())
		if op.Cancelled {
			cancel()
		}
		observation := remoteObservation{Phases: [][]any{}}
		progress := func(phase string, url *string) {
			row := []any{phase}
			if url != nil {
				row = append(row, *url)
			}
			observation.Phases = append(observation.Phases, row)
		}
		value := installation
		if op.Installation != nil {
			value = *op.Installation
		}
		var result any
		var err error
		switch op.Action {
		case "configure":
			err = adapter.(ConfigurableAdapter).Configure(ctx, op.Configuration)
		case "prepare":
			var prepared Installation
			prepared, err = adapter.Prepare(ctx, progress)
			if err == nil {
				installation = prepared
				result = installation
			}
		case "authenticate":
			err = adapter.Authenticate(ctx, value, progress)
		case "check":
			var check Check
			check, err = adapter.Check(ctx, value)
			if err == nil {
				wire := record(field("authenticated", check.Authenticated), field("verification", check.Verification))
				if check.ToolCount != nil {
					wire.Set("toolCount", raw(*check.ToolCount))
				}
				result = wire
			}
		case "deactivate":
			err = adapter.Deactivate(ctx)
		case "remove":
			err = adapter.Remove(ctx, value)
		case "bridge-check":
			result, err = bridge.Check(ctx)
		case "bridge-authenticate":
			err = bridge.Authenticate(ctx, progress)
		case "config":
			var configured bool
			configured, err = bridge.IsConfigured(ctx)
			if err == nil {
				result = record(field("configured", configured))
			}
		default:
			t.Fatal("unknown remote scenario", op.Action)
		}
		cancel()
		if err != nil {
			text := err.Error()
			observation.Error = &text
			observation.Value = nil
		} else {
			observation.Value = result
		}
		out.Observations = append(out.Observations, observation)
	}
	compareRemoteSource(t, home, row, out)
	return out, bridge
}
func compareRemoteSource(t *testing.T, home string, row remoteScenario, actual remoteOutcomes) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-remote-adapter.fixture.ts")
	input, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_REMOTE_ADAPTER_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source RemoteAdapter: %v %s", err, stderr.Bytes())
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if json.Unmarshal(actualJSON, &left) != nil || json.Unmarshal(output, &right) != nil {
		t.Fatalf("invalid source observations: %s", output)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("RemoteAdapter full observations/calls/options differ\nGo: %s\nSource: %s", actualJSON, output)
	}
}
