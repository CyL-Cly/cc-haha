package cloudauth

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

func cloudStatusSource(t *testing.T, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/native-cloud-auth-status.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLOUD_STATUS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(input)
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	return out
}
func TestActualSourceCloudAuthStatusCopyRetentionAndLiveSubscriptionOrder(t *testing.T) {
	manager := NewAuthStatusManager()
	events := []AuthStatus{}
	visits := []string{}
	dispose := manager.Subscribe(func(value AuthStatus) { events = append(events, value) })
	var removeSecond, third func()
	first := manager.Subscribe(func(AuthStatus) {
		visits = append(visits, "first")
		removeSecond()
		if third == nil {
			third = manager.Subscribe(func(AuthStatus) { visits = append(visits, "third") })
		}
	})
	removeSecond = manager.Subscribe(func(AuthStatus) { visits = append(visits, "second") })
	manager.Start()
	manager.AddOutput("line one")
	manager.SetError("first failure")
	manager.SetError("latest failure")
	manager.End(false)
	retained := manager.Status()
	retained.Output = append(retained.Output, "mutating the returned copy")
	manager.Start()
	manager.AddOutput("fresh line")
	manager.End(true)
	first()
	third()
	dispose()
	manager.Start()
	var want, got any
	source := cloudStatusSource(t, map[string]any{})
	_ = json.Unmarshal(source, &want)
	encoded, _ := json.Marshal(map[string]any{"events": events, "visits": visits, "status": manager.Status()})
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Source%s Go%s", source, encoded)
	}
}
func TestActualSourceCloudRefreshStateFromRealProcessOutputAndFailure(t *testing.T) {
	for _, provider := range []string{"aws", "gcp"} {
		for _, row := range []struct {
			name, command, stdout, stderr string
			code                          int
		}{
			{"success clears output", "printf '  browser challenge\\n'", "  browser challenge\n", "", 0},
			{"failure retains output", "printf ' waiting for login\\n'; exit 7", " waiting for login\n", "", 7},
			{"failure retains trimmed stderr", "printf ' bad session\\n' >&2; exit 2", "", " bad session\n", 2},
		} {
			t.Run(provider+"/"+row.name, func(t *testing.T) {
				manager := NewAuthStatusManager()
				events := []AuthStatus{}
				manager.Subscribe(func(value AuthStatus) { events = append(events, value) })
				home := t.TempDir()
				output, err := RunAuthRefresh(context.Background(), row.command, time.Second, home, map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin"}, manager)
				if err != nil {
					t.Fatal(err)
				}
				input := map[string]any{"kind": "refresh", "provider": provider, "code": row.code}
				if row.stdout != "" {
					input["stdout"] = row.stdout
				}
				if row.stderr != "" {
					input["stderr"] = row.stderr
				}
				source := cloudStatusSource(t, input)
				var want, got any
				_ = json.Unmarshal(source, &want)
				encoded, _ := json.Marshal(map[string]any{"success": output.ExitCode == 0, "events": events, "status": manager.Status()})
				_ = json.Unmarshal(encoded, &got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Source%s Go%s", source, encoded)
				}
			})
		}
	}
}
