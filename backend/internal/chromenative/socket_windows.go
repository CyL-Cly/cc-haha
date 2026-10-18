//go:build windows

package chromenative

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func prepareSocketDirectory(string) error { return nil }

type pipeAddress string

func (p pipeAddress) Network() string { return "pipe" }
func (p pipeAddress) String() string  { return string(p) }

type pipeListener struct {
	path    string
	mu      sync.Mutex
	closed  bool
	pending map[windows.Handle]bool
	initial windows.Handle
	workers sync.WaitGroup
}

func listenSocket(path string) (net.Listener, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, windows.PIPE_UNLIMITED_INSTANCES, 65536, 65536, 0, nil)
	if err != nil {
		return nil, err
	}
	return &pipeListener{path: path, pending: map[windows.Handle]bool{}, initial: handle}, nil
}
func (l *pipeListener) Addr() net.Addr { return pipeAddress(l.path) }
func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	name, err := windows.UTF16PtrFromString(l.path)
	if err != nil {
		l.mu.Unlock()
		return nil, err
	}
	handle := l.initial
	l.initial = 0
	if handle == 0 {
		handle, err = windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, windows.PIPE_UNLIMITED_INSTANCES, 65536, 65536, 0, nil)
		if err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	l.pending[handle] = true
	l.workers.Add(1)
	l.mu.Unlock()
	defer l.workers.Done()
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err == nil {
		defer windows.CloseHandle(event)
		over := windows.Overlapped{HEvent: event}
		l.mu.Lock()
		if l.closed {
			err = net.ErrClosed
		} else {
			err = windows.ConnectNamedPipe(handle, &over)
		}
		l.mu.Unlock()
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		if errors.Is(err, windows.ERROR_IO_PENDING) {
			var done uint32
			err = windows.GetOverlappedResult(handle, &over, &done, true)
		}
	}
	l.mu.Lock()
	delete(l.pending, handle)
	closed := l.closed
	l.mu.Unlock()
	if err != nil || closed {
		_ = windows.CloseHandle(handle)
		if closed {
			return nil, net.ErrClosed
		}
		return nil, err
	}
	return &pipeConnection{path: l.path, handle: handle}, nil
}
func (l *pipeListener) Close() error {
	l.mu.Lock()
	l.closed = true
	if l.initial != 0 {
		_ = windows.CloseHandle(l.initial)
		l.initial = 0
	}
	for handle := range l.pending {
		_ = windows.CancelIoEx(handle, nil)
	}
	l.mu.Unlock()
	l.workers.Wait()
	return nil
}

type pipeConnection struct {
	path    string
	handle  windows.Handle
	mu      sync.Mutex
	closed  bool
	workers sync.WaitGroup
	once    sync.Once
}

func (c *pipeConnection) transfer(data []byte, write bool) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	c.workers.Add(1)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		c.workers.Done()
		c.mu.Unlock()
		return 0, err
	}
	over := windows.Overlapped{HEvent: event}
	var count uint32
	// Start I/O under the close lock, so Close cannot miss the operation
	// between its closed check and CancelIoEx's cancellation snapshot.
	if write {
		err = windows.WriteFile(c.handle, data, &count, &over)
	} else {
		err = windows.ReadFile(c.handle, data, &count, &over)
	}
	c.mu.Unlock()
	defer c.workers.Done()
	defer windows.CloseHandle(event)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(c.handle, &over, &count, true)
	}
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		err = io.EOF
	}
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		err = net.ErrClosed
	}
	return int(count), err
}
func (c *pipeConnection) Read(data []byte) (int, error)  { return c.transfer(data, false) }
func (c *pipeConnection) Write(data []byte) (int, error) { return c.transfer(data, true) }
func (c *pipeConnection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		_ = windows.CancelIoEx(c.handle, nil)
		c.mu.Unlock()
		c.workers.Wait()
		_ = windows.CloseHandle(c.handle)
	})
	return nil
}
func (c *pipeConnection) LocalAddr() net.Addr              { return pipeAddress(c.path) }
func (c *pipeConnection) RemoteAddr() net.Addr             { return pipeAddress(c.path) }
func (c *pipeConnection) SetDeadline(time.Time) error      { return errors.ErrUnsupported }
func (c *pipeConnection) SetReadDeadline(time.Time) error  { return errors.ErrUnsupported }
func (c *pipeConnection) SetWriteDeadline(time.Time) error { return errors.ErrUnsupported }
