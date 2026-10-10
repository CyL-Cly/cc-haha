package clibootstrap

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalProjectKeyUsesVerifiedWorktreeAndPreservesFallbackMemo(t *testing.T) {
	for _, kind := range []string{"normal", "valid", "bare", "bad-structure", "bad-backlink", "missing-commondir"} {
		t.Run(kind, func(t *testing.T) {
			n := nativeForTest(t)
			common := filepath.Join(n.Home, "main", ".git")
			if kind == "bare" {
				common = filepath.Join(n.Home, "bare.git")
			}
			gitdir := filepath.Join(common, "worktrees", "private")
			if kind == "bad-structure" {
				gitdir = filepath.Join(n.Home, "forged")
			}
			if e := os.MkdirAll(gitdir, 0700); e != nil {
				t.Fatal(e)
			}
			if kind == "normal" {
				os.MkdirAll(filepath.Join(n.CWD, ".git"), 0700)
			} else {
				os.WriteFile(filepath.Join(n.CWD, ".git"), []byte("\ufeffgitdir: "+gitdir+"\n"), 0600)
				if kind != "missing-commondir" {
					os.WriteFile(filepath.Join(gitdir, "commondir"), []byte(common+"\n"), 0600)
				}
				backlink := filepath.Join(n.CWD, ".git")
				if kind == "bad-backlink" {
					backlink = filepath.Join(n.Home, "unrelated")
					os.WriteFile(backlink, []byte("existing"), 0600)
				}
				os.WriteFile(filepath.Join(gitdir, "gitdir"), []byte(backlink+"\n"), 0600)
			}
			args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
			if e != nil {
				t.Fatal(e)
			}
			var out, errOut bytes.Buffer
			p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			e = p.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := p.state.SetOriginalCWD(scope, n.CWD); e != nil {
					return e
				}
				key, e := p.originalProjectConfigKey(scope)
				if e != nil {
					return e
				}
				want := n.CWD
				if kind == "valid" {
					want = filepath.Dir(common)
				} else if kind == "bare" {
					want = common
				}
				if key != want {
					t.Fatal(kind, key, want)
				}
				// Source getProjectPathForConfig memoizes before an active mutation.
				if kind != "normal" {
					os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("/untrusted"), 0600)
				}
				again, e := p.originalProjectConfigKey(scope)
				if e != nil || again != key {
					t.Fatal("memo changed after native mutation", again, e)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
