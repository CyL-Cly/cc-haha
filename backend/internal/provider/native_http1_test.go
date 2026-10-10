//go:build darwin

package provider

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func nativePair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.TCPConn, 1)
	go func() { peer, _ := listener.AcceptTCP(); accepted <- peer }()
	connection, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	if peer == nil {
		t.Fatal("accept")
	}
	t.Cleanup(func() { _ = connection.Close(); _ = peer.Close() })
	return connection, peer
}
func nativeOperation(t *testing.T, limit int) (*NativeHTTP1Operation, *net.TCPConn) {
	t.Helper()
	connection, peer := nativePair(t)
	operation, err := NewNativeHTTP1Operation(connection, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = operation.Close() })
	return operation, peer
}
func nativeWrite(t *testing.T, peer *net.TCPConn, raw string) {
	t.Helper()
	if _, err := io.WriteString(peer, raw); err != nil {
		t.Fatal(err)
	}
}

// This is an actual kernel-readiness barrier, not a sleep or worker completion.
func nativeReady(t *testing.T, operation *NativeHTTP1Operation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := operation.WaitReadable(ctx); err != nil {
		t.Fatal(err)
	}
}
func nativeAdopt(t *testing.T, operation *NativeHTTP1Operation) *http.Response {
	t.Helper()
	request, _ := http.NewRequest("POST", "http://fixture.invalid/v1/messages", nil)
	response, err := operation.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestNativePollFreshHeadersBeforeWorkerScheduled(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nbody")
	nativeReady(t, operation)
	release := make(chan struct{})
	returned := make(chan error, 1)
	go func() { <-release; returned <- operation.WaitReadable(context.Background()) }()
	checkpoint := operation.Checkpoint()
	if checkpoint.State != NativeHTTP1HeaderComplete || checkpoint.Received != 42 || string(checkpoint.Header) != "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n" {
		t.Fatalf("fresh facts %+v", checkpoint)
	}
	close(release)
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(nativeAdopt(t, operation).Body)
	if err != nil || string(body) != "body" {
		t.Fatalf("same prefix %q %v", body, err)
	}
}

func TestNativePollIncrementalPartialAndSnapshotIsolation(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	if actual := operation.Checkpoint(); actual.State != NativeHTTP1NoReadable || actual.Received != 0 || actual.Err != nil {
		t.Fatal(actual)
	}
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Len")
	nativeReady(t, operation)
	if actual := operation.Checkpoint(); actual.State != NativeHTTP1Partial || actual.Received == 0 {
		t.Fatal(actual)
	}
	if actual := operation.Checkpoint(); actual.State != NativeHTTP1Partial || actual.Received != 0 {
		t.Fatal(actual)
	}
	nativeWrite(t, peer, "gth: 3\r\n\r\nxyz")
	nativeReady(t, operation)
	actual := operation.Checkpoint()
	if actual.State != NativeHTTP1HeaderComplete {
		t.Fatal(actual)
	}
	actual.Header[0] = 'X'
	repeated := operation.Checkpoint()
	if repeated.State != NativeHTTP1HeaderComplete || repeated.Received != 0 || repeated.Header[0] != 'H' {
		t.Fatal(repeated)
	}
	response := nativeAdopt(t, operation)
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "xyz" {
		t.Fatalf("body %q %v", body, err)
	}
	if !errors.Is(operation.Checkpoint().Err, ErrHTTPExchangeProfile) {
		t.Fatal("post-adoption poll must fail explicitly")
	}
	if _, err = operation.AdoptResponse(nil); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
}

func TestNativePollSingleRequestAndBodyAcrossAdoption(t *testing.T) {
	connection, peer := nativePair(t)
	request, _ := http.NewRequest("POST", "http://fixture.invalid/v1/messages?beta=true", strings.NewReader("signed-once"))
	request.Header.Set("X-Original", "fixture")
	sent := make(chan error, 1)
	go func() { sent <- request.Write(connection) }()
	original, err := http.ReadRequest(bufio.NewReader(peer))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(original.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-sent; err != nil || original.RequestURI != "/v1/messages?beta=true" || original.Header.Get("X-Original") != "fixture" || string(body) != "signed-once" {
		t.Fatalf("single request %v %v %q", original, err, body)
	}
	operation, err := NewNativeHTTP1Operation(connection, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer operation.Close()
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\none")
	nativeReady(t, operation)
	if operation.Checkpoint().State != NativeHTTP1HeaderComplete {
		t.Fatal("complete")
	}
	response := nativeAdopt(t, operation)
	nativeWrite(t, peer, "twoEND!")
	actual, err := io.ReadAll(response.Body)
	if err != nil || string(actual) != "onetwoEND!" {
		t.Fatalf("data %q %v", actual, err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 1)
	n, err := peer.Read(raw)
	if n != 0 || err != io.EOF {
		t.Fatalf("physical close/second request %d %v", n, err)
	}
}

func TestNativePollFramingAndNativeEOF(t *testing.T) {
	for _, test := range []struct{ name, wire, want string }{
		{"chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nab\r\n1\r\nc\r\n0\r\nX-Trailer: value\r\n\r\n", "abc"},
		{"connection-eof", "HTTP/1.1 200 OK\r\n\r\nstream-final", "stream-final"},
		{"empty", "HTTP/1.1 204 No Content\r\n\r\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation, peer := nativeOperation(t, 1024)
			nativeWrite(t, peer, test.wire)
			_ = peer.CloseWrite()
			nativeReady(t, operation)
			if actual := operation.Checkpoint(); actual.State != NativeHTTP1HeaderComplete {
				t.Fatal(actual)
			}
			response := nativeAdopt(t, operation)
			raw, err := io.ReadAll(response.Body)
			if err != nil || string(raw) != test.want {
				t.Fatalf("%q %v", raw, err)
			}
			if test.name == "chunked" && response.Trailer.Get("X-Trailer") != "value" {
				t.Fatal(response.Trailer)
			}
		})
	}
}

func TestNativePollIncompleteEOFFaultAndHeaderBound(t *testing.T) {
	for _, test := range []struct {
		name, wire string
		limit      int
		want       error
	}{
		{"empty-eof", "", 1024, io.EOF}, {"partial-eof", "HTTP/1.1", 1024, io.EOF},
		{"partial-bound", "long missing terminator", 4, ErrHTTPExchangeProfile},
		{"complete-over-bound", "HTTP/1.1 200 OK\r\n\r\n", 4, ErrHTTPExchangeProfile},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation, peer := nativeOperation(t, test.limit)
			nativeWrite(t, peer, test.wire)
			_ = peer.CloseWrite()
			nativeReady(t, operation)
			actual := operation.Checkpoint()
			if actual.State != NativeHTTP1Fault || !errors.Is(actual.Err, test.want) {
				t.Fatal(actual)
			}
			if _, err := operation.AdoptResponse(nil); !errors.Is(err, ErrHTTPExchangeProfile) {
				t.Fatal(err)
			}
		})
	}
}

func TestNativePollInvalidTransportAndPhase(t *testing.T) {
	if _, err := NewNativeHTTP1Operation(nil, 1024); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	var nilTCP *net.TCPConn
	if _, err := NewNativeHTTP1Operation(nilTCP, 1024); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	connection, _ := nativePair(t)
	if _, err := NewNativeHTTP1Operation(tls.Client(connection, &tls.Config{}), 1024); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 3, 1024*1024 + 1} {
		if _, err := NewNativeHTTP1Operation(connection, limit); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
	}
	operation, peer := nativeOperation(t, 1024)
	if _, err := operation.Read(make([]byte, 1)); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	if n, err := operation.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty %d %v", n, err)
	}
	if _, err := operation.AdoptResponse(nil); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	nativeWrite(t, peer, "NOT HTTP\r\n\r\n")
	nativeReady(t, operation)
	operation.Checkpoint()
	if _, err := operation.AdoptResponse(nil); err == nil {
		t.Fatal("invalid framing accepted")
	}
	if actual := operation.Checkpoint(); !errors.Is(actual.Err, net.ErrClosed) {
		t.Fatal(actual)
	}
	if err := operation.WaitReadable(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := operation.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := operation.AdoptResponse(nil); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if err := operation.Close(); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if _, err := NewNativeHTTP1Operation(connection, 1024); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}

