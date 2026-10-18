//go:build darwin

package clibootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// nativePromptReadFile is only the physical read. Its caller checks the native
// path capability before dispatch and parses the returned bytes on the Owner.
// The context is the shared producer's lifetime, never an individual waiter.
func nativePromptReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	// A blocking FIFO open cannot be interrupted by closing a file that has
	// not been returned yet. Open it nonblocking, then wait for genuine kernel
	// readability; an unconnected FIFO does not report EOF to this waiter.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		callbackDone := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			defer close(callbackDone)
			_ = file.Close()
		})
		defer func() {
			if !stop() {
				<-callbackDone
			}
		}()
		data, err := io.ReadAll(file)
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return data, err
	}
	queue, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(queue)
	defer unix.Close(queue)
	var wake [2]int
	if err := unix.Pipe(wake[:]); err != nil {
		return nil, err
	}
	defer unix.Close(wake[0])
	defer unix.Close(wake[1])
	for _, descriptor := range wake {
		unix.CloseOnExec(descriptor)
		if err := unix.SetNonblock(descriptor, true); err != nil {
			return nil, err
		}
	}
	changes := make([]unix.Kevent_t, 2)
	unix.SetKevent(&changes[0], fd, unix.EVFILT_READ, unix.EV_ADD|unix.EV_ENABLE)
	unix.SetKevent(&changes[1], wake[0], unix.EVFILT_READ, unix.EV_ADD|unix.EV_ENABLE)
	if _, err := unix.Kevent(queue, changes, nil, nil); err != nil {
		return nil, err
	}
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(callbackDone)
		_, _ = unix.Write(wake[1], []byte{1})
	})
	defer func() {
		// Join a started cancellation callback before closing its descriptors;
		// otherwise a reused fd could receive the late wake byte.
		if !stop() {
			<-callbackDone
		}
	}()
	var data []byte
	var events [2]unix.Kevent_t
	buffer := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		count, err := unix.Kevent(queue, nil, events[:], nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, event := range events[:count] {
			if int(event.Ident) == wake[0] {
				continue
			}
			if event.Flags&unix.EV_ERROR != 0 {
				return nil, &os.PathError{Op: "read", Path: path, Err: syscall.Errno(event.Data)}
			}
			for {
				if ctx.Err() != nil {
					return nil, context.Cause(ctx)
				}
				n, err := unix.Read(fd, buffer)
				if n > 0 {
					data = append(data, buffer[:n]...)
				}
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.EAGAIN) {
					break
				}
				if err != nil {
					return nil, &os.PathError{Op: "read", Path: path, Err: err}
				}
				if n == 0 {
					return data, nil
				}
			}
		}
	}
}
