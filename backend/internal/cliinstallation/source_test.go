package cliinstallation

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceInstallationVersionsAndDirectories(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(allocation, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	targets := []string{"1.2.3", "v1.2.3", "1.2.3-dev.foo", "99.99.1", "v99.99.9", "1.2.3+sha", "1.2", "latest ", "unknown", "../1.2.3", ""}
	environments := []map[string]string{{}, {"XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache")}, {"XDG_DATA_HOME": "", "XDG_STATE_HOME": "", "XDG_CACHE_HOME": ""}}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "targets": targets, "environments": environments})
	fixture, _ := filepath.Abs("../../testdata/native-cli-installation.fixture.ts")
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_INSTALLATION_SOURCE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(payload)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Versions    []struct{ Version, Error string }
		Directories []Directories
		Blocked     []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Versions) != len(targets) || len(source.Directories) != len(environments) || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, target := range targets {
		version, err := ValidateTarget(target)
		message := ""
		if err != nil {
			message = err.Error()
		}
		if version != source.Versions[i].Version || message != source.Versions[i].Error {
			t.Fatal(i, version, message, source.Versions[i])
		}
	}
	for i, environment := range environments {
		installer, err := New(Options{Home: home, Environment: environment, Platform: "darwin", Architecture: "arm64"})
		if err != nil || !reflect.DeepEqual(installer.Directories(), source.Directories[i]) {
			t.Fatal(i, err, installer.Directories(), source.Directories[i])
		}
	}
}