func TestNativePollWaitCancellationDoesNotOwnBodyLifetime(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := operation.WaitReadable(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- operation.WaitReadable(ctx) }()
	nativeWaitRegistered(t, operation)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nlive")
	nativeReady(t, operation)
	if operation.Checkpoint().State != NativeHTTP1HeaderComplete {
		t.Fatal("head")
	}
	body, err := io.ReadAll(nativeAdopt(t, operation).Body)
	if err != nil || string(body) != "live" {
		t.Fatalf("independent body %q %v", body, err)
	}
}

func TestNativePollCloseWakesAndJoinsPhysicalWait(t *testing.T) {
	for turn := 0; turn < 32; turn++ {
		operation, peer := nativeOperation(t, 1024)
		done := make(chan error, 1)
		go func() { done <- operation.WaitReadable(context.Background()) }()
		nativeWaitRegistered(t, operation)
		if err := operation.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		var raw [1]byte
		n, err := peer.Read(raw[:])
		if n != 0 || err != io.EOF {
			t.Fatalf("peer close %d %v", n, err)
		}
	}
}

func TestNativePollResetAndClosedDescriptorArePhysicalFaults(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		operation, peer := nativeOperation(t, 1024)
		if err := peer.SetLinger(0); err != nil {
			t.Fatal(err)
		}
		_ = peer.Close()
		nativeReady(t, operation)
		actual := operation.Checkpoint()
		if actual.State != NativeHTTP1Fault || !errors.Is(actual.Err, syscall.ECONNRESET) {
			t.Fatal(actual)
		}
	})
	t.Run("descriptor-fault", func(t *testing.T) {
		operation, _ := nativeOperation(t, 1024)
		_ = syscall.Close(operation.socket.descriptor)
		if err := operation.WaitReadable(context.Background()); !errors.Is(err, syscall.EBADF) {
			t.Fatal(err)
		}
		actual := operation.Checkpoint()
		if actual.State != NativeHTTP1Fault || !errors.Is(actual.Err, syscall.EBADF) {
			t.Fatal(actual)
		}
	})
}

