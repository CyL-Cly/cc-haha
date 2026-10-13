package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func TestActualSourceSDKPermissionControlACKStatusGuardAndTransitions(t *testing.T) {
	for _, profile := range []struct {
		name        string
		settings    string
		allowBypass bool
		env         map[string]string
	}{
		{"default", `{}`, false, nil}, {"bypass_available", `{}`, true, nil}, {"bypass_disabled", `{"permissions":{"disableBypassPermissionsMode":"disable"}}`, true, nil}, {"auto_disabled", `{"permissions":{"disableAutoMode":"disable"}}`, false, nil}, {"circuit_broken", `{}`, false, map[string]string{"USER_TYPE": "ant", "CLAUDE_INTERNAL_FC_OVERRIDES": `{"tengu_auto_mode_config":{"enabled":"disabled"}}`}},
	} {
		t.Run(profile.name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), profile.settings)
			for k, v := range profile.env {
				f.n.Environment[k] = v
			}
			controls := []map[string]any{{"subtype": "set_permission_mode", "mode": "plan", "ultraplan": true}, {"subtype": "set_permission_mode", "mode": "plan"}, {"subtype": "set_permission_mode", "mode": "default"}, {"subtype": "set_permission_mode", "mode": "acceptEdits"}, {"subtype": "set_permission_mode", "mode": "dontAsk"}, {"subtype": "set_permission_mode", "mode": "auto"}, {"subtype": "set_permission_mode", "mode": "default"}, {"subtype": "set_permission_mode", "mode": "bypassPermissions"}, {"subtype": "set_permission_mode", "mode": "default"}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			allocation, _ := filepath.EvalSymlinks(t.TempDir())
			home := filepath.Join(allocation, "source-home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../testdata/native-sdk-permission-mode.fixture.ts")
			c := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
			c.Dir = home
			c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_SDK_MODE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
			var settings protocol.JSONObject
			_ = settings.UnmarshalJSON([]byte(profile.settings))
			c.Stdin = bytes.NewReader(rawMain(map[string]any{"settings": settings, "allowBypass": profile.allowBypass, "env": profile.env, "controls": controls}))
			data, err := c.CombinedOutput()
			if err != nil {
				t.Fatal("actual Source mode", err, string(data))
			}
			var source struct {
				Results      []protocol.JSONObject
				Capabilities protocol.JSONObject
			}
			if err = json.Unmarshal(data, &source); err != nil || len(source.Results) != len(controls) {
				t.Fatal(err, string(data))
			}
			if string(source.Capabilities.Get("blocked")) != "[]" {
				t.Fatal(string(source.Capabilities.Get("blocked")))
			}
			if profile.env["USER_TYPE"] == "ant" {
				// The external CLI has a separate, explicit internal-host schema
				// boundary. Exercise this conditional policy with its real prepared
				// startup producer, without claiming an ANT MainPrint is admitted.
				owner := cooperative.New()
				defer owner.Close()
				global := protocol.NewJSONObject()
				startup, err := sdkpermissions.NewNativeStartup(owner, sdkpermissions.NativeStartupOptions{Home: f.home, CWD: f.cwd, OriginalCWD: f.cwd, Platform: "darwin", Environment: func(key string) string { return profile.env[key] }, Global: func() (*protocol.JSONObject, error) { return &global, nil }, InitialSettings: func() (*protocol.JSONObject, error) { return &settings, nil }, SettingsForSource: func(name string) (*protocol.JSONObject, error) {
					if name == "userSettings" {
						return &settings, nil
					}
					return nil, nil
				}, Stat: os.Stat, Lstat: os.Lstat, Realpath: filepath.EvalSymlinks})
				if err != nil {
					t.Fatal(err)
				}
				if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
					prepared, e := startup.Prepare(scope, &settings, sdkpermissions.NativeStartupRequest{Mode: "default"})
					if e != nil {
						return e
					}
					current := protocol.NewJSONObject(protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: rawMain(prepared.BypassAvailable)})
					for i, control := range controls {
						reason, e := startup.NativeSDKModeRejection(scope, control["mode"].(string), current)
						if e != nil {
							return e
						}
						var frames []protocol.JSONObject
						_ = json.Unmarshal(source.Results[i].Get("frames"), &frames)
						want := nativeInProcessObject(frames[0].Get("response")).String("error")
						if reason != want {
							return fmt.Errorf("conditional Source SDK guard mode%s Source%q Go%q", control["mode"], want, reason)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return
			}
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			frames := make(chan protocol.JSONObject, 64)
			disabled := false
			m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", AllowDangerouslySkipPermissions: profile.allowBypass, MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error {
				var frame protocol.JSONObject
				if err := frame.UnmarshalJSON(data); err != nil {
					return err
				}
				if frame.String("type") == "control_response" || frame.String("type") == "system" && frame.String("subtype") == "status" {
					frames <- frame.Clone()
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := m.Run(ctx); done <- err }()
			t.Cleanup(func() { _ = writer.Close(); cancel(); _ = m.ShutdownSession(context.Background()) })
			send := func(id string, request any) {
				t.Helper()
				_, err := writer.Write(append(rawMain(map[string]any{"type": "control_request", "request_id": id, "request": request}), '\n'))
				if err != nil {
					t.Fatal(err)
				}
			}
			read := func() protocol.JSONObject {
				t.Helper()
				select {
				case frame := <-frames:
					return frame
				case err := <-done:
					t.Fatal("input ended before mode reply", err)
				case <-ctx.Done():
					t.Fatal("mode response missing", ctx.Err())
				}
				return protocol.JSONObject{}
			}
			send("initialize", map[string]any{"subtype": "initialize"})
			_ = read()
			for i, request := range controls {
				send(fmt.Sprintf("mode-%d", i), request)
				var sourceFrames []protocol.JSONObject
				_ = json.Unmarshal(source.Results[i].Get("frames"), &sourceFrames)
				gotFrames := []protocol.JSONObject{}
				for range sourceFrames {
					frame := read()
					frame.Delete("uuid")
					frame.Delete("session_id")
					gotFrames = append(gotFrames, frame)
				}
				var snapshot protocol.JSONObject
				receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
					current, e := m.Controller.PlanModeContext(scope)
					if e != nil {
						return e
					}
					active, e := m.Controller.NativeAutoModeActive(scope)
					if e != nil {
						return e
					}
					snapshot = protocol.NewJSONObject(protocol.JSONField{Name: "frames", Value: rawMain(gotFrames)}, protocol.JSONField{Name: "mode", Value: current.Get("mode")}, protocol.JSONField{Name: "autoActive", Value: rawMain(active)})
					if current.Get("prePlanMode") != nil {
						snapshot.Set("prePlanMode", current.Get("prePlanMode"))
					}
					return nil
				})
				if e == nil {
					e = receipt.Wait()
				}
				if e != nil {
					t.Fatal(e)
				}
				want := source.Results[i].Clone()
				want.Delete("metadata")
				var a, b any
				_ = json.Unmarshal(rawMain(want), &a)
				_ = json.Unmarshal(rawMain(snapshot), &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("control%d actual Source%s Go%s", i, rawMain(want), rawMain(snapshot))
				}
			}
			_ = writer.Close()
			select {
			case err := <-done:
				if err != nil && !strings.Contains(err.Error(), "canceled") {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("actual mode input did not close")
			}
		})
	}
}
