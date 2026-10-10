package gitbranch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestActualSourceOriginalContracts(t *testing.T) {
	data, e := os.ReadFile("testdata/source_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Original   originalProfile
		Values     json.RawMessage
		AsyncReads json.RawMessage
		ClockCalls json.RawMessage
		CWDReads   json.RawMessage
	}
	if e = json.Unmarshal(data, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Original.Name, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			t.Setenv("HOME", home)
			t.Setenv("CC_HAHA_GIT_BRANCH_FIXTURE", "1")
			t.Setenv("CC_HAHA_FIXTURE_TEMP_ROOT", filepath.Dir(home))
			if e = os.MkdirAll(filepath.Join(home, c.Original.CWD), 0700); e != nil {
				t.Fatal(e)
			}
			for path, encoded := range c.Original.Files {
				target := filepath.Join(home, path)
				if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					t.Fatal(e)
				}
				var text string
				if json.Unmarshal(encoded, &text) == nil {
					e = os.WriteFile(target, []byte(text), 0600)
				} else {
					var v struct {
						Directory bool
						Bytes     string
					}
					if e = json.Unmarshal(encoded, &v); e != nil {
						t.Fatal(e)
					}
					if v.Directory {
						e = os.MkdirAll(target, 0700)
					} else {
						var raw []byte
						raw, e = base64.StdEncoding.DecodeString(v.Bytes)
						if e == nil {
							e = os.WriteFile(target, raw, 0600)
						}
					}
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			input := fixtureInput{Home: home, Profile: c.Original}
			encodedInput, _ := json.Marshal(input)
			path := filepath.Join(home, "input.json")
			if e = os.WriteFile(path, encodedInput, 0600); e != nil {
				t.Fatal(e)
			}
			t.Setenv("CC_HAHA_GIT_BRANCH_INPUT", path)
			TestGitBranchFixture(t)
			got, e := os.ReadFile(filepath.Join(home, "git-result.json"))
			if e != nil {
				t.Fatal(e)
			}
			want := new(bytes.Buffer)
			expected := append([]byte(`{"values":`), c.Values...)
			expected = append(expected, []byte(`,"asyncReads":`)...)
			expected = append(expected, c.AsyncReads...)
			expected = append(expected, []byte(`,"clockCalls":`)...)
			expected = append(expected, c.ClockCalls...)
			expected = append(expected, []byte(`,"cwdReads":`)...)
			expected = append(expected, c.CWDReads...)
			expected = append(expected, '}')
			if e = json.Compact(want, expected); e != nil {
				t.Fatal(e)
			}
			if string(got) != want.String() {
				t.Fatalf("actual Source original contract\nwant %s\ngot  %s", want.String(), got)
			}
		})
	}
}

type harness struct {
	s       *Service
	owner   *cooperative.Owner
	home    string
	notify  map[string]func()
	stopped map[string]int
	mu      sync.Mutex
}

