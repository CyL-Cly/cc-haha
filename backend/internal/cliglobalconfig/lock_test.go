package cliglobalconfig

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type leaseCapabilities struct {
	OSFileSystem
	mkdir   func(string) error
	stat    func(string) (FileStat, error)
	remove  func(string) error
	chtimes func(string, time.Time) error
}

func (capabilities leaseCapabilities) Mkdir(path string) error {
	if capabilities.mkdir != nil {
		return capabilities.mkdir(path)
	}
	return capabilities.OSFileSystem.Mkdir(path)
}
func (capabilities leaseCapabilities) Stat(path string) (FileStat, error) {
	if capabilities.stat != nil {
		return capabilities.stat(path)
	}
	return capabilities.OSFileSystem.Stat(path)
}
func (capabilities leaseCapabilities) Remove(path string) error {
	if capabilities.remove != nil {
		return capabilities.remove(path)
	}
	return capabilities.OSFileSystem.Remove(path)
}
func (capabilities leaseCapabilities) Chtimes(path string, stamp time.Time) error {
	if capabilities.chtimes != nil {
		return capabilities.chtimes(path, stamp)
	}
	return capabilities.OSFileSystem.Chtimes(path, stamp)
}

func TestLeaseFailureDoesNotDeleteForeignOrLeaveUnprobedLease(t *testing.T) {
	for _, boundary := range []string{"mkdir", "stat-existing", "remove-stale", "precision-update", "precision-stat"} {
		t.Run(boundary, func(t *testing.T) {
			capabilities := leaseCapabilities{}
			var lockPath string
			switch boundary {
			case "mkdir":
				capabilities.mkdir = func(string) error { return fs.ErrPermission }
			case "stat-existing", "precision-stat":
				capabilities.stat = func(path string) (FileStat, error) {
					if path == lockPath {
						return FileStat{}, fs.ErrPermission
					}
					return OSFileSystem{}.Stat(path)
				}
			case "remove-stale":
				capabilities.remove = func(string) error { return fs.ErrPermission }
			case "precision-update":
				capabilities.chtimes = func(string, time.Time) error { return fs.ErrPermission }
			}
			service := enabledService(t, Options{FS: capabilities}, "{}")
			lockPath = service.Path() + ".lock"
			foreign := boundary == "stat-existing" || boundary == "remove-stale"
			if foreign {
				_ = os.Mkdir(lockPath, 0700)
				old := time.Now().Add(-time.Minute)
				_ = os.Chtimes(lockPath, old, old)
			}
			if _, err := service.acquireLock(); !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("failure must retain the original capability error: %v", err)
			}
			if exists := (OSFileSystem{}).Exists(lockPath); exists != foreign {
				t.Fatalf("foreign=%v remaining=%v", foreign, exists)
			}
		})
	}
}

func TestLeaseDisappearsBetweenMkdirAndStatWithoutStealingReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "foreign-replacement"}[replacement], func(t *testing.T) {
			var lockPath string
			var raced bool
			capabilities := leaseCapabilities{stat: func(path string) (FileStat, error) {
				if path == lockPath && !raced {
					raced = true
					_ = os.Remove(path)
					if replacement {
						_ = os.Mkdir(path, 0700)
					}
					return FileStat{}, fs.ErrNotExist
				}
				return OSFileSystem{}.Stat(path)
			}}
			service := enabledService(t, Options{FS: capabilities}, "{}")
			lockPath = service.Path() + ".lock"
			_ = os.Mkdir(lockPath, 0700)
			release, err := service.acquireLock()
			if replacement {
				if err == nil || err.Error() != "Lock file is already being held" || !(OSFileSystem{}).Exists(lockPath) {
					t.Fatalf("replacement stolen: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if err := release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLeaseSecondPrecisionRetryAndSynchronousSaveFrame(t *testing.T) {
	var now atomic.Int64
	now.Store(946684800001)
	var updateFailure atomic.Bool
	scheduled := make(chan *fixtureTimer, 8)
	delays := make(chan time.Duration, 8)
	capabilities := leaseCapabilities{chtimes: func(path string, stamp time.Time) error {
		if updateFailure.Swap(false) {
			return fs.ErrPermission
		}
		return (OSFileSystem{}).Chtimes(path, stamp.Truncate(time.Second))
	}}
	service := enabledService(t, Options{FS: capabilities, Now: func() time.Time { return time.UnixMilli(now.Load()) }, Timer: func(delay time.Duration) LeaseTimer {
		timer := &fixtureTimer{ready: make(chan time.Time, 1)}
		delays <- delay
		scheduled <- timer
		return timer
	}}, "{}")
	release, err := service.acquireLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	timer := <-scheduled
	if <-delays != 5*time.Second {
		t.Fatal("initial lease interval changed")
	}
	now.Store(946684805001)
	updateFailure.Store(true)
	timer.ready <- time.UnixMilli(now.Load())
	timer = <-scheduled
	if <-delays != time.Second {
		t.Fatal("transient error did not schedule the Source one-second retry")
	}
	now.Store(946684806001)
	timer.ready <- time.UnixMilli(now.Load())
	timer = <-scheduled
	if <-delays != 5*time.Second {
		t.Fatal("successful retry retained the failure interval")
	}
	stat, _ := os.Stat(service.Path() + ".lock")
	if stat.ModTime().UnixMilli() != 946684807000 {
		t.Fatal("second precision was not rounded upward", stat.ModTime())
	}
	// Source lockSync is used in a synchronous Save frame. Even when a timer
	// becomes ready during that frame, no background renewal may interleave.
	service.saveMu.Lock()
	prior := stat.ModTime()
	now.Store(946684811001)
	timer.ready <- time.UnixMilli(now.Load())
	<-scheduled
	<-delays
	stat, _ = os.Stat(service.Path() + ".lock")
	service.saveMu.Unlock()
	if !stat.ModTime().Equal(prior) {
		t.Fatal("background renewal strengthened Source sync-frame semantics")
	}
}

func TestReleaseAfterExternalRemovalIsIdempotentOnlyOnce(t *testing.T) {
	service := enabledService(t, Options{}, "{}")
	release, err := service.acquireLock()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(service.Path() + ".lock")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil || !strings.Contains(err.Error(), "already released") {
		t.Fatal("repeated release was accepted", err)
	}
}
