package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestProcessNativeRawLaunchEnvironmentAndSettingsAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-launch-environment.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_LAUNCH_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home}))
	output, err := cmd.CombinedOutput()
	var source struct {
		Rows []struct {
			Name, CWD, Home, Physical, SettingsPath, GlobalPath, Language, Content string
			Directory                                                              *string
		}
		Caps struct{ Blocked, SecureReads []json.RawMessage }
	}
	if err != nil || json.Unmarshal(output, &source) != nil || len(source.Rows) != 6 || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole Source launch environment %v %.6500s", err, output)
	}
	for _, row := range source.Rows {
		t.Run(row.Name, func(t *testing.T) {
			environment := map[string]string{"HOME": row.Home, "USERPROFILE": row.Home, "PATH": "/usr/bin:/bin", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
			if row.Directory != nil {
				environment["CLAUDE_CONFIG_DIR"] = *row.Directory
			}
			n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: environment, CWD: row.CWD, Platform: runtime.GOOS, ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			actual, present := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
			if present != (row.Directory != nil) || row.Directory != nil && actual != *row.Directory {
				t.Fatalf("raw launch directory %q/%v Source %v", actual, present, row.Directory)
			}
			if n.Environment["HOME"] != row.Home || environment["HOME"] != row.Home {
				t.Fatal("launch HOME was rewritten")
			}
			if n.ConfigHomeDirectory() != row.Physical {
				t.Fatalf("native physical root %q Source %q", n.ConfigHomeDirectory(), row.Physical)
			}
			p, err := NewNativeProcessSettings(n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(ctx)
			if err := p.ensureSettings(); err != nil {
				t.Fatal(err)
			}
			path, err := p.settings.Path("userSettings", "")
			if err != nil || path != row.SettingsPath {
				t.Fatalf("actual settings path %q Source %q: %v", path, row.SettingsPath, err)
			}
			settings, err := p.startup.GetSettingsForSource("userSettings")
			if err != nil || settings == nil || settings.String("language") != row.Language {
				t.Fatalf("actual raw launch settings Source %q: %v %v", row.Language, settings, err)
			}
			if p.global.Path() != row.GlobalPath {
				t.Fatal(fmt.Sprintf("actual global path %q Source %q", p.global.Path(), row.GlobalPath))
			}
			physical, err := os.ReadFile(row.SettingsPath)
			if err != nil || string(physical) != row.Content {
				t.Fatalf("raw launch changed original physical file: %v %s", err, physical)
			}
		})
	}
}
