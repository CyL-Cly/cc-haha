package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceSandboxWriteConfigPlatformDefaultsGlobsAndOwnedSnapshot(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	configs := []FilesystemConfig{{AllowWrite: []string{}, DenyWrite: []string{}}, {AllowWrite: []string{".", "/owned/**", "/owned/*", "/owned/[ab]", "~/owned"}, DenyWrite: []string{"/owned/deny/**", "/owned/deny*", "/owned/deny?", "/owned/[ab]"}}}
	rows := []map[string]any{}
	for _, platform := range []string{"macos", "linux", "windows"} {
		for _, config := range configs {
			rows = append(rows, map[string]any{"platform": platform, "config": map[string]any{"filesystem": config}})
		}
	}
	fixture, _ := filepath.Abs("testdata/write-config.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source []WriteRestriction
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if len(source) != len(rows) {
		t.Fatal("Source write configuration receipt incomplete")
	}
	for i, row := range rows {
		config := row["config"].(map[string]any)["filesystem"].(FilesystemConfig)
		platform := row["platform"].(string)
		got := FilesystemWriteRestriction(config, platform, home)
		if !reflect.DeepEqual(got, source[i]) {
			t.Fatalf("Source config row%d native=%+v Source=%+v", i, got, source[i])
		}
		manager := &Manager{config: Config{Filesystem: config}, options: ManagerOptions{Platform: platform, Home: home}}
		snapshot := manager.WriteConfig()
		snapshot.AllowOnly[0] = "injected"
		if len(snapshot.DenyWithinAllow) > 0 {
			snapshot.DenyWithinAllow[0] = "injected"
		}
		if actual := manager.WriteConfig(); !reflect.DeepEqual(actual, source[i]) {
			t.Fatal("production write snapshot borrowed permission-owned mutable paths", actual)
		}
	}
}
