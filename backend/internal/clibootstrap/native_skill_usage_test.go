package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSkillUsagePreservesOldConfigAndProcessDebounceLikeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	_ = os.MkdirAll(f.n.Temp, 0700)
	old := `{"future":{"keep":true},"skillUsage":{"numeric":{"usageCount":4,"lastUsedAt":12,"future":true},"string":{"usageCount":"4"},"null":{"usageCount":null},"boolean":{"usageCount":false},"array":{"usageCount":[1,null,"x"]},"object":{"usageCount":{"x":1}},"surrogate":{"usageCount":"\ud800"},"infinity":{"usageCount":1e400},"untouched":{"usageCount":9,"future":true}}}`
	path := filepath.Join(f.config, ".claude.json")
	f.write(t, path, old)
	rows := []struct {
		Name string  `json:"name"`
		Now  float64 `json:"now"`
	}{
		{"numeric", 100000}, {"numeric", 159999}, {"numeric", 160000}, {"numeric", 150000}, {"string", 200000}, {"null", 200000}, {"boolean", 200000}, {"array", 200000}, {"object", 200000}, {"surrogate", 200000}, {"infinity", 200000}, {"new", 200000}, {"__proto__", 200000}, {"numeric", 220000},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-tool.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = f.home
	cmd.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": f.home, "usages": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Usages       []json.RawMessage
		Config       protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Usages) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v usages=%d blocked=%v %.1000s", e, len(source.Usages), source.Capabilities.Blocked, output)
	}
	f.write(t, path, old)
	clock := float64(0)
	f.n.NowMillis = func() float64 { return clock }
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: f.home, ConfigDir: &f.config, Now: f.n.Now, NowMillis: f.n.NowMillis, PID: os.Getpid(), FS: mainGlobalFiles{n: f.n}})
	if e != nil {
		t.Fatal(e)
	}
	if e = global.Enable(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = global.Close() })
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	m := &MainPrint{Owner: owner, Global: global, options: MainPrintOptions{Native: f.n}}
	for i, row := range rows {
		clock = row.Now
		if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
			// Borrowed child has a separate MainPrint but the exact same process
			// debounce map; it must not double-record a parent invocation.
			if i == 1 {
				m.ensureNativeSkillState()
				child := &MainPrint{Owner: owner, Global: global, options: m.options, skillListing: m.skillListing}
				if e := child.recordNativeSkillUsage(scope, row.Name); e != nil {
					return e
				}
			} else if e := m.recordNativeSkillUsage(scope, row.Name); e != nil {
				return e
			}
			current, e := global.Get()
			if e != nil {
				return e
			}
			if !planSourcesEqual(current.Get("skillUsage"), source.Usages[i]) {
				return fmt.Errorf("usage%d Go=%s Source=%s", i, current.Get("skillUsage"), source.Usages[i])
			}
			return nil
		}); e != nil {
			t.Error(e)
		}
	}
	disk, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var persisted protocol.JSONObject
	_ = persisted.UnmarshalJSON(disk)
	if !planSourcesEqual(persisted.Get("future"), source.Config.Get("future")) || !planSourcesEqual(persisted.Get("skillUsage"), source.Usages[len(rows)-1]) {
		t.Fatal("old unknown config fields or actual persisted skill usage changed", string(disk))
	}
}
