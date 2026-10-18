package terminaldiff

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualWholeSourceGitFetchUsesPhysicalRepositoryAndTransientState(t *testing.T) {
	for _, mode := range []string{"modified", "merge", "non-repository"} {
		t.Run(mode, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			work := filepath.Join(home, "work")
			for _, dir := range []string{work, filepath.Join(home, "bin"), filepath.Join(home, ".claude")} {
				if e = os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.WriteFile(filepath.Join(home, "bin/git"), []byte("#!/bin/sh\nexec /usr/bin/git \"$@\"\n"), 0700); e != nil {
				t.Fatal(e)
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("/usr/bin/git", args...)
				cmd.Dir = work
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
				out, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatal(e, string(out))
				}
				return CommandOutput(string(out))
			}
			if mode != "non-repository" {
				git("init", "-q")
				git("config", "user.name", "Private Source diff")
				git("config", "user.email", "fixture@example.invalid")
				if e = os.WriteFile(filepath.Join(work, "tracked.txt"), []byte("old\n"), 0600); e != nil {
					t.Fatal(e)
				}
				git("add", "tracked.txt")
				git("commit", "-qm", "private physical repo")
				if e = os.WriteFile(filepath.Join(work, "tracked.txt"), []byte("new\n"), 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(work, "untracked.txt"), []byte("not staged"), 0600); e != nil {
					t.Fatal(e)
				}
				if mode == "merge" {
					if e = os.WriteFile(filepath.Join(work, ".git/MERGE_HEAD"), []byte("owned transient"), 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			result := originalDiffHome(t, home, map[string]any{"physical": true, "cwd": work})
			var physical struct {
				Stats   *protocol.JSONObject
				Hunks   map[string][]Hunk
				Blocked []string
			}
			if e = json.Unmarshal(result.Get("physical"), &physical); e != nil {
				t.Fatal(e, string(result.Get("physical")))
			}
			if len(physical.Blocked) != 0 {
				t.Fatal("original escaped private capabilities", physical.Blocked)
			}
			if mode != "modified" {
				if physical.Stats != nil || len(physical.Hunks) != 0 {
					t.Fatal("original transient/nonrepo changed", string(result.Get("physical")))
				}
				return
			}
			actual, e := Current(git("diff", "HEAD", "--shortstat"), git("diff", "HEAD", "--numstat"), git("ls-files", "--others", "--exclude-standard"), git("diff", "HEAD"))
			if e != nil {
				t.Fatal(e)
			}
			var stats Stats
			if e = json.Unmarshal(physical.Stats.Get("stats"), &stats); e != nil {
				t.Fatal(e)
			}
			if !jsonSame(actual.Stats, stats) || !jsonSame(ParseHunks(git("diff", "HEAD")), physical.Hunks) {
				t.Fatalf("actual Git diff mismatch %s", result.Get("physical"))
			}
			var per map[string]protocol.JSONObject
			_ = json.Unmarshal(physical.Stats.Get("perFileStats"), &per)
			if len(actual.Files) != len(per) {
				t.Fatal(actual.Files, per)
			}
			for _, file := range actual.Files {
				row := per[file.Path]
				if file.LinesAdded != int(numberJSON(row.Get("added"))) || file.LinesRemoved != int(numberJSON(row.Get("removed"))) || file.IsUntracked != (string(row.Get("isUntracked")) == "true") {
					t.Fatal(file, string(row.Get("added")))
				}
			}
		})
	}
}
