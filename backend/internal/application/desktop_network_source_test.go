package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDesktopNetworkProducerMatchesActualOriginalInitialAndRefreshSDKEnvelope(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	profiles := []struct {
		initial, next string
		base          map[string]string
	}{
		{`{"network":{"proxy":{"mode":"direct"}}}`, `{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":45000}}`, nil},
		{`{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":1}}`, `{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":1}}`, nil},
		{`{"network":{"proxy":{"mode":"manual","url":" http://127.0.0.1:23456 "},"aiRequestTimeoutMs":1900000}}`, `{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":2100000}}`, map[string]string{"NO_PROXY": "private.local"}},
		{`{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":45000}}`, `{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":55000}}`, map[string]string{"CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": "123456", "CLAUDE_STREAM_MAX_DURATION_MS": "765432"}},
	}
	for index, row := range profiles {
		t.Run(string(rune('A'+index)), func(t *testing.T) {
			home := t.TempDir()
			_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
			fixture, _ := filepath.Abs("../../testdata/native-desktop-network.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=production", "CC_HAHA_DESKTOP_NETWORK_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1", "PATH=/usr/bin:/bin"}
			for key, value := range row.base {
				child.Env = append(child.Env, key+"="+value)
			}
			child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "initial": json.RawMessage(row.initial), "next": json.RawMessage(row.next), "base": row.base}))
			var stderr bytes.Buffer
			child.Stderr = &stderr
			data, err := child.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Initial map[string]string
				Derived struct{ FirstTokenTimeoutDerived, StreamMaxDurationDerived bool }
				Queued  []struct {
					Type      string
					Variables map[string]string
				}
			}
			if err = json.Unmarshal(data, &source); err != nil {
				t.Fatal(err, string(data))
			}
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.desktopTeamLaunch = &desktopTeamLaunch{}
			for key, value := range row.base {
				o.Native.Environment[key] = value
			}
			config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(row.initial), 0600); err != nil {
				t.Fatal(err)
			}
			state, err := initializeDesktopNetwork(ctx, o, o.Native)
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range source.Initial {
				if o.Native.Environment[key] != value {
					t.Errorf("initial %s Source%q Go%q", key, value, o.Native.Environment[key])
				}
			}
			if state.firstTokenDerived != source.Derived.FirstTokenTimeoutDerived || state.streamMaxDerived != source.Derived.StreamMaxDurationDerived {
				t.Fatal("derived Source metadata differs")
			}
			// The actual Source sender emits a queued SDK environment message. Its
			// values are exercised on the real MainPrint/HTTP consumer in adjacent tests.
			if len(source.Queued) > 0 {
				if source.Queued[0].Type != "update_environment_variables" {
					t.Fatal("original Source update type changed")
				}
				variables, err := state.nextVariables(ctx, json.RawMessage(row.next))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(variables, source.Queued[0].Variables) {
					t.Errorf("refresh variables Source%v Go%v", source.Queued[0].Variables, variables)
				}
			}
		})
	}
}
