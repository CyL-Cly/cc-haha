// Generic native Unix/TCP byte relay used solely at the socat executable seam.
// It executes no CLI, sandbox, settings, Agent, or protocol business.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(os.Args[1], "UNIX-LISTEN:"), ",fork,reuseaddr")
	target := strings.Split(strings.TrimPrefix(os.Args[2], "TCP:"), ",")[0]
	listener, e := net.Listen("unix", path)
	if e != nil {
		os.WriteFile(filepath.Join(filepath.Dir(path), "listen-error-"+filepath.Base(path)), []byte(fmt.Sprint(e)), 0600)
		os.Exit(3)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	go func() {
		<-signals
		listener.Close()
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
	}()
	for {
		c, e := listener.Accept()
		if e != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close()
			up, e := net.Dial("tcp", target)
			if e != nil {
				return
			}
			defer up.Close()
			mu.Lock()
			connections[c] = true
			connections[up] = true
			mu.Unlock()
			done := make(chan struct{})
			go func() { io.Copy(up, c); up.Close(); close(done) }()
			io.Copy(c, up)
			c.Close()
			<-done
			mu.Lock()
			delete(connections, c)
			delete(connections, up)
			mu.Unlock()
		}()
	}
	wg.Wait()
}