func newHarness(t *testing.T, change func(*Options)) *harness {
	t.Helper()
	h := &harness{owner: cooperative.New(), home: t.TempDir(), notify: map[string]func(){}, stopped: map[string]int{}}
	o := Options{Now: func() int64 { return time.Now().UnixMilli() }, Owner: h.owner, CWD: func() string { return h.home }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, ScrollDraining: func() bool { return false }, RegisterCleanup: func(func(*cooperative.Scope) error) {}, WatchFile: func(p string, d time.Duration, f func()) (func(), error) {
		h.mu.Lock()
		h.notify[p] = f
		h.mu.Unlock()
		return func() { h.mu.Lock(); h.stopped[p]++; delete(h.notify, p); h.mu.Unlock() }, nil
	}}
	if change != nil {
		change(&o)
	}
	var e error
	h.s, e = New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := h.s.Close(context.Background()); e != nil && !errors.Is(e, cooperative.ErrClosed) {
			t.Error(e)
		}
		h.owner.Close()
	})
	return h
}
func (h *harness) call(t *testing.T, f func(*cooperative.Scope) error) {
	t.Helper()
	r, e := h.owner.Submit(context.Background(), cooperative.Read, f)
	if e == nil {
		e = r.Wait()
	}
	if e != nil {
		t.Fatal(e)
	}
}
func (h *harness) branch(t *testing.T) string {
	t.Helper()
	var f *BranchFuture
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Branch(scope); return e })
	v, e := f.Wait(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (h *harness) write(t *testing.T, p, v string) {
	t.Helper()
	p = filepath.Join(h.home, p)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(v), 0600); e != nil {
		t.Fatal(e)
	}
}
func (h *harness) signal(t *testing.T, p string) {
	t.Helper()
	h.mu.Lock()
	f := h.notify[filepath.Join(h.home, p)]
	h.mu.Unlock()
	if f == nil {
		t.Fatal("actual external registration missing", p)
	}
	f()
	h.call(t, func(*cooperative.Scope) error { return nil })
}

