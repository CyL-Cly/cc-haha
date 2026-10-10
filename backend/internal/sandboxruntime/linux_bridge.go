package sandboxruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type LinuxBridgeOptions struct {
	TemporaryDirectory, Socat string
	Environment               []string
	Random                    io.Reader
}
type LinuxBridge struct {
	HTTPSocketPath, SOCKSSocketPath string
	processes                       []*exec.Cmd
	waited                          []chan struct{}
	closeOnce                       sync.Once
	closeDone                       chan struct{}
}

func LinuxBridgeArguments(socket string, port float64) []string {
	return []string{"UNIX-LISTEN:" + socket + ",fork,reuseaddr", "TCP:localhost:" + numberString(port) + ",keepalive,keepidle=10,keepintvl=5,keepcnt=3"}
}

// NewLinuxBridge retains the original five native ready observations and
// backoff sequence. The bridge is a native generic socat process, not an Agent
// or a language-worker fallback. Its lifetime is owned by this resource.
func NewLinuxBridge(ctx context.Context, http, socks float64, o LinuxBridgeOptions) (*LinuxBridge, error) {
	if !filepath.IsAbs(o.TemporaryDirectory) || o.Socat == "" {
		return nil, errors.New("Linux bridge requires actual temp root and socat")
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	random := make([]byte, 8)
	if _, e := io.ReadFull(o.Random, random); e != nil {
		return nil, e
	}
	id := hex.EncodeToString(random)
	bridge := &LinuxBridge{HTTPSocketPath: filepath.Join(o.TemporaryDirectory, "claude-http-"+id+".sock"), SOCKSSocketPath: filepath.Join(o.TemporaryDirectory, "claude-socks-"+id+".sock"), closeDone: make(chan struct{})}
	failed := func(e error) (*LinuxBridge, error) { _ = bridge.Close(context.Background()); return nil, e }
	for i, port := range []float64{http, socks} {
		path := bridge.HTTPSocketPath
		if i == 1 {
			path = bridge.SOCKSSocketPath
		}
		command := exec.Command(o.Socat, LinuxBridgeArguments(path, port)...)
		command.Env = append([]string{}, o.Environment...)
		if e := command.Start(); e != nil {
			if i == 0 {
				return failed(errors.New("Failed to start HTTP bridge process"))
			}
			return failed(errors.New("Failed to start SOCKS bridge process"))
		}
		bridge.processes = append(bridge.processes, command)
		done := make(chan struct{})
		bridge.waited = append(bridge.waited, done)
		go func() { _ = command.Wait(); close(done) }()
	}
	for i := 0; i < 5; i++ {
		if exists(bridge.HTTPSocketPath) && exists(bridge.SOCKSSocketPath) {
			return bridge, nil
		}
		if i == 4 {
			return failed(fmt.Errorf("Failed to create bridge sockets after %d attempts", 5))
		}
		duration := time.Duration(i) * 100 * time.Millisecond
		if duration == 0 {
			if e := ctx.Err(); e != nil {
				return failed(e)
			}
			continue
		}
		timer := time.NewTimer(duration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return failed(ctx.Err())
		case <-timer.C:
		}
	}
	return failed(errors.New("Linux bridge did not settle"))
}
func (b *LinuxBridge) Close(ctx context.Context) error {
	b.closeOnce.Do(func() {
		for _, process := range b.processes {
			if process.Process != nil {
				_ = process.Process.Signal(syscall.SIGTERM)
			}
		}
		go func() {
			var wg sync.WaitGroup
			for i, done := range b.waited {
				wg.Add(1)
				go func(index int, finished <-chan struct{}) {
					defer wg.Done()
					timer := time.NewTimer(5 * time.Second)
					defer timer.Stop()
					select {
					case <-finished:
						return
					case <-timer.C:
						if b.processes[index].Process != nil {
							_ = b.processes[index].Process.Kill()
						}
						<-finished
					}
				}(i, done)
			}
			wg.Wait()
			_ = os.Remove(b.HTTPSocketPath)
			_ = os.Remove(b.SOCKSSocketPath)
			close(b.closeDone)
		}()
	})
	select {
	case <-b.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
