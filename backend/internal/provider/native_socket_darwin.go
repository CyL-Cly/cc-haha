//go:build darwin

package provider

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
)

// Only duplicated socket/wakeup descriptors cross into the readiness worker.
// All byte reads are nonblocking; no Go netpoll Read or bufio steals the prefix.
type nativeHTTP1Socket struct {
	descriptor int
	wakeRead   int
	wakeWrite  int
}

func newNativeHTTP1Socket(connection *net.TCPConn) (*nativeHTTP1Socket, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return nil, err
	}
	descriptor := -1
	var nativeErr error
	err = raw.Control(func(fd uintptr) { descriptor, nativeErr = syscall.Dup(int(fd)) })
	if err != nil {
		return nil, err
	}
	if nativeErr != nil {
		return nil, nativeErr
	}
	fds := []int{descriptor}
	success := false
	defer func() {
		if !success {
			for _, fd := range fds {
				_ = syscall.Close(fd)
			}
		}
	}()
	var wake [2]int
	if err = syscall.Pipe(wake[:]); err != nil {
		return nil, err
	}
	fds = append(fds, wake[:]...)
	for _, fd := range fds {
		syscall.CloseOnExec(fd)
		if fd >= len(syscall.FdSet{}.Bits)*32 {
			return nil, fmt.Errorf("%w: Darwin readiness descriptor exceeds select domain", ErrHTTPExchangeProfile)
		}
		if err = syscall.SetNonblock(fd, true); err != nil {
			return nil, err
		}
	}
	success = true
	return &nativeHTTP1Socket{descriptor, wake[0], wake[1]}, nil
}

func (socket *nativeHTTP1Socket) read(out []byte) (int, error) {
	for {
		count, err := syscall.Read(socket.descriptor, out)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if count == 0 {
			return 0, io.EOF
		}
		return count, nil
	}
}

func (socket *nativeHTTP1Socket) wake() { _, _ = syscall.Write(socket.wakeWrite, []byte{1}) }
func (socket *nativeHTTP1Socket) wait() error {
	for {
		var reads syscall.FdSet
		reads.Bits[socket.descriptor/32] |= int32(uint32(1) << uint(socket.descriptor%32))
		reads.Bits[socket.wakeRead/32] |= int32(uint32(1) << uint(socket.wakeRead%32))
		maximum := socket.descriptor
		if socket.wakeRead > maximum {
			maximum = socket.wakeRead
		}
		err := syscall.Select(maximum+1, &reads, nil, nil, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if reads.Bits[socket.wakeRead/32]&int32(uint32(1)<<uint(socket.wakeRead%32)) != 0 {
			var scratch [64]byte
			for {
				_, err = syscall.Read(socket.wakeRead, scratch[:])
				if err != nil {
					break
				}
			}
		}
		return nil
	}
}
func (socket *nativeHTTP1Socket) shutdown() {
	_ = syscall.Shutdown(socket.descriptor, syscall.SHUT_RDWR)
}
func (socket *nativeHTTP1Socket) close() {
	_ = syscall.Close(socket.descriptor)
	_ = syscall.Close(socket.wakeRead)
	_ = syscall.Close(socket.wakeWrite)
}
