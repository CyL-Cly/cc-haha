package builtintools

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourcePowerShellSandboxWriteConfigAndAllSymlinkRepresentations(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	s, owner, home, permission := fixture(t)
	work, allow, outside := filepath.Join(home, "work"), filepath.Join(home, "allow"), filepath.Join(home, "outside")
	for _, path := range []string{work, allow, outside, filepath.Join(allow, "denied")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.options.CWD = work
	final := filepath.Join(allow, "final.txt")
	if err := os.WriteFile(final, []byte("PRIVATE"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{
		{filepath.Join(home, "alias"), allow},
		{filepath.Join(allow, "escaped"), outside},
		{filepath.Join(allow, "intermediate.txt"), filepath.Join(allow, "denied", "middle.txt")},
		{filepath.Join(allow, "denied", "middle.txt"), final},
		{filepath.Join(allow, "dangling.txt"), filepath.Join(outside, "new.txt")},
	} {
		if err := os.Symlink(row[1], row[0]); err != nil {
			t.Fatal(err)
		}
	}
	type config struct {
		Enabled         bool     `json:"enabled"`
		AllowOnly       []string `json:"allowOnly"`
		DenyWithinAllow []string `json:"denyWithinAllow"`
	}
	configs := []config{
		{true, []string{allow}, []string{}},
		{true, []string{filepath.Join(home, "alias")}, []string{}},
		{true, []string{allow}, []string{filepath.Join(allow, "denied")}},
		{true, []string{allow, outside}, []string{filepath.Join(allow, "denied")}},
		{false, []string{home}, []string{}},
		{true, []string{"."}, []string{}},
		{true, []string{filepath.Join(allow, "*")}, []string{}},
	}
	// The shared Source capability guard refuses dangling symlink reads by
	// design. Keep that native filesystem negative below, separate from the
	// paired Source corpus rather than weakening the process guard.
	paths := []string{final, filepath.Join(allow, "new.txt"), filepath.Join(allow, "escaped", "new.txt"), filepath.Join(allow, "intermediate.txt"), filepath.Join(outside, "new.txt"), filepath.Join(work, "new.txt"), filepath.Join(home, "alias", "new.txt")}
	rows := []map[string]any{}
	for _, c := range configs {
		for _, path := range paths {
			rows = append(rows, map[string]any{"config": c, "path": path})
		}
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", filepath.Join(root, "scripts/quality-gate/go-parity/native-powershell-policy.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_TMPDIR=" + home, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "cwd": work, "sandboxRows": rows})
	cmd.Stdin = bytes.NewReader(data)
	var source struct{ SandboxRows []struct{ Allowed bool } }
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if len(source.SandboxRows) != len(rows) {
		t.Fatal("incomplete actual sandbox path corpus")
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for i, row := range rows {
			c := row["config"].(config)
			s.options.BashSandbox = &BashSandboxBindings{Owner: owner, WritePaths: func(*cooperative.Scope) (*BashSandboxWritePaths, error) {
				if !c.Enabled {
					return nil, nil
				}
				return &BashSandboxWritePaths{AllowOnly: c.AllowOnly, DenyWithinAllow: c.DenyWithinAllow}, nil
			}}
			got, e := s.powerShellSandboxWriteAllowed(scope, row["path"].(string))
			if e != nil {
				return e
			}
			if got != source.SandboxRows[i].Allowed {
				return fmt.Errorf("Source sandbox row %d %+v native=%v Source=%v", i, row, got, source.SandboxRows[i].Allowed)
			}
		}
		// The real permission consumer gates sandbox writes to paths OUTSIDE
		// the working directory; seeded cwd must retain its default-mode ask.
		permission.Set("mode", quote("default"))
		s.options.BashSandbox = &BashSandboxBindings{Owner: owner, WritePaths: func(*cooperative.Scope) (*BashSandboxWritePaths, error) {
			return &BashSandboxWritePaths{AllowOnly: []string{work, allow}, DenyWithinAllow: []string{filepath.Join(allow, "denied")}}, nil
		}}
		paths, e := s.powerShellPathContext(scope, permission.Clone())
		if e != nil {
			return e
		}
		if allowed, e := s.powerShellSandboxWriteAllowed(scope, filepath.Join(allow, "dangling.txt")); e != nil || allowed {
			return fmt.Errorf("real dangling target escapes allowed directory: allowed=%v err=%v", allowed, e)
		}
		for _, row := range []struct {
			Path    string
			Allowed bool
		}{{filepath.Join(work, "new.txt"), false}, {filepath.Join(allow, "new.txt"), true}, {filepath.Join(allow, "denied", "new.txt"), false}, {filepath.Join(allow, "escaped", "new.txt"), false}} {
			got, e := paths.ValidatePath(row.Path, "write")
			if e != nil {
				return e
			}
			if got.Allowed != row.Allowed {
				return fmt.Errorf("real path consumer %+v -> %+v", row, got)
			}
		}
		permission.Set("alwaysDenyRules", raw(map[string][]string{"localSettings": {"Edit(//" + filepath.ToSlash(filepath.Join(allow, "new.txt"))[1:] + ")"}}))
		paths, e = s.powerShellPathContext(scope, permission.Clone())
		if e != nil {
			return e
		}
		got, e := paths.ValidatePath(filepath.Join(allow, "new.txt"), "write")
		if e != nil {
			return e
		}
		if got.Allowed {
			return fmt.Errorf("sandbox config bypassed file deny")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
