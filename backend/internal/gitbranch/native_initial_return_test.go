package gitbranch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A native missing-file listener may be slow. Registration must not execute
// that listener inline and block the caller's original registration stack.
// The bound is failure control; callback and return channels drive completion.
func TestInitialENOENTReadyDoesNotBlockWatchRegistration(t *testing.T) {
	w := &NativePollWatch{Stat: os.Stat}
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var stop func()
	var err error
	go func() {
		stop, err = w.Watch(filepath.Join(t.TempDir(), "missing"), time.Hour, func() { close(entered); <-release })
		close(returned)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actual initial ENOENT ready did not arrive")
	}
	blocked := false
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		blocked = true
	}
	close(release)
	<-returned
	if stop != nil {
		stop()
	}
	w.Join()
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("initial ENOENT listener blocked the Watch registration calling stack")
	}
}
