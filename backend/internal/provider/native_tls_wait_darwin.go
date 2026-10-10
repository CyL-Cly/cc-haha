//go:build darwin

package provider

import (
	"errors"
	"fmt"
	"syscall"
)

type nativeTLSWritableWait struct{ descriptor, wakeRead, wakeWrite int }

func newNativeTLSWriteReadiness(socket *nativeHTTP1Socket) (nativeTLSWriteReadiness, error) {
	var wake [2]int
	if err := syscall.Pipe(wake[:]); err != nil {
		return nil, err
	}
	for _, fd := range wake {
		syscall.CloseOnExec(fd)
		if fd >= len(syscall.FdSet{}.Bits)*32 {
			_ = syscall.Close(wake[0])
			_ = syscall.Close(wake[1])
			return nil, fmt.Errorf("%w: Darwin writable descriptor exceeds select domain", ErrHTTPExchangeProfile)
		}
		if err := syscall.SetNonblock(fd, true); err != nil {
			_ = syscall.Close(wake[0])
			_ = syscall.Close(wake[1])
			return nil, err
		}
	}
	return &nativeTLSWritableWait{socket.descriptor, wake[0], wake[1]}, nil
}
func (w *nativeTLSWritableWait) wake() { _, _ = syscall.Write(w.wakeWrite, []byte{1}) }
func (w *nativeTLSWritableWait) close() {
	_ = syscall.Close(w.wakeRead)
	_ = syscall.Close(w.wakeWrite)
}
func (w *nativeTLSWritableWait) wait() error {
	for {
		var reads, writes syscall.FdSet
		reads.Bits[w.wakeRead/32] |= int32(uint32(1) << uint(w.wakeRead%32))
		writes.Bits[w.descriptor/32] |= int32(uint32(1) << uint(w.descriptor%32))
		err := syscall.Select(max(w.descriptor, w.wakeRead)+1, &reads, &writes, nil, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if reads.Bits[w.wakeRead/32]&int32(uint32(1)<<uint(w.wakeRead%32)) != 0 {
			var scratch [64]byte
			for {
				_, err = syscall.Read(w.wakeRead, scratch[:])
				if err != nil {
					break
				}
			}
		}
		return nil
	}
}
func nativeTLSWriteSome(socket *nativeHTTP1Socket, out []byte) (int, bool, error) {
	for {
		count, err := syscall.Write(socket.descriptor, out)
		if count < 0 {
			count = 0
		}
		if errors.Is(err, syscall.EINTR) {
			if count > 0 {
				return count, false, nil
			}
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			return count, true, nil
		}
		if err != nil {
			return count, false, err
		}
		return count, count < len(out), nil
	}
}
