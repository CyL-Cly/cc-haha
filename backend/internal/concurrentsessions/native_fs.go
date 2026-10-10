package concurrentsessions

import (
	"context"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// NativeFileSystem shares the caller's canonical Owner. mu/count/idle track only
// administrative physical-work receipts; they contain no session business state.
type NativeFileSystem struct {
	owner *cooperative.Owner
	mu    sync.Mutex
	count int
	idle  chan struct{}
}

func NewNativeFileSystem(owner *cooperative.Owner) (*NativeFileSystem, error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	idle := make(chan struct{})
	close(idle)
	return &NativeFileSystem{owner: owner, idle: idle}, nil
}

func (files *NativeFileSystem) check(scope *cooperative.Scope) error {
	if files == nil || !scope.BelongsTo(files.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func (files *NativeFileSystem) started() {
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.count == 0 {
		files.idle = make(chan struct{})
	}
	files.count++
}
func (files *NativeFileSystem) finished() {
	files.mu.Lock()
	defer files.mu.Unlock()
	files.count--
	if files.count == 0 {
		close(files.idle)
	}
}

// Join is off-owner administrative observation of the currently active batch.
// It must not be used as Source cleanup/forceExit policy or to join later jobs.
// Non-cancellable OS calls can outlive Owner.Close; callers must release their
// external IO and explicitly observe Join before disposing a test HOME.
func (files *NativeFileSystem) Join(ctx context.Context) error {
	if files == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	files.mu.Lock()
	idle := files.idle
	files.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// work is pure native IO over captured immutable inputs. The dispatch bridge
// only posts its immutable receipt; it never gives work a Scope or business
// getter. Resolve/Reject and every Then continuation run on the original Owner.
func startNative[T any](files *NativeFileSystem, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if err := files.check(scope); err != nil {
		return nil, err
	}
	future, _ := ownerasync.NewOwnerFuture[T](files.owner)
	files.started()
	go func() {
		defer files.finished()
		value, failure := work()
		receipt, err := files.owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var err error
			if failure != nil {
				err = future.Reject(next, failure)
			} else {
				err = future.Resolve(next, value)
			}
			if err != nil {
				_ = future.RejectAdministrative(err)
			}
			return nil
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			_ = future.RejectAdministrative(err)
		}
	}()
	return future, nil
}

func (files *NativeFileSystem) Mkdir(scope *cooperative.Scope, path string, mode fs.FileMode) (*ownerasync.OwnerFuture[struct{}], error) {
	return startNative(files, scope, func() (struct{}, error) { return struct{}{}, os.MkdirAll(path, mode) })
}
func (files *NativeFileSystem) Chmod(scope *cooperative.Scope, path string, mode fs.FileMode) (*ownerasync.OwnerFuture[struct{}], error) {
	return startNative(files, scope, func() (struct{}, error) { return struct{}{}, os.Chmod(path, mode) })
}
func (files *NativeFileSystem) WriteFile(scope *cooperative.Scope, path string, data []byte, mode fs.FileMode) (*ownerasync.OwnerFuture[struct{}], error) {
	if err := files.check(scope); err != nil {
		return nil, err
	}
	// Source's serialized registry string is immutable. No caller-owned byte
	// slice can race a physical worker or change a queued write's input.
	copied := append([]byte(nil), data...)
	return startNative(files, scope, func() (struct{}, error) { return struct{}{}, os.WriteFile(path, copied, mode) })
}
func (files *NativeFileSystem) ReadFile(scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[[]byte], error) {
	return startNative(files, scope, func() ([]byte, error) { return os.ReadFile(path) })
}
func (files *NativeFileSystem) ReadDir(scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[[]string], error) {
	return startNative(files, scope, func() ([]string, error) {
		directory, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		// os.ReadDir sorts names. Source consumes native readdir order instead.
		// One physical operation owns its descriptor through enumeration/close.
		names, err := directory.Readdirnames(-1)
		closed := directory.Close()
		if err != nil {
			return names, err
		}
		return names, closed
	})
}
func (files *NativeFileSystem) Unlink(scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[struct{}], error) {
	return startNative(files, scope, func() (struct{}, error) {
		// Unlike os.Remove, unlink never falls back to deleting a directory.
		if err := syscall.Unlink(path); err != nil {
			return struct{}{}, &os.PathError{Op: "unlink", Path: path, Err: err}
		}
		return struct{}{}, nil
	})
}
