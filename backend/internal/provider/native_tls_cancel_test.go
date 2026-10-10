//go:build darwin

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestNativeTLSActualBlockedWriterCancelAndCloseJoinAllWaiters(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel-first=", cancelFirst), func(t *testing.T) {
			serverConfig, trust := tlsProbeConfiguration(t)
			listener := tlsProbeListener(t)
			peerDone := make(chan error, 1)
			peerRelease := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(peerRelease) }) }
			defer release()
			go func() {
				peer, err := listener.Accept()
				if err != nil {
					peerDone <- err
					return
				}
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
				secured := tls.Server(peer, serverConfig)
				if err = secured.Handshake(); err == nil {
					// No application read releases write pressure. The raw copy only
					// starts after the client has been closed below.
					<-peerRelease
					_, err = io.Copy(io.Discard, peer)
				}
				peerDone <- err
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resource, err := newNativeTLSHTTP1Reservation(ctx, listener.Addr().String(), bytes.Repeat([]byte("private original TLS bytes"), 180000), 128, trust)
			if err != nil {
				t.Fatal(err)
			}
			defer resource.Close()
			deadline := time.After(5 * time.Second)
			select {
			case <-resource.available:
			case <-deadline:
				t.Fatal("TLS operation publication")
			}
			resource.mu.Lock()
			operation, failure := resource.operation, resource.failure
			resource.mu.Unlock()
			if operation == nil || failure != nil {
				t.Fatal("actual TLS handshake", failure)
			}
			writer := operation.recordReader.(*nativeTLSHTTP1Stream).wrapped.writer
			for {
				writer.mu.Lock()
				pending, changed := writer.pending, writer.changed
				writer.mu.Unlock()
				if pending > 1024*1024 {
					break
				}
				select {
				case <-changed:
				case <-deadline:
					t.Fatal("physical write pressure missing", pending)
				}
			}
			readDone := make(chan error, 1)
			go func() { readDone <- resource.WaitReadable(context.Background()) }()
			if cancelFirst {
				cancel()
				select {
				case <-writer.done:
				case <-deadline:
					t.Fatal("cancel did not wake/join writable descriptor worker")
				}
				select {
				case <-writer.callbackDone:
				case <-deadline:
					t.Fatal("cancel callback did not publish original fault")
				}
				if _, _, err = resource.Checkpoint(); !errors.Is(err, context.Canceled) {
					t.Fatal("owner did not observe original cancellation", err)
				}
			}
			var closes sync.WaitGroup
			closeErrors := make(chan error, 8)
			for range 8 {
				closes.Add(1)
				go func() { defer closes.Done(); closeErrors <- resource.Close() }()
			}
			joined := make(chan struct{})
			go func() { closes.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-deadline:
				t.Fatal("concurrent Close did not join actual transport")
			}
			close(closeErrors)
			for err = range closeErrors {
				if err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err = <-readDone:
				if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
					t.Fatal("readiness waiter", err)
				}
			case <-deadline:
				t.Fatal("Close left actual readable waiter")
			}
			select {
			case <-resource.done:
			default:
				t.Fatal("Close returned before initial TLS producer joined")
			}
			select {
			case <-writer.done:
			default:
				t.Fatal("Close returned before encrypted record worker joined")
			}
			waiter := writer.waiter.(*nativeTLSWritableWait)
			for _, descriptor := range []int{operation.socket.descriptor, operation.socket.wakeRead, operation.socket.wakeWrite, waiter.wakeRead, waiter.wakeWrite} {
				if _, err = syscall.Read(descriptor, make([]byte, 1)); !errors.Is(err, syscall.EBADF) {
					t.Fatal("owned descriptor still open after Close", descriptor, err)
				}
			}
			release()
			select {
			case err = <-peerDone:
				if err != nil {
					t.Fatal("physical peer close", err)
				}
			case <-deadline:
				t.Fatal("actual TLS peer left behind")
			}
		})
	}
}
