package gitbranch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type originalOp struct {
	Kind, Path, Value string
	Count             int
}
type originalProfile struct {
	Name, CWD  string
	ClockStart int64
	Files      map[string]json.RawMessage
	Ops        []originalOp
}
type fixtureInput struct {
	Home    string
	Profile originalProfile
}
type observation struct {
	Kind      string          `json:"kind"`
	Operation string          `json:"operation,omitempty"`
	Settled   json.RawMessage `json:"settled,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
}

type fixtureGitAwaitOperation struct {
	start  func(*cooperative.Scope) (*Future, error)
	finish func(json.RawMessage)
}

// Consecutive original await calls resume in their actual Promise reactions on
// one owner stack. An off-owner Wait followed by another Submit admits an I/O
// turn between calls that Source's async caller did not request. This is a test
// host correction, not a change to Service or native-watch admission policy.
func runFixtureGitAwaitChain(owner *cooperative.Owner, operations []fixtureGitAwaitOperation) error {
	return owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, operation := range operations {
			future, err := operation.start(scope)
			if err != nil {
				return err
			}
			value, err := future.Await(scope)
			if err != nil {
				return err
			}
			operation.finish(value)
		}
		return nil
	})
}

func TestGitBranchFixture(t *testing.T) {
	inputPath := os.Getenv("CC_HAHA_GIT_BRANCH_INPUT")
	if inputPath == "" {
		t.Skip("private fixture only")
	}
	home, e := filepath.EvalSymlinks(os.Getenv("HOME"))
	if e != nil || home != os.Getenv("HOME") || os.Getenv("CC_HAHA_GIT_BRANCH_FIXTURE") != "1" || !strings.HasPrefix(home, filepath.Clean(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))+string(filepath.Separator)) {
		t.Fatal("real disposable HOME and explicit original fixture required")
	}
	if !strings.HasPrefix(filepath.Clean(inputPath), home+string(filepath.Separator)) {
		t.Fatal("input path escaped private HOME")
	}
	data, e := os.ReadFile(inputPath)
	if e != nil {
		t.Fatal(e)
	}
	var input fixtureInput
	if e = json.Unmarshal(data, &input); e != nil {
		t.Fatal(e)
	}
	if input.Home != home {
		t.Fatal("original HOME mismatch")
	}
	local := func(v string) string {
		p := filepath.Join(home, v)
		if p != home && !strings.HasPrefix(p, home+string(filepath.Separator)) {
			t.Fatal("private Git path escape")
		}
		return p
	}
	var guardMu sync.Mutex
	blocked := []string{}
	absent := map[string]bool{}
	for parent := filepath.Dir(home); ; parent = filepath.Dir(parent) {
		absent[filepath.Join(parent, ".git")] = true
		if parent == filepath.Dir(parent) {
			break
		}
	}
	allowed := func(p string) bool {
		path := filepath.Clean(p)
		candidate := path
		for {
			_, e := os.Lstat(candidate)
			if e == nil {
				break
			}
			if !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, syscall.ENOTDIR) {
				return false
			}
			parent := filepath.Dir(candidate)
			if parent == candidate {
				return false
			}
			candidate = parent
		}
		canonical, e := filepath.EvalSymlinks(candidate)
		if e != nil {
			return false
		}
		suffix, e := filepath.Rel(candidate, path)
		if e != nil {
			return false
		}
		resolved := filepath.Join(canonical, suffix)
		return resolved == home || strings.HasPrefix(resolved, home+string(filepath.Separator))
	}
	deny := func(p string) error {
		guardMu.Lock()
		blocked = append(blocked, p)
		guardMu.Unlock()
		return fs.ErrPermission
	}
	stat := func(p string) (fs.FileInfo, error) {
		if !strings.HasPrefix(p, home+string(filepath.Separator)) && p != home {
			if absent[filepath.Clean(p)] {
				return nil, fs.ErrNotExist
			}
			return nil, deny(p)
		}
		if !allowed(p) {
			return nil, deny(p)
		}
		return os.Stat(p)
	}
	type ioObservation struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
	}
	var ioMu sync.Mutex
	asyncReads := []ioObservation{}
	recordIO := func(op, path string) {
		rel, _ := filepath.Rel(home, path)
		ioMu.Lock()
		asyncReads = append(asyncReads, ioObservation{op, filepath.ToSlash(rel)})
		ioMu.Unlock()
	}
	owner := cooperative.New()
	watch := &NativePollWatch{Owner: owner, Stat: stat}
	var callbacks []func(*cooperative.Scope) error
	clockCalls := []int64{}
	cwdReads := []string{}
	diagnostic := []map[string]any{}
	watchHost := &fixtureWatchHost{headPath: local("work/.git/HEAD")}
	var service *Service
	service, e = New(Options{Now: func() int64 {
		v := input.Profile.ClockStart + int64(len(clockCalls))
		clockCalls = append(clockCalls, v)
		return v
	}, Owner: owner, CWD: func() string { v := local(input.Profile.CWD); cwdReads = append(cwdReads, v); return v }, StatSync: stat, Stat: func(p string) (fs.FileInfo, error) { recordIO("stat", p); return stat(p) }, ReadFile: func(p string) ([]byte, error) {
		recordIO("read", p)
		if !allowed(p) {
			return nil, deny(p)
		}
		return os.ReadFile(p)
	}, WatchFileOwned: watch.WatchOwned, ScrollDraining: func() bool { return false }, RegisterCleanup: func(f func(*cooperative.Scope) error) { callbacks = append(callbacks, f) }, NodeEnv: "production", ObserveWatchCallbackEnd: func(scope *cooperative.Scope, path string) {
		watchHost.callbackEnd(scope, path)
		if os.Getenv("CC_HAHA_GIT_NATIVE_DIAGNOSTIC") == "1" {
			ioMu.Lock()
			reads := len(asyncReads)
			ioMu.Unlock()
			rel, _ := filepath.Rel(home, path)
			diagnostic = append(diagnostic, map[string]any{"kind": "callbackEnd", "path": filepath.ToSlash(rel), "hasBranch": service.branch != nil, "dirty": service.branch != nil && service.branch.dirty, "asyncReads": reads, "cwdReads": len(cwdReads)})
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	watchHost.service = service
	defer func() { _ = service.Close(context.Background()); watch.Join(); owner.Close() }()
	call := func(f func(*cooperative.Scope) error) {
		r, e := owner.Submit(context.Background(), cooperative.Read, f)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	values := []observation{}
	operation := func(current originalOp) fixtureGitAwaitOperation {
		return fixtureGitAwaitOperation{start: func(scope *cooperative.Scope) (*Future, error) {
			var f *Future
			var err error
			switch current.Kind {
			case "head":
				f, err = service.ReadHead(scope, local(current.Path))
			case "resolve":
				f, err = service.Resolve(scope, local(current.Path))
			default:
				var b *BranchFuture
				b, err = service.Branch(scope)
				if b != nil {
					f = b.Future
				}
			}
			if err != nil {
				return nil, err
			}
			settled := false
			service.then(scope, f, func(*cooperative.Scope, result) { settled = true })
			values = append(values, observation{Kind: "returned", Operation: current.Kind, Settled: raw(settled)})
			return f, nil
		}, finish: func(value json.RawMessage) {
			if os.Getenv("CC_HAHA_GIT_NATIVE_DIAGNOSTIC") == "1" {
				diagnostic = append(diagnostic, map[string]any{"kind": "callerFinish", "operation": current.Kind, "hasBranch": service.branch != nil, "dirty": service.branch != nil && service.branch.dirty, "cwdReads": len(cwdReads)})
			}
			values = append(values, observation{Kind: current.Kind, Value: value})
		}}
	}
	for index := 0; index < len(input.Profile.Ops); index++ {
		op := input.Profile.Ops[index]
		if op.Kind == "parallelBranches" {
			chain := []fixtureGitAwaitOperation{{start: func(scope *cooperative.Scope) (*Future, error) {
				var futures []*BranchFuture
				states := make([]bool, op.Count)
				for i := 0; i < op.Count; i++ {
					f, err := service.Branch(scope)
					if err != nil {
						return nil, err
					}
					futures = append(futures, f)
					index := i
					service.then(scope, f.Future, func(*cooperative.Scope, result) { states[index] = true })
				}
				values = append(values, observation{Kind: "parallelReturned", Settled: raw(append([]bool(nil), states...))})
				// Source awaits the actual Promise.all result, then immediately
				// enters the following original await on the same caller stack.
				all := service.future()
				list := make([]string, len(futures))
				remaining := len(futures)
				if remaining == 0 {
					service.settle(scope, all, result{raw: raw(list)})
				}
				for i, f := range futures {
					service.then(scope, f.Future, func(next *cooperative.Scope, r result) {
						if r.err != nil {
							service.settle(next, all, r)
							return
						}
						if err := json.Unmarshal(r.raw, &list[i]); err != nil {
							service.settle(next, all, result{err: err})
							return
						}
						remaining--
						if remaining == 0 {
							service.settle(next, all, result{raw: raw(list)})
						}
					})
				}
				return all, nil
			}, finish: func(list json.RawMessage) {
				if os.Getenv("CC_HAHA_GIT_NATIVE_DIAGNOSTIC") == "1" {
					diagnostic = append(diagnostic, map[string]any{"kind": "parallelFinish", "hasBranch": service.branch != nil, "dirty": service.branch != nil && service.branch.dirty, "cwdReads": len(cwdReads)})
				}
				values = append(values, observation{Kind: op.Kind, Value: list})
			}}}
			next := index + 1
		parallelTail:
			for ; next < len(input.Profile.Ops); next++ {
				current := input.Profile.Ops[next]
				switch current.Kind {
				case "parallelBranches", "write", "watchChange", "reset", "clearDirs", "clearRoots", "find":
					break parallelTail
				}
				chain = append(chain, operation(current))
			}
			index = next - 1
			if err := runFixtureGitAwaitChain(owner, chain); err != nil {
				t.Fatal(err)
			}
			continue
		}

		if op.Kind == "write" || op.Kind == "watchChange" {
			p := local(op.Path)
			write := func() error {
				if err := os.MkdirAll(filepath.Dir(p), 0777); err != nil {
					return err
				}
				return os.WriteFile(p, []byte(op.Value), 0666)
			}
			if op.Kind == "watchChange" {
				// Pinned Bun uses a native ImmediateObject batch, separately from
				// Promise reactions and ready FS callbacks. Both actual awaits
				// stay on this Owner; HEAD is still exact callback-end completion.
				var missingRefs []string
				if input.Profile.Name == "watch-head-dirty" {
					// The original native case is timing-sensitive when loose refs do not
					// exist. This declared caller waits for exactly those two first callbacks
					// on both implementations; old values/read/clock/CWD goldens stay exact.
					missingRefs = []string{local("work/.git/refs/heads/first"), local("work/.git/refs/heads/second")}
				}
				e = watchHost.change(owner, (*cooperative.Scope).AwaitImmediate, write, missingRefs...)
			} else {
				e = write()
			}
			if e != nil {
				t.Fatal(e)
			}
			values = append(values, observation{Kind: op.Kind})
			continue
		}
		if op.Kind == "reset" || op.Kind == "clearDirs" || op.Kind == "clearRoots" {
			call(func(scope *cooperative.Scope) error {
				switch op.Kind {
				case "reset":
					return service.Reset(scope)
				case "clearDirs":
					return service.ClearDirs(scope)
				default:
					return service.ClearRoots(scope)
				}
			})
			values = append(values, observation{Kind: op.Kind})
			continue
		}
		if op.Kind == "find" {
			var v string
			call(func(scope *cooperative.Scope) error { v, e = service.FindRoot(scope, local(op.Path)); return e })
			values = append(values, observation{Kind: op.Kind, Value: nullable(v)})
			continue
		}
		var chain []fixtureGitAwaitOperation
	chainScan:
		for ; index < len(input.Profile.Ops); index++ {
			current := input.Profile.Ops[index]
			switch current.Kind {
			case "parallelBranches", "write", "watchChange", "reset", "clearDirs", "clearRoots", "find":
				break chainScan
			}
			chain = append(chain, operation(current))
		}
		index--
		if err := runFixtureGitAwaitChain(owner, chain); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("CC_HAHA_GIT_NATIVE_DIAGNOSTIC") == "1" {
		bytes, _ := json.Marshal(diagnostic)
		t.Logf("actual native observer: %s", bytes)
	}
	call(func(scope *cooperative.Scope) error { return service.Reset(scope) })
	service.Join()
	guardMu.Lock()
	if len(blocked) != 0 {
		guardMu.Unlock()
		t.Fatal("fixture capability violation", blocked)
	}
	guardMu.Unlock()
	ioMu.Lock()
	out, e := json.Marshal(struct {
		Values     []observation   `json:"values"`
		AsyncReads []ioObservation `json:"asyncReads"`
		ClockCalls []int64         `json:"clockCalls"`
		CWDReads   []string        `json:"cwdReads"`
	}{values, append([]ioObservation{}, asyncReads...), clockCalls, cwdReads})
	ioMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	out = []byte(strings.ReplaceAll(string(out), home, "<HOME>"))
	if e = os.WriteFile(filepath.Join(home, "git-result.json"), out, 0600); e != nil {
		t.Fatal(e)
	}
}
