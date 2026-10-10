package filehistorycopy

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"io"
	"os"
)

// OSFiles does only the native operation. No settings, snapshot, cache or
// diagnostic policy is evaluated by an I/O worker.
type OSFiles struct{}

func (OSFiles) MkdirAll(path string) error { return os.MkdirAll(path, 0777) }
func (OSFiles) Link(old, new string) error { return os.Link(old, new) }
func (OSFiles) CopyFile(old, new string) (err error) {
	input, e := os.Open(old)
	if e != nil {
		return e
	}
	defer input.Close()
	info, e := input.Stat()
	if e != nil {
		return e
	}
	if info.IsDir() {
		return &os.PathError{Op: "copyfile", Path: old, Err: os.ErrInvalid}
	}
	output, e := os.OpenFile(new, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if e != nil {
		return e
	}
	defer func() {
		if e := output.Close(); err == nil {
			err = e
		}
	}()
	_, err = io.Copy(output, input)
	return
}

// dispatch gives the worker only an immutable syscall closure. The completion
// produces an actual Owner reaction before the shared native reference closes.
func (s *Service) dispatch(scope *cooperative.Scope, work func() error) (*ownerasync.OwnerFuture[struct{}], error) {
	ref, e := s.options.Binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[struct{}](s.options.Owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	owner := s.options.Owner
	go func() {
		failure := work()
		receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = future.Reject(next, failure)
			} else {
				e = future.Resolve(next, struct{}{})
			}
			if e != nil {
				return e
			}
			// This is behind the native promise's admitted reactions, not an await of
			// detached Record. A subsequent dispatched I/O acquires its own reference.
			release, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e == nil {
				ref.ReclaimRejectedReceipt(release)
			}
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}
