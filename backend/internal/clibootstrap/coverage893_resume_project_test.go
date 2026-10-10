package clibootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverage893NativeWorktreeRelativePathsAndBrokenBacklinksPreserveProjectRoot(t *testing.T) {
	for _, kind := range []string{"relative-valid", "missing-backlink", "dangling-backlink", "not-gitdir"} {
		t.Run(kind, func(t *testing.T) {
			n := nativeForTest(t)
			common := filepath.Join(n.Home, "main", ".git")
			gitdir := filepath.Join(common, "worktrees", "private")
			if e := os.MkdirAll(gitdir, 0700); e != nil {
				t.Fatal(e)
			}
			relative, e := filepath.Rel(n.CWD, gitdir)
			if e != nil {
				t.Fatal(e)
			}
			text := "gitdir: " + relative + "\n"
			if kind == "not-gitdir" {
				text = "private unrelated pointer\n"
			}
			if e = os.WriteFile(filepath.Join(n.CWD, ".git"), []byte(text), 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0600); e != nil {
				t.Fatal(e)
			}
			if kind != "missing-backlink" {
				back := filepath.Join(n.CWD, ".git")
				if kind == "dangling-backlink" {
					back = filepath.Join(n.Home, "missing-private-file")
				}
				if e = os.WriteFile(filepath.Join(gitdir, "gitdir"), []byte(back), 0600); e != nil {
					t.Fatal(e)
				}
			}
			p := &MainPrint{options: MainPrintOptions{Native: n}}
			got := p.canonicalProjectRoot(n.CWD)
			want := n.CWD
			if kind == "relative-valid" {
				want = filepath.Dir(common)
			}
			if got != want {
				t.Fatal(kind, got, want)
			}
		})
	}
}
func TestCoverage893PublicMissingResumeStopsBeforeInputAndProvider(t *testing.T) {
	n := nativeForTest(t)
	args := mainRecipeArgs()
	args[len(args)-2] = "--resume"
	parsed, e := ParsePrintArguments(args, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: parsed, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/cost\"}}\n"), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	code, e := p.Run(context.Background())
	if e == nil || code != 1 || !strings.Contains(e.Error(), "No conversation found") {
		t.Fatal(code, e)
	}
	if !p.closed || out.Len() != 0 {
		t.Fatal("missing resume entered normal session", out.String())
	}
}
