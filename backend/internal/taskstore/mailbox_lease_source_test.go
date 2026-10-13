package taskstore

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceMailboxLeaseHonorsExplicitSymlinkPathAndThirtyRetries(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "symlink"}[symlink], func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			for _, p := range []string{config, filepath.Join(home, "tmp")} {
				_ = os.MkdirAll(p, 0700)
			}
			target := filepath.Join(config, "inbox.json")
			if e := os.WriteFile(target, []byte(`[]`), 0600); e != nil {
				t.Fatal(e)
			}
			original := target
			if symlink {
				target = filepath.Join(config, "alias.json")
				if e := os.Symlink(original, target); e != nil {
					t.Skip(e)
				}
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-mailbox-lock.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_MAILBOX_LOCK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			stdin, e := cmd.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			stdout, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer cmd.Wait()
			data, _ := json.Marshal(map[string]string{"target": target})
			_, _ = stdin.Write(append(data, '\n'))
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || !strings.Contains(scanner.Text(), `"ready":true`) {
				t.Fatal("Source mailbox lock not ready", scanner.Text())
			}
			if !strings.Contains(scanner.Text(), `"retries":30`) {
				t.Fatal(scanner.Text())
			}
			s, e := New(Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
			if e != nil {
				t.Fatal(e)
			}
			start := time.Now()
			release, e := s.AcquireMailboxLease(target)
			if e == nil {
				_ = release()
				t.Fatal("native mailbox ignored actual Source explicit lease")
			}
			if elapsed := time.Since(start); elapsed < 2*time.Second || elapsed > 4*time.Second {
				t.Fatal("thirty-retry policy changed", elapsed)
			}
			_, _ = stdin.Write([]byte("release\n"))
			if !scanner.Scan() || !strings.Contains(scanner.Text(), `"released":true`) {
				t.Fatal("Source release", scanner.Text())
			}
			if strings.Contains(scanner.Text(), `"blocked":["`) {
				t.Fatal(scanner.Text())
			}
			release, e = s.AcquireMailboxLease(target)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(target + ".lock"); e != nil {
				t.Fatal(e)
			}
			if symlink {
				if _, e = os.Stat(original + ".lock"); !os.IsNotExist(e) {
					t.Fatal("explicit lock path canonicalized", e)
				}
			}
			if e = release(); e != nil {
				t.Fatal(e)
			}
			_ = stdin.Close()
		})
	}
}
