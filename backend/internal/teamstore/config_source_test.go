package teamstore

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestTeamConfigReadAtomicMutationAndLegacyUnknownsMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	for _, p := range []string{".claude", "tmp"} {
		_ = os.MkdirAll(filepath.Join(home, p), 0700)
	}
	cases := []map[string]any{{"name": "missing"}, {"name": "invalid", "initial": "invalid-json"}, {"name": "object", "initial": `{"unknown":true}`}, {"name": "array", "initial": `[]`}, {"name": "null", "initial": `null`}, {"name": "Legacy_中文😀", "initial": `{"name":"old","members":[],"unknown":{"keep":true}}`, "mutate": map[string]any{"description": "exact"}, "noop": true}, {"name": "created", "write": object([]byte(`{"name":"created","members":[],"future":{"keep":true}}`))}, {"name": "absentmutation", "mutate": map[string]any{"description": "missing"}}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-config.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_CONFIG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	nativeHome := t.TempDir()
	configPath := filepath.Join(nativeHome, ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configPath, PID: os.Getpid(), Now: time.Now})
	config, e := NewConfig(ConfigOptions{ConfigDir: configPath, TaskStore: tasks, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range cases {
		name := row["name"].(string)
		t.Run(name, func(t *testing.T) {
			if initial, ok := row["initial"].(string); ok {
				_ = os.MkdirAll(config.Directory(name), 0700)
				_ = os.WriteFile(config.Path(name), []byte(initial), 0666)
			}
			var err error
			var unchanged any
			if value, ok := row["write"]; ok {
				err = config.Write(name, object(raw(value)))
			}
			if value, ok := row["mutate"]; ok {
				_, err = config.Mutate(name, func(v protocol.JSONObject) (protocol.JSONObject, error) { v.Assign(object(raw(value))); return v, nil })
			}
			if row["noop"] == true {
				before, _ := os.Stat(config.Path(name))
				_, err = config.Mutate(name, func(v protocol.JSONObject) (protocol.JSONObject, error) { return v, nil })
				after, _ := os.Stat(config.Path(name))
				unchanged = before.ModTime().Equal(after.ModTime())
			}
			var message any
			if err != nil {
				message = err.Error()
			}
			var physical, mode any
			if data, e := os.ReadFile(config.Path(name)); e == nil {
				physical = string(data)
				stat, _ := os.Stat(config.Path(name))
				mode = uint32(stat.Mode().Perm())
			}
			files := []string{}
			if entries, e := os.ReadDir(config.Directory(name)); e == nil {
				for _, entry := range entries {
					files = append(files, entry.Name())
				}
			}
			sort.Strings(files)
			actual := raw(map[string]any{"sync": config.Read(name), "async": config.ReadAsyncRaw(name), "physical": physical, "mode": mode, "files": files, "error": message, "unchanged": unchanged})
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(source.Rows[i], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("%s\n%s", actual, source.Rows[i])
			}
		})
	}
}
