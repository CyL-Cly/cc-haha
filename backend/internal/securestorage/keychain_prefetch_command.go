package securestorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// execFile's default maxBuffer is per stream. Its pre-existing maxBuffer
// RangeError has no killed field; timeout errors acquire that field only when
// the actual child did not exit successfully. See pinned child_process.ts.
const keychainPrefetchMaxBuffer = 1024 * 1024

var errKeychainPrefetchMaxBuffer = errors.New("Keychain prefetch maxBuffer exceeded")

type keychainPrefetchKilledError struct{ cause error }

func (e *keychainPrefetchKilledError) Error() string { return e.cause.Error() }
func (e *keychainPrefetchKilledError) Unwrap() error { return e.cause }

type keychainPrefetchOutput struct {
	mu           sync.Mutex
	data         bytes.Buffer
	carry        []byte
	decodedBytes int
	finished     bool
	overflow     bool
	kill         func() error
}

// Pinned StringDecoder deliberately detects incomplete suffixes by byte shape,
// including C0/C1, surrogate prefixes and F5..F7. FullRune's earlier rejection
// would change when replacement bytes enter maxBuffer accounting.
func keychainPrefetchUTF8Shape(value byte) int {
	switch {
	case value < 0x80:
		return 0
	case value&0xe0 == 0xc0:
		return 2
	case value&0xf0 == 0xe0:
		return 3
	case value&0xf8 == 0xf0:
		return 4
	case value&0xc0 == 0x80:
		return -1
	default:
		return -2
	}
}

func keychainPrefetchReadyUTF8(data []byte) int {
	for index := len(data) - 1; index >= 0 && len(data)-index <= 3; index-- {
		shape := keychainPrefetchUTF8Shape(data[index])
		if shape >= 0 {
			if shape > len(data)-index {
				return index
			}
			return len(data)
		}
		if shape == -2 {
			return len(data)
		}
	}
	return len(data)
}

// One Write returns one StringDecoder chunk. A completed/failed saved prefix
// and the remaining text must be combined before execFile's byte-length and
// UTF-16 slice calculation, rather than counted as two separate data events.
func (output *keychainPrefetchOutput) decodeChunk(data []byte) []byte {
	var decoded []byte
	if len(output.carry) != 0 {
		needed := keychainPrefetchUTF8Shape(output.carry[0]) - len(output.carry)
		check := needed
		if check > len(data) {
			check = len(data)
		}
		accepted := 0
		for accepted < check && data[accepted]&0xc0 == 0x80 {
			accepted++
		}
		if accepted == check && len(data) < needed {
			output.carry = append(output.carry, data...)
			return nil
		}
		prefix := append([]byte(nil), output.carry...)
		prefix = append(prefix, data[:accepted]...)
		decoded = protocol.DecodeJavaScriptUTF8(prefix)
		data = data[accepted:]
		output.carry = output.carry[:0]
	}
	ready := keychainPrefetchReadyUTF8(data)
	output.carry = append(output.carry[:0], data[ready:]...)
	return append(decoded, protocol.DecodeJavaScriptUTF8(data[:ready])...)
}

func (output *keychainPrefetchOutput) appendDecoded(decoded []byte) bool {
	if output.overflow {
		return false
	}
	remaining := keychainPrefetchMaxBuffer - output.decodedBytes
	output.decodedBytes += len(decoded)
	if len(decoded) <= remaining {
		_, _ = output.data.Write(decoded)
		return false
	}
	// Pinned execFile uses a byte remainder as String.slice's UTF-16 count.
	// Consequently its error stdout can itself exceed maxBuffer after UTF-8
	// replacement or a multibyte chunk; the error, not a raw-byte cap, rejects
	// the credential read. Its private byte result encodes any sliced lone
	// surrogate as replacement, matching the native string/byte boundary.
	units := utf16.Encode([]rune(string(decoded)))
	if remaining < len(units) {
		units = units[:remaining]
	}
	_, _ = output.data.WriteString(string(utf16.Decode(units)))
	output.overflow = true
	return true
}

