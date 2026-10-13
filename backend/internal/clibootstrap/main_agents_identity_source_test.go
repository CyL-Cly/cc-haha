package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestActualSourceAgentDeviceInodeBigIntAndFailureIdentity(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	file := filepath.Join(home, "actual-agent.md")
	if e := os.WriteFile(file, []byte("---\nname: actual\ndescription: actual hardlink\n---\nActual instructions"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(home, "hardlink-agent.md")
	if e := os.Link(file, link); e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(file)
	if e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	for _, device := range []uint64{0, 1, 4294967295} {
		for _, inode := range []uint64{0, 1, 9007199254740992, 9007199254740993, 18446744073709551615} {
			rows = append(rows, map[string]any{"device": strconv.FormatUint(device, 10), "inode": strconv.FormatUint(inode, 10)})
		}
	}
	rows = append(rows, map[string]any{"error": true}, map[string]any{"physical": true, "path": file}, map[string]any{"physical": true, "path": link})
	fixture, _ := filepath.Abs("../../testdata/native-agent-file-identity.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_IDENTITY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": rows}))
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct{ Identities []*string }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Identities) != len(rows) {
		t.Fatal(e, string(out))
	}
	for i, row := range rows[:16] {
		probe := func(path string, nofollow bool) (uint64, uint64, error) {
			if path != "owned" || !nofollow {
				t.Fatal("lstat followed a link")
			}
			if row["error"] == true {
				return 0, 0, errors.New("owned identity failure")
			}
			dev, _ := strconv.ParseUint(row["device"].(string), 10, 64)
			inode, _ := strconv.ParseUint(row["inode"].(string), 10, 64)
			return dev, inode, nil
		}
		wrapped := agentsInfoWithIdentity("owned", info, true, probe)
		actual := agentsFileIdentity(wrapped)
		expected := ""
		if source.Identities[i] != nil {
			expected = *source.Identities[i]
		}
		if actual != expected {
			t.Fatalf("Source identity row%d Go%s Source%s", i, actual, expected)
		}
	}
	for offset, path := range []string{file, link} {
		actual, e := os.Lstat(path)
		if e != nil {
			t.Fatal(e)
		}
		wrapped := withAgentsNativeIdentity(path, actual, true)
		got := agentsFileIdentity(wrapped)
		if source.Identities[16+offset] == nil || got != *source.Identities[16+offset] {
			t.Fatal("native producer physical hardlink", got, source.Identities[16+offset])
		}
	}
	if *source.Identities[16] != *source.Identities[17] {
		t.Fatal("Source hardlink producer changed identity")
	}
	directory, e := os.Stat(home)
	if e != nil {
		t.Fatal(e)
	}
	zero := agentsInfoWithIdentity(home, directory, false, func(string, bool) (uint64, uint64, error) { return 0, 0, nil })
	if agentsFileIdentity(zero) != "0:0" {
		t.Fatal("Source directory walker defined 0:0 key lost")
	}
	if agentsInfoWithIdentity("nil", nil, true, func(string, bool) (uint64, uint64, error) { t.Fatal("nil metadata queried"); return 0, 0, nil }) != nil {
		t.Fatal("nil identity metadata fabricated")
	}
}
func TestAgentIdentityFailureRetainsOriginalFileInfoForPathFallback(t *testing.T) {
	home := t.TempDir()
	info, e := os.Stat(home)
	if e != nil {
		t.Fatal(e)
	}
	wrapped := agentsInfoWithIdentity(home, info, true, func(string, bool) (uint64, uint64, error) { return 0, 0, fs.ErrPermission })
	if agentsFileIdentity(wrapped) != "" || wrapped.Name() != info.Name() || !wrapped.IsDir() {
		t.Fatal("failed identity did not retain metadata/path fallback")
	}
}

func TestActualNativeAgentDiscoveryDeduplicatesOnePhysicalHardlinkedDefinition(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		dir := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "agents")
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		file := filepath.Join(dir, "identity-original.md")
		if e := os.WriteFile(file, []byte("---\nname: physical-identity-proof\ndescription: Actual native hardlink discovery\n---\nOwned agent instructions."), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Link(file, filepath.Join(dir, "identity-alias.md")); e != nil {
			t.Fatal(e)
		}
	}, nil)
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		definitions, e := m.agents.List(scope)
		if e != nil {
			return e
		}
		count := 0
		for _, agent := range definitions.All {
			if agent.Name == "physical-identity-proof" {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("physical Stat/Lstat identity→actual Agent catalog dedup returned %d entries, sources=%#v failed=%#v config=%s", count, m.Startup.EnabledSettingSources(), definitions.FailedFiles, m.options.Native.Environment["CLAUDE_CONFIG_DIR"])
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