func TestCanonicalOwnerAwaitsOriginalBranch(t *testing.T) {
	h := newHarness(t, nil)
	h.write(t, ".git/HEAD", "ref: refs/heads/main")
	if e := h.owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := h.s.Branch(scope)
		if e != nil {
			return e
		}
		v, e := f.Await(scope)
		if e != nil {
			return e
		}
		if v != "main" {
			t.Errorf("got %s", v)
		}
		f, e = h.s.Branch(scope)
		if e != nil {
			return e
		}
		v, e = f.Await(scope)
		if v != "main" {
			t.Errorf("hot %s", v)
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestDirtyDuringReadRetainsPreviousCache(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	armed := false
	count := 0
	h := newHarness(t, func(o *Options) {
		o.ReadFile = func(p string) ([]byte, error) {
			v, e := os.ReadFile(p)
			if filepath.Base(p) == "HEAD" {
				count++
				if armed {
					close(entered)
					<-release
					armed = false
				}
			}
			return v, e
		}
	})
	h.write(t, ".git/HEAD", "ref: refs/heads/first")
	if h.branch(t) != "first" {
		t.Fatal("initial")
	}
	h.call(t, func(*cooperative.Scope) error { armed = true; h.s.branch.dirty = true; return nil })
	var f *BranchFuture
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Branch(scope); return e })
	<-entered
	h.write(t, ".git/HEAD", "ref: refs/heads/second")
	h.signal(t, ".git/config")
	close(release)
	if v, e := f.Wait(context.Background()); e != nil || v != "first" {
		t.Fatal(v, e)
	}
	h.call(t, func(*cooperative.Scope) error {
		if !h.s.branch.dirty || string(h.s.branch.value) != `"first"` {
			t.Error("dirty completion overwrote cache")
		}
		return nil
	})
	if h.branch(t) != "second" {
		t.Fatal("next dirty computation")
	}
	if count < 4 {
		t.Fatal("native HEAD reads", count)
	}
}
func TestWatcherBranchDetachAndNewRefLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	h.write(t, ".git/HEAD", "ref: refs/heads/first")
	if h.branch(t) != "first" {
		t.Fatal("first")
	}
	old := filepath.Join(h.home, ".git/refs/heads/first")
	h.write(t, ".git/HEAD", strings.Repeat("a", 40))
	h.signal(t, ".git/HEAD")
	h.s.Join()
	h.call(t, func(*cooperative.Scope) error {
		if h.s.refPath != "" {
			t.Error("detach ref not stopped")
		}
		return nil
	})
	if h.branch(t) != "HEAD" {
		t.Fatal("detach")
	}
	h.mu.Lock()
	stopped := h.stopped[old]
	h.mu.Unlock()
	if stopped != 1 {
		t.Fatal("old branch stop", stopped)
	}
	h.write(t, ".git/HEAD", "ref: refs/heads/second")
	h.signal(t, ".git/HEAD")
	h.s.Join()
	if h.branch(t) != "second" {
		t.Fatal("new")
	}
	h.call(t, func(scope *cooperative.Scope) error { return h.s.Cleanup(scope) })
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notify) != 0 {
		t.Error("watchers escaped cleanup")
	}
}
func TestScrollAndRegistrationFaultAreExplicit(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.ScrollDraining = func() bool { return true } })
	h.write(t, ".git/HEAD", "ref: refs/heads/main")
	h.branch(t)
	h.signal(t, ".git/HEAD")
	h.call(t, func(scope *cooperative.Scope) error {
		_, e := h.s.Branch(scope)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Error("active scroll not explicit", e)
		}
		return nil
	})
	h2 := newHarness(t, func(o *Options) {
		o.WatchFile = func(string, time.Duration, func()) (func(), error) { return nil, fs.ErrPermission }
	})
	h2.write(t, ".git/HEAD", "ref: refs/heads/main")
	var f *BranchFuture
	h2.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h2.s.Branch(scope); return e })
	if _, e := f.Wait(context.Background()); !errors.Is(e, fs.ErrPermission) {
		t.Fatal(e)
	}
}
func TestResolveErrorsAndNativeRootLRU(t *testing.T) {
	deny := false
	h := newHarness(t, func(o *Options) {
		o.Stat = func(p string) (fs.FileInfo, error) {
			if deny {
				return nil, fs.ErrPermission
			}
			return os.Stat(p)
		}
	})
	h.write(t, ".git/HEAD", "ref: refs/heads/main")
	deny = true
	var f *Future
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Resolve(scope, h.home); return e })
	if v, e := f.Wait(context.Background()); e != nil || string(v) != "null" {
		t.Fatal(string(v), e)
	}
	h.call(t, func(scope *cooperative.Scope) error {
		if len(h.s.dirs) != 1 {
			t.Error("negative cache")
		}
		return nil
	})
	h2 := newHarness(t, nil)
	h2.write(t, ".git", "gitdir: other")
	if e := os.Remove(filepath.Join(h2.home, ".git")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(h2.home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	h2.call(t, func(scope *cooperative.Scope) error {
		for i := 0; i < 514; i++ {
			_, e := h2.s.FindRoot(scope, filepath.Join(h2.home, strings.Repeat("x", i+1)))
			if e != nil {
				return e
			}
		}
		if len(h2.s.roots) != 512 {
			t.Error("LRU size")
		}
		first := h2.s.roots[0]
		_, e := h2.s.FindRoot(scope, first.start)
		if h2.s.roots[511] != first {
			t.Error("LRU hot entry ordering")
		}
		return e
	})
}
func TestRejectedCapabilitiesAndObservedCancellation(t *testing.T) {
	if _, e := New(Options{}); e == nil {
		t.Fatal("nil owner accepted")
	}
	h := newHarness(t, nil)
	if _, e := h.s.Branch(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	other := cooperative.New()
	defer other.Close()
	r, e := other.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		_, e := h.s.ReadHead(scope, h.home)
		if !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Wait(); e != nil {
		t.Fatal(e)
	}
	h.call(t, func(scope *cooperative.Scope) error {
		for _, v := range []string{"x\x00", string([]byte{255})} {
			if _, e := h.s.FindRoot(scope, v); e == nil {
				t.Error("root unsupported path")
			}
			if _, e := h.s.Resolve(scope, v); e == nil {
				t.Error("resolve unsupported path")
			}
		}
		return nil
	})
	f := h.s.future()
	ctx, cancel := context.WithCancelCause(context.Background())
	fault := errors.New("observer stop")
	cancel(fault)
	if _, e := f.Wait(ctx); !errors.Is(e, fault) {
		t.Fatal(e)
	}
	if _, e := f.Wait(nil); e == nil {
		t.Fatal("nil observer")
	}
	h.call(t, func(scope *cooperative.Scope) error {
		h.s.settle(scope, f, result{raw: raw("value")})
		h.s.settle(scope, f, result{raw: raw("other")})
		return nil
	})
	if v, e := f.Wait(ctx); e != nil || string(v) != `"value"` {
		t.Fatal(string(v), e)
	}
	h.call(t, func(scope *cooperative.Scope) error {
		h.s.settle(scope, f, result{})
		h.s.updateRef(scope, h.s.future(), func(*cooperative.Scope, error) {})
		return nil
	})
}
func TestResolveReadFailureAndSymrefCycle(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.ReadFile = func(string) ([]byte, error) { return nil, fs.ErrPermission } })
	h.write(t, ".git", "gitdir: somewhere")
	var f *Future
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Resolve(scope, h.home); return e })
	if v, e := f.Wait(context.Background()); e != nil || string(v) != "null" {
		t.Fatal(string(v), e)
	}
	h2 := newHarness(t, nil)
	h2.write(t, ".git/HEAD", "ref: refs/tags/a")
	h2.write(t, ".git/refs/tags/a", "ref: refs/tags/a")
	h2.call(t, func(scope *cooperative.Scope) error {
		var e error
		f, e = h2.s.ReadHead(scope, filepath.Join(h2.home, ".git"))
		return e
	})
	if _, e := f.Wait(context.Background()); e == nil {
		t.Fatal("cyclic symref silently fabricated")
	}
}
func TestJavaScriptWhitespaceAndSHA(t *testing.T) {
	if trim("\u0085x\u0085") != "\u0085x\u0085" {
		t.Fatal("NEL is not JavaScript whitespace")
	}
	if trim("\ufeff\u2009\u2028x\u3000") != "x" {
		t.Fatal("JS trim")
	}
	if validSHA(strings.Repeat("g", 40)) || SafeRef("") || SafeRef("refs//a") {
		t.Fatal("invalid ref accepted")
	}
}

