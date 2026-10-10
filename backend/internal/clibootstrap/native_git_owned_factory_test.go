package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// This exercises the actual MainPrint factory's Git service and native boundary,
// without substituting its watcher or running a model. The two cold requests and
// next hot request stay on the same caller/Owner, as in the original fixture.
func TestMainPrintFactoryOwnsInitialMissingRefReadyBeforeColdResults(t *testing.T) {
	n := nativeForTest(t)
	head := filepath.Join(n.CWD, ".git", "HEAD")
	if e := os.MkdirAll(filepath.Dir(head), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0600); e != nil {
		t.Fatal(e)
	}
	args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	main, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer main.Close()
	err := main.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		all, e := ownerasync.NewOwnerFuture[struct{}](main.Owner)
		if e != nil {
			return e
		}
		remaining := 2
		for i := 0; i < 2; i++ {
			f, e := main.Git.Branch(scope)
			if e != nil {
				return e
			}
			if e = f.ThenOwner(scope, func(next *cooperative.Scope, value string, err error) {
				if err != nil {
					_ = all.Reject(next, err)
					return
				}
				if value != "main" {
					_ = all.Reject(next, errors.New("actual cold branch not main"))
					return
				}
				remaining--
				if remaining == 0 {
					_ = all.Resolve(next, struct{}{})
				}
			}, func(err error) { _ = all.RejectAdministrative(err) }); e != nil {
				return e
			}
		}
		if _, e = all.Await(scope); e != nil {
			return e
		}
		f, e := main.Git.Branch(scope)
		if e != nil {
			return e
		}
		value, e := f.Await(scope)
		if e != nil {
			return e
		}
		if value != "main" {
			return errors.New("actual hot branch not main")
		}
		return main.Git.Cleanup(scope)
	})
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	reads := 0
	for _, event := range n.Trace {
		if event.Operation == "readFile" && event.Path == head {
			reads++
		}
	}
	n.mu.Unlock()
	if reads != 3 {
		t.Fatalf("actual MainPrint native Git HEAD reads=%d, want3 (one watcher init/two cold/zero hot)", reads)
	}
	if out.Len() != 0 || errOut.Len() != 0 || main.Host != nil {
		t.Fatal("Git component factory unexpectedly performed ASK/output", out.String(), errOut.String())
	}
}
