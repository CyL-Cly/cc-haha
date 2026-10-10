package concurrentsessions

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func nativeFixture(t *testing.T) (*cooperative.Owner, *NativeFileSystem) {
	t.Helper()
	owner := cooperative.New()
	files, err := NewNativeFileSystem(owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Close()
		if err := files.Join(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return owner, files
}

func TestNativeRoundTripModesCopyAndDirectoryOrder(t *testing.T) {
	owner, files := nativeFixture(t)
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		made, err := files.Mkdir(scope, dir, 0o700)
		if err != nil {
			return err
		}
		if _, err = made.Await(scope); err != nil {
			return err
		}
		changed, err := files.Chmod(scope, dir, 0o710)
		if err != nil {
			return err
		}
		if _, err = changed.Await(scope); err != nil {
			return err
		}
		stat, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if stat.Mode().Perm() != 0o710 {
			return errors.New("native chmod mode lost")
		}
		for _, name := range []string{"z-last.json", "a-first.json", "middle.json"} {
			data := []byte{0, 0xff, '=', 'x'}
			written, err := files.WriteFile(scope, filepath.Join(dir, name), data, 0o600)
			if err != nil {
				return err
			}
			copy(data, []byte("edit"))
			if _, err = written.Await(scope); err != nil {
				return err
			}
			read, err := files.ReadFile(scope, filepath.Join(dir, name))
			if err != nil {
				return err
			}
			actual, err := read.Await(scope)
			if err != nil {
				return err
			}
			if !bytes.Equal(actual, []byte{0, 0xff, '=', 'x'}) {
				return errors.New("write input was not copied")
			}
			stat, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			if stat.Mode().Perm() != 0o600 {
				return errors.New("native write mode lost")
			}
		}
		opened, err := os.Open(dir)
		if err != nil {
			return err
		}
		expected, err := opened.Readdirnames(-1)
		closed := opened.Close()
		if err != nil {
			return err
		}
		if closed != nil {
			return closed
		}
		listing, err := files.ReadDir(scope, dir)
		if err != nil {
			return err
		}
		actual, err := listing.Await(scope)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actual, expected) {
			return errors.New("native directory order changed")
		}
		for _, name := range actual {
			removed, err := files.Unlink(scope, filepath.Join(dir, name))
			if err != nil {
				return err
			}
			if _, err = removed.Await(scope); err != nil {
				return err
			}
		}
		listing, err = files.ReadDir(scope, dir)
		if err != nil {
			return err
		}
		actual, err = listing.Await(scope)
		if err != nil {
			return err
		}
		if len(actual) != 0 {
			return errors.New("actual files not removed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeErrorsRemainPathErrorsAndErrno(t *testing.T) {
	owner, files := nativeFixture(t)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, start := range []func() (error, error){
			func() (error, error) {
				f, e := files.Chmod(scope, missing, 0o600)
				if e != nil {
					return nil, e
				}
				_, e = f.Await(scope)
				return e, nil
			},
			func() (error, error) {
				f, e := files.WriteFile(scope, filepath.Join(missing, "file"), nil, 0o600)
				if e != nil {
					return nil, e
				}
				_, e = f.Await(scope)
				return e, nil
			},
			func() (error, error) {
				f, e := files.ReadFile(scope, missing)
				if e != nil {
					return nil, e
				}
				_, e = f.Await(scope)
				return e, nil
			},
			func() (error, error) {
				f, e := files.ReadDir(scope, missing)
				if e != nil {
					return nil, e
				}
				_, e = f.Await(scope)
				return e, nil
			},
			func() (error, error) {
				f, e := files.Unlink(scope, missing)
				if e != nil {
					return nil, e
				}
				_, e = f.Await(scope)
				return e, nil
			},
		} {
			failure, admission := start()
			if admission != nil {
				return admission
			}
			var pathError *os.PathError
			if !errors.Is(failure, fs.ErrNotExist) || !errors.As(failure, &pathError) {
				return errors.New("native errno/path error replaced")
			}
		}
		regular := filepath.Join(filepath.Dir(missing), "regular")
		if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
			return err
		}
		made, err := files.Mkdir(scope, filepath.Join(regular, "child"), 0o700)
		if err != nil {
			return err
		}
		if _, err = made.Await(scope); err == nil {
			return errors.New("mkdir on file did not reject")
		}
		listing, err := files.ReadDir(scope, regular)
		if err != nil {
			return err
		}
		if _, err = listing.Await(scope); err == nil {
			return errors.New("readdir on file did not reject")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAdmissionRejectsForeignStaleAndCancelledScopes(t *testing.T) {
	owner, files := nativeFixture(t)
	foreign := cooperative.New()
	defer foreign.Close()
	path := filepath.Join(t.TempDir(), "forbidden")
	if _, err := NewNativeFileSystem(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := files.WriteFile(scope, path, []byte("x"), 0o600); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign scope admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var stale *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error { stale = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Mkdir(stale, path, 0o700); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err := files.ReadFile(nil, path); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("original caller cancellation")
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel(cause)
		if _, err := files.WriteFile(scope, path, []byte("x"), 0o600); !errors.Is(err, cause) {
			return errors.New("cancelled scope started native write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("rejected scope caused FS effect", err)
	}
	if err := files.Join(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	var absent *NativeFileSystem
	if err := absent.Join(context.Background()); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err := absent.ReadFile(stale, path); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
}

func TestNativeCallerCancelLeavesAdmittedPromiseAndOwnerReactionAlive(t *testing.T) {
	owner, files := nativeFixture(t)
	path := filepath.Join(t.TempDir(), "actual")
	if err := os.WriteFile(path, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	gate, started, admitted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("cancel observer, not physical FS")
	var future *ownerasync.OwnerFuture[[]byte]
	var child *ownerasync.OwnerFuture[struct{}]
	finished := make(chan error, 1)
	go func() {
		finished <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			future, err = startNative(files, scope, func() ([]byte, error) { close(started); <-gate; return os.ReadFile(path) })
			if err != nil {
				return err
			}
			child, err = future.Then(scope, func(next *cooperative.Scope, value []byte, failure error) error {
				if failure != nil {
					return failure
				}
				if !next.BelongsTo(owner) || !bytes.Equal(value, []byte("result")) {
					return errors.New("wrong owner/result")
				}
				if err := next.Frame(cooperative.FrameFeed, func() error { return nil }); !errors.Is(err, cooperative.ErrRestricted) {
					return errors.New("native completion got Frame capability")
				}
				return nil
			})
			if err != nil {
				return err
			}
			close(admitted)
			_, err = future.Await(scope)
			return err
		})
	}()
	<-started
	<-admitted
	cancel(cause)
	if err := <-finished; !errors.Is(err, cause) {
		t.Fatal(err)
	}
	select {
	case <-future.Done():
		t.Fatal("caller cancellation settled independent native promise")
	default:
	}
	cancelled, stop := context.WithCancelCause(context.Background())
	stop(cause)
	if err := files.Join(cancelled); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	close(gate)
	if err := files.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if value, err := future.Wait(context.Background()); err != nil || !bytes.Equal(value, []byte("result")) {
		t.Fatal(string(value), err)
	}
}

func TestNativeOwnerCloseRejectsObligationWithoutWaitingForPhysicalIO(t *testing.T) {
	owner, files := nativeFixture(t)
	path := filepath.Join(t.TempDir(), "late-native-file")
	gate, started := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	var future *ownerasync.OwnerFuture[struct{}]
	var child *ownerasync.OwnerFuture[struct{}]
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		future, err = startNative(files, scope, func() (struct{}, error) {
			close(started)
			<-gate
			return struct{}{}, os.WriteFile(path, []byte("real late native effect"), 0o600)
		})
		if err != nil {
			return err
		}
		child, err = future.Then(scope, func(*cooperative.Scope, struct{}, error) error {
			t.Error("business reaction ran after Owner.Close")
			return nil
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	owner.Close()
	if _, err := future.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := child.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	select {
	case <-files.idle:
		t.Fatal("physical worker disappeared when owner closed")
	default:
	}
	close(gate)
	if err := files.Join(context.Background()); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != "real late native effect" {
		t.Fatal(string(actual), err)
	}
}

func TestNativeUnlinkRejectsDirectoryAndPreservesSymlinkTarget(t *testing.T) {
	owner, files := nativeFixture(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty-directory")
	target := filepath.Join(dir, "target-file")
	link := filepath.Join(dir, "symbolic-link")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("retained-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		removed, err := files.Unlink(scope, empty)
		if err != nil {
			return err
		}
		_, err = removed.Await(scope)
		if err == nil {
			return errors.New("unlink accepted actual empty directory")
		}
		var pathError *os.PathError
		if !errors.As(err, &pathError) || pathError.Op != "unlink" {
			return errors.New("unlink lost native path error")
		}
		stat, err := os.Stat(empty)
		if err != nil || !stat.IsDir() {
			return errors.New("rejected unlink removed directory")
		}
		removed, err = files.Unlink(scope, link)
		if err != nil {
			return err
		}
		if _, err = removed.Await(scope); err != nil {
			return err
		}
		if _, err = os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("symlink not unlinked")
		}
		actual, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if string(actual) != "retained-target" {
			return errors.New("symlink target modified")
		}
		removed, err = files.Unlink(scope, target)
		if err != nil {
			return err
		}
		if _, err = removed.Await(scope); err != nil {
			return err
		}
		if _, err = os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("ordinary file not unlinked")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
