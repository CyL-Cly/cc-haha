package sessionenvironment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualSourceSessionEnvFilesMemoInvalidationCwdClearAndPhysicalOrdering(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type profile struct {
		Name     string            `json:"name"`
		Platform string            `json:"platform"`
		Parent   string            `json:"parent"`
		Files    map[string]string `json:"files"`
		Paths    []any             `json:"paths"`
		Sort     []string          `json:"sort"`
	}
	encode := func(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }
	profiles := []profile{}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, parent := range []string{"", " \ufeffexport PARENT='actual-parent'\n", "export INVALID='\xff'"} {
			files := map[string]string{"setup-hook-1.sh": encode("export SETUP='first'\n"), "sessionstart-hook-10.sh": encode("export TEN='10'\n"), "sessionstart-hook-2.sh": encode("export TWO='2'\n"), "cwdchanged-hook-0.sh": encode("export CWD='clear-me'\n"), "filechanged-hook-1.sh": encode("export FILE='clear-me'\n"), "sessionstart-hook-bad.sh": encode("must-not-load"), "unrelated.sh": encode("must-not-load")}
			if parent == "" {
				files = map[string]string{}
			}
			profiles = append(profiles, profile{Name: "source-" + platform + "-" + string(rune('a'+len(profiles))), Platform: platform, Parent: encode(parent), Files: files, Paths: []any{[]any{"Setup", 0}, []any{"SessionStart", 10}, []any{"CwdChanged", 2}, []any{"FileChanged", 1}}, Sort: []string{"sessionstart-hook-10.sh", "filechanged-hook-2.sh", "sessionstart-hook-2.sh", "setup-hook-9.sh", "cwdchanged-hook-1.sh", "invalid", "sessionstart-hook-9999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999.sh"}})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-environment.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SESSION_ENV_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "rows": profiles})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct{ Rows []map[string]any }
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(profiles) {
		t.Fatal(err, string(out))
	}
	for i, p := range profiles {
		config := filepath.Join(t.TempDir(), "config")
		if err = os.MkdirAll(config, 0700); err != nil {
			t.Fatal(err)
		}
		parent := filepath.Join(config, "parent.sh")
		raw, _ := base64.StdEncoding.DecodeString(p.Parent)
		_ = os.WriteFile(parent, raw, 0600)
		service, err := New(config, p.Platform)
		if err != nil {
			t.Fatal(err)
		}
		directory, err := service.directory("first-session")
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range p.Files {
			raw, _ := base64.StdEncoding.DecodeString(value)
			if err = os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		paths := []string{}
		for _, entry := range p.Paths {
			pair := entry.([]any)
			path, err := service.HookEnvFile("first-session", pair[0].(string), pair[1].(int))
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, strings.TrimPrefix(path, config))
		}
		env := map[string]string{"CLAUDE_ENV_FILE": parent}
		first, err := service.Script("first-session", env)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(directory, "sessionstart-hook-0.sh"), []byte("export NEW=\"after-first-read\"\n"), 0600)
		cached, _ := service.Script("first-session", env)
		service.Invalidate()
		invalidated, _ := service.Script("first-session", env)
		service.ClearCwd("first-session")
		service.Invalidate()
		cleared, _ := service.Script("first-session", env)
		switchedCached, _ := service.Script("second-session", env)
		service.Invalidate()
		switched, _ := service.Script("second-session", env)
		sorted := append([]string{}, p.Sort...)
		SortHookEnvFiles(sorted)
		actual := map[string]any{"paths": paths, "first": first, "cached": cached, "invalidated": invalidated, "cleared": cleared, "switchedCached": switchedCached, "switched": switched, "sorted": sorted}
		raw, _ = json.Marshal(actual)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		if !reflect.DeepEqual(parsed, source.Rows[i]) {
			t.Errorf("Source sessionenv%d Go%s Source%v", i, raw, source.Rows[i])
		}
	}
}