func TestInvalidOriginalCWDAndFailurePropagation(t *testing.T) {
	h := newHarness(t, nil)
	o := h.s.options
	o.CWD = func() string { return string([]byte{255}) }
	if _, e := New(o); e != nil {
		t.Fatal("constructor must not call original CWD", e)
	}
	cwd := h.home
	h2 := newHarness(t, func(o *Options) { o.CWD = func() string { return cwd } })
	cwd = string([]byte{255})
	var f *BranchFuture
	h2.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h2.s.Branch(scope); return e })
	if _, e := f.Wait(context.Background()); e == nil {
		t.Fatal("original state failure disappeared")
	}
	cwd = h.home
	h.write(t, ".git/HEAD", "ref: refs/heads/main")
	h.optionsCWDForTest(t, &cwd)
	if h.branch(t) != "main" {
		t.Fatal("initial")
	}
	cwd = "bad\x00"
	h.call(t, func(scope *cooperative.Scope) error {
		h.s.branch.dirty = true
		var e error
		f, e = h.s.Branch(scope)
		return e
	})
	if _, e := f.Wait(context.Background()); e == nil {
		t.Fatal("late CWD capability disappeared")
	}
	h3 := newHarness(t, nil)
	h3.write(t, ".git/HEAD", "ref: refs/tags/a")
	h3.write(t, ".git/refs/tags/a", "ref: refs/tags/a")
	h3.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h3.s.Branch(scope); return e })
	if _, e := f.Wait(context.Background()); e == nil {
		t.Fatal("initial ref cycle hidden")
	}
}
func (h *harness) optionsCWDForTest(t *testing.T, cwd *string) {
	h.call(t, func(*cooperative.Scope) error { h.s.options.CWD = func() string { return *cwd }; return nil })
}
func TestActualRegistrationFailuresStopExistingWatches(t *testing.T) {
	for _, target := range []string{"config", "refs/heads/main"} {
		t.Run(target, func(t *testing.T) {
			h := newHarness(t, func(o *Options) {
				o.NodeEnv = "test"
				watch := o.WatchFile
				o.WatchFile = func(p string, d time.Duration, f func()) (func(), error) {
					if d != 10*time.Millisecond {
						t.Error("Source test interval")
					}
					if strings.HasSuffix(filepath.ToSlash(p), target) {
						return nil, fs.ErrPermission
					}
					return watch(p, d, f)
				}
			})
			h.write(t, ".git/HEAD", "ref: refs/heads/main")
			var f *BranchFuture
			h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Branch(scope); return e })
			if _, e := f.Wait(context.Background()); !errors.Is(e, fs.ErrPermission) {
				t.Fatal(e)
			}
		})
	}
}
func TestClosedServiceAndForeignFutureAreExplicit(t *testing.T) {
	h := newHarness(t, nil)
	f := h.s.future()
	other := cooperative.New()
	r, e := other.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		if _, e := f.Await(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		if _, e := (&BranchFuture{f}).Await(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error(e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Wait(); e != nil {
		t.Fatal(e)
	}
	other.Close()
	if e := h.s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.call(t, func(scope *cooperative.Scope) error {
		for _, f := range []func() error{func() error { _, e := h.s.FindRoot(scope, h.home); return e }, func() error { _, e := h.s.Resolve(scope, h.home); return e }, func() error { _, e := h.s.ReadHead(scope, h.home); return e }, func() error { _, e := h.s.Branch(scope); return e }, func() error { return h.s.Reset(scope) }, func() error { return h.s.ClearDirs(scope) }, func() error { return h.s.ClearRoots(scope) }} {
			if e := f(); !errors.Is(e, cooperative.ErrClosed) {
				t.Error(e)
			}
		}
		return nil
	})
}
func TestOwnerUnavailableRejectsPhysicalCompletionWithoutReducer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := newHarness(t, func(o *Options) {
		o.ReadFile = func(string) ([]byte, error) { close(entered); <-release; return []byte("ref: refs/heads/private"), nil }
	})
	var f *Future
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.ReadHead(scope, h.home); return e })
	<-entered
	h.owner.Close()
	close(release)
	h.s.Join()
	if _, e := f.Wait(context.Background()); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	// Rejection changes only this structural future receipt; no cache reduction ran.
	if h.s.branch != nil {
		t.Fatal("business worker cache mutation")
	}
	h.s.post(f, func(*cooperative.Scope) { t.Error("closed callback executed") })
	h.s.post(nil, func(*cooperative.Scope) { t.Error("closed callback executed") })
}
func TestOriginalClockCallsStayOnOwnerAndCWDNotReadAtConstruction(t *testing.T) {
	h := newHarness(t, nil)
	clock, cwd := 0, 0
	h.call(t, func(scope *cooperative.Scope) error {
		h.s.options.Now = func() int64 { clock++; return int64(clock) }
		h.s.options.CWD = func() string { cwd++; return h.home }
		f, e := h.s.Branch(scope)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		if e != nil {
			return e
		}
		f, e = h.s.Branch(scope)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		return e
	})
	if clock != 2 || cwd != 2 {
		t.Fatal("original cold/hot calls", clock, cwd)
	}
}
func TestInitialMissingHEADAndActiveScrollIsCappedBeforeBranchResult(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.ScrollDraining = func() bool { return true }
		o.WatchFile = func(p string, d time.Duration, notify func()) (func(), error) {
			if filepath.Base(p) == "HEAD" {
				notify()
			}
			return func() {}, nil
		}
	})
	h.write(t, ".git/config", "")
	var f *BranchFuture
	h.call(t, func(scope *cooperative.Scope) error { var e error; f, e = h.s.Branch(scope); return e })
	if _, e := f.Wait(context.Background()); e == nil {
		t.Fatal("unported active scroll was silently emitted")
	}
}