func (output *keychainPrefetchOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	if output.finished {
		output.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if output.overflow {
		output.mu.Unlock()
		return len(data), nil
	}
	kill := output.appendDecoded(output.decodeChunk(data))
	output.mu.Unlock()
	if kill {
		_ = output.kill()
	}
	return len(data), nil
}

// Only a real EOF ends StringDecoder and emits replacement for a pending
// prefix. Destroy/cancellation of a pipe does not manufacture an EOF flush.
func (output *keychainPrefetchOutput) finish() {
	output.mu.Lock()
	if output.finished {
		output.mu.Unlock()
		return
	}
	output.finished = true
	kill := output.appendDecoded(protocol.DecodeJavaScriptUTF8(output.carry))
	output.carry = nil
	output.mu.Unlock()
	if kill {
		_ = output.kill()
	}
}

// KeychainPrefetchProcess is a real started process, never a completed byte
// record. External fixture adapters can substitute the executable primitive;
// only the Store's prefetch producer can consume its private Wait result.
// Pipe readers are this process's actual descriptors. Destroying them on kill
// mirrors execFile and does not claim to terminate a descendant process tree.
type KeychainPrefetchProcess struct {
	command        *exec.Cmd
	ctx            context.Context
	done           chan struct{}
	childExited    chan struct{}
	stdout, stderr *keychainPrefetchOutput
	readers        [2]*os.File
	readDone       [2]chan struct{}
	readErrors     [2]error
	stopCancel     func() bool
	killed         atomic.Bool
	killOnce       sync.Once
	killError      error
	once           sync.Once
	data           []byte
	failure        error
}

func StartKeychainPrefetchProcess(ctx context.Context, request *exec.Cmd) (*KeychainPrefetchProcess, error) {
	if ctx == nil || request == nil || request.Process != nil || request.Stdout != nil || request.Stderr != nil || request.Env == nil || !filepath.IsAbs(request.Dir) || len(request.Args) == 0 || len(request.ExtraFiles) != 0 {
		return nil, ErrKeychainPrefetchState
	}
	// Bind this actual child to the arm's lifetime, rather than a hidden
	// context inside a caller-supplied Cmd description.
	command := exec.CommandContext(ctx, filepath.Join(request.Dir, "security-prefetch-unresolved"))
	command.Path, command.Args, command.Err = request.Path, append([]string(nil), request.Args...), request.Err
	command.Dir, command.Env, command.Stdin = request.Dir, append([]string{}, request.Env...), request.Stdin
	command.SysProcAttr = request.SysProcAttr
	process := &KeychainPrefetchProcess{command: command, ctx: ctx, done: make(chan struct{}), childExited: make(chan struct{})}
	var writers [2]*os.File
	for index := range 2 {
		reader, writer, err := os.Pipe()
		if err != nil {
			for earlier := range index {
				_ = process.readers[earlier].Close()
				_ = writers[earlier].Close()
			}
			return nil, err
		}
		process.readers[index], writers[index], process.readDone[index] = reader, writer, make(chan struct{})
	}
	process.stdout, process.stderr = &keychainPrefetchOutput{kill: process.terminate}, &keychainPrefetchOutput{kill: process.terminate}
	command.Stdout, command.Stderr, command.Cancel = writers[0], writers[1], process.terminate
	if err := command.Start(); err != nil {
		for index := range 2 {
			_ = process.readers[index].Close()
			_ = writers[index].Close()
		}
		return nil, err
	}
	for _, writer := range writers {
		_ = writer.Close()
	}
	// Cmd's watcher ends when the direct child exits. execFile's own timeout
	// remains active until the stdio close callbacks have also completed.
	process.stopCancel = context.AfterFunc(ctx, func() { _ = process.terminate() })
	for index, output := range []*keychainPrefetchOutput{process.stdout, process.stderr} {
		go func(index int, output *keychainPrefetchOutput) {
			defer close(process.readDone[index])
			defer process.readers[index].Close()
			_, process.readErrors[index] = io.Copy(output, process.readers[index])
			if process.readErrors[index] == nil {
				output.finish()
			}
		}(index, output)
	}
	return process, nil
}

func (process *KeychainPrefetchProcess) terminate() error {
	if process == nil || process.PID() <= 0 {
		return ErrKeychainPrefetchState
	}
	process.killOnce.Do(func() {
		// Pinned execFile kill() destroys both streams, then sets killed=true
		// and signals only its direct child, even when that child already exited.
		for _, reader := range process.readers {
			_ = reader.Close()
		}
		process.killed.Store(true)
		process.killError = terminateKeychainPrefetchChild(process.command.Process)
	})
	return process.killError
}

func (process *KeychainPrefetchProcess) PID() int {
	if process == nil || process.command == nil || process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

// Done closes only after actual Cmd.Wait and the two stream callbacks, never
// at cancellation or launch. It exposes no credential value or settlement port.
func (process *KeychainPrefetchProcess) Done() <-chan struct{} {
	if process == nil {
		return nil
	}
	return process.done
}

func (process *KeychainPrefetchProcess) wait() ([]byte, error) {
	if process == nil || process.PID() <= 0 {
		return nil, ErrKeychainPrefetchState
	}
	process.once.Do(func() {
		defer close(process.done)
		failure := process.command.Wait()
		// Go's context watcher may return ctx.Err even for a real code-0
		// exit. execFile exitHandler's success branch precedes killed handling.
		if state := process.command.ProcessState; state != nil && state.Success() {
			failure = nil
		}
		close(process.childExited)
		for _, done := range process.readDone {
			<-done
		}
		if !process.stopCancel() && context.Cause(process.ctx) != nil {
			_ = process.terminate()
		}
		if process.stdout.overflow || process.stderr.overflow {
			// Original execFile retains its preconstructed maxBuffer error;
			// it does not add killed to that error in exitHandler.
			failure = errKeychainPrefetchMaxBuffer
		} else if process.killed.Load() && failure != nil {
			failure = &keychainPrefetchKilledError{cause: failure}
		} else if failure == nil && !process.killed.Load() {
			for _, err := range process.readErrors {
				if err != nil {
					failure = err
					break
				}
			}
		}
		process.data, process.failure = append([]byte(nil), process.stdout.data.Bytes()...), failure
	})
	return append([]byte(nil), process.data...), process.failure
}

func runKeychainPrefetchCommand(ctx context.Context, command *exec.Cmd) ([]byte, error) {
	process, err := StartKeychainPrefetchProcess(ctx, command)
	if err != nil {
		return nil, err
	}
	return process.wait()
}