func TestNativePollConcurrentDemandIsExplicitCapability(t *testing.T) {
	operation, _ := nativeOperation(t, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- operation.WaitReadable(ctx) }()
	nativeWaitRegistered(t, operation)
	if err := operation.WaitReadable(context.Background()); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativePollReadWaitAndCloseResource(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\n\r\n")
	nativeReady(t, operation)
	operation.Checkpoint()
	response := nativeAdopt(t, operation)
	done := make(chan error, 1)
	go func() { var out [1]byte; _, err := response.Body.Read(out[:]); done <- err }()
	nativeWaitRegistered(t, operation)
	_ = operation.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}

// This observes a registered resource wait under its actual mutex. No delay,
// Gosched, timer padding or worker-result guess makes the condition true.
func nativeWaitRegistered(t *testing.T, operation *NativeHTTP1Operation) {
	t.Helper()
	watchdog := time.NewTimer(2 * time.Second)
	defer watchdog.Stop()
	for {
		operation.mu.Lock()
		waiting := operation.waiting
		operation.mu.Unlock()
		if waiting {
			return
		}
		select {
		case <-watchdog.C:
			t.Fatal("real resource wait not registered")
		default:
		}
	}
}

func TestNativePollBodyReadAfterActualPendingWait(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\n\r\n")
	nativeReady(t, operation)
	operation.Checkpoint()
	response := nativeAdopt(t, operation)
	done := make(chan struct {
		raw []byte
		err error
	}, 1)
	go func() {
		raw, err := io.ReadAll(response.Body)
		done <- struct {
			raw []byte
			err error
		}{raw, err}
	}()
	nativeWaitRegistered(t, operation)
	nativeWrite(t, peer, "later-native-bytes")
	_ = peer.CloseWrite()
	result := <-done
	if result.err != nil || string(result.raw) != "later-native-bytes" {
		t.Fatalf("late body %q %v", result.raw, result.err)
	}
}

func TestNativePollResourceLimitsChild(t *testing.T) {
	for _, profile := range []string{"dup-exhausted", "pipe-exhausted", "fd-out-of-domain"} {
		t.Run(profile, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestNativePollResourceFailureChild$", "-test.v")
			command.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=/opt/homebrew/bin:/usr/bin:/bin", "GOMAXPROCS=2", "CC_HAHA_NATIVE_POLL_CHILD=" + profile}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("native child %v %s", err, output)
			}
		})
	}
}
func TestNativePollResourceFailureChild(t *testing.T) {
	profile := os.Getenv("CC_HAHA_NATIVE_POLL_CHILD")
	if profile == "" {
		t.Skip("private subprocess only")
	}
	connection, _ := nativePair(t)
	raw, err := connection.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var original int
	if err = raw.Control(func(fd uintptr) { original = int(fd) }); err != nil {
		t.Fatal(err)
	}
	var previous syscall.Rlimit
	if err = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &previous); err != nil {
		t.Fatal(err)
	}
	limit := uint64(64)
	if profile == "fd-out-of-domain" {
		limit = 2048
	}
	if previous.Max < limit {
		t.Fatalf("private native limit %v", previous)
	}
	if err = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: limit, Max: previous.Max}); err != nil {
		t.Fatal(err)
	}
	defer syscall.Setrlimit(syscall.RLIMIT_NOFILE, &previous)
	var descriptors []int
	defer func() {
		for _, fd := range descriptors {
			_ = syscall.Close(fd)
		}
	}()
	for {
		fd, e := syscall.Dup(original)
		if e != nil {
			if e != syscall.EMFILE {
				t.Fatal(e)
			}
			break
		}
		descriptors = append(descriptors, fd)
		if profile == "fd-out-of-domain" && fd >= 1024 {
			break
		}
	}
	if profile == "pipe-exhausted" {
		_ = syscall.Close(descriptors[len(descriptors)-1])
		descriptors = descriptors[:len(descriptors)-1]
	}
	operation, err := NewNativeHTTP1Operation(connection, 1024)
	if operation != nil {
		operation.Close()
		t.Fatal("unsupported resource unexpectedly acquired")
	}
	if profile == "fd-out-of-domain" {
		if !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
	} else if !errors.Is(err, syscall.EMFILE) {
		t.Fatal(err)
	}
}

func TestNativePollUninitializedTCPResource(t *testing.T) {
	if _, err := NewNativeHTTP1Operation(&net.TCPConn{}, 1024); !errors.Is(err, syscall.EINVAL) {
		t.Fatal(err)
	}
}

func TestNativePollBodyReaderRetainsOriginalEOF(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\n\r\nfinal")
	_ = peer.CloseWrite()
	nativeReady(t, operation)
	operation.Checkpoint()
	response := nativeAdopt(t, operation)
	raw, err := io.ReadAll(response.Body)
	if err != nil || string(raw) != "final" {
		t.Fatalf("%q %v", raw, err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		var out [1]byte
		n, err := operation.Read(out[:])
		if n != 0 || err != io.EOF {
			t.Fatalf("stable original EOF %d %v", n, err)
		}
	}
}
