package gitbranch

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualNativePollFileCreationModificationRemovalAndStop(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "HEAD")
	events := make(chan struct{}, 4)
	watch := &NativePollWatch{Stat: os.Stat}
	stop, e := watch.Watch(p, time.Millisecond, func() { events <- struct{}{} })
	if e != nil {
		t.Fatal(e)
	}
	<-events // first native ENOENT receipt
	wait := func() {
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("actual poll notification absent")
		}
	}
	if e = os.WriteFile(p, []byte("one"), 0600); e != nil {
		t.Fatal(e)
	}
	wait()
	if e = os.WriteFile(p, []byte("different-size"), 0600); e != nil {
		t.Fatal(e)
	}
	wait()
	if e = os.Remove(p); e != nil {
		t.Fatal(e)
	}
	wait()
	stop()
	stop()
	watch.Join()
	if e = os.WriteFile(p, []byte("after-unwatch"), 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case <-events:
		t.Fatal("callback after joined unwatch")
	default:
	}
}
func TestPollUnwatchDoesNotJoinAlreadyStartedStat(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	reads := 0
	w := &NativePollWatch{Stat: func(string) (fs.FileInfo, error) {
		reads++
		if reads == 2 {
			close(started)
			<-release
		}
		return nil, fs.ErrNotExist
	}}
	stop, e := w.Watch("original", time.Millisecond, func() {})
	if e != nil {
		t.Fatal(e)
	}
	<-started
	stop()
	close(release)
	w.Join()
	if reads < 2 {
		t.Fatal("native read not run")
	}
}
