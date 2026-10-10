package cliglobalconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sync"
	"time"
)

// The lease follows the pinned proper-lockfile 4.1.2 mkdir/mtime algorithm.
// Its sync API uses no retry delay. A failed lock is intentionally handled by
// Save's unlocked fallback rather than turned into a stronger new policy.
type lease struct {
	mu                    sync.Mutex
	path                  string
	mtime                 float64
	lastUpdate            int64
	released, compromised bool
	done                  chan struct{}
	stop                  sync.Once
}
type realLeaseTimer struct{ *time.Timer }

func (timer realLeaseTimer) Channel() <-chan time.Time { return timer.C }

func (service *Service) acquireLock() (func() error, error) {
	if _, err := service.options.FS.Realpath(service.Path()); err != nil {
		return nil, err
	}
	path := service.Path() + ".lock"
	var acquire func(bool) error
	acquire = func(stale bool) error {
		err := service.options.FS.Mkdir(path)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if !stale {
			return fmt.Errorf("Lock file is already being held")
		}
		stat, err := service.options.FS.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return acquire(false)
		}
		if err != nil {
			return err
		}
		if stat.Mtime >= float64(service.options.Now().UnixMilli()-10000) {
			return fmt.Errorf("Lock file is already being held")
		}
		if err := service.options.FS.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return acquire(false)
	}
	if err := acquire(true); err != nil {
		return nil, err
	}
	service.stateMu.Lock()
	precision := service.precision
	service.stateMu.Unlock()
	if precision == "" {
		stamp := time.UnixMilli(int64(math.Ceil(float64(service.options.Now().UnixMilli())/1000)*1000 + 5))
		if err := service.options.FS.Chtimes(path, stamp); err != nil {
			_ = service.options.FS.Remove(path)
			return nil, err
		}
	}
	stat, err := service.options.FS.Stat(path)
	if err != nil {
		_ = service.options.FS.Remove(path)
		return nil, err
	}
	if precision == "" {
		precision = "ms"
		if int64(stat.Mtime)%1000 == 0 {
			precision = "s"
		}
		service.stateMu.Lock()
		service.precision = precision
		service.stateMu.Unlock()
	}
	lock := &lease{path: path, mtime: float64(int64(stat.Mtime)), lastUpdate: service.options.Now().UnixMilli(), done: make(chan struct{})}
	go service.keepLease(lock, precision)
	return func() error {
		lock.mu.Lock()
		defer lock.mu.Unlock()
		if lock.released {
			return fmt.Errorf("Lock is already released")
		}
		lock.stop.Do(func() { close(lock.done) })
		lock.released = true
		err := service.options.FS.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}, nil
}
func (service *Service) keepLease(lock *lease, precision string) {
	delay := 5 * time.Second
	for {
		timer := service.options.Timer(delay)
		select {
		case <-lock.done:
			timer.Stop()
			return
		case <-timer.Channel():
		}
		if !service.saveMu.TryLock() {
			continue
		}
		lock.mu.Lock()
		if lock.released {
			lock.mu.Unlock()
			service.saveMu.Unlock()
			return
		}
		now := service.options.Now().UnixMilli()
		stat, err := service.options.FS.Stat(lock.path)
		over := lock.lastUpdate+10000 < now
		if err == nil && float64(int64(stat.Mtime)) != lock.mtime {
			err = fmt.Errorf("Unable to update lock within the stale threshold")
			over = true
		}
		if err == nil {
			stamp := now
			if precision == "s" {
				stamp = int64(math.Ceil(float64(now)/1000) * 1000)
			}
			err = service.options.FS.Chtimes(lock.path, time.UnixMilli(stamp))
			if err == nil {
				lock.mtime = float64(stamp)
				lock.lastUpdate = now
				delay = 5 * time.Second
			}
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || over {
				lock.released = true
				lock.compromised = true
				lock.stop.Do(func() { close(lock.done) })
				lock.mu.Unlock()
				service.saveMu.Unlock()
				service.emit("lock_compromised", map[string]any{"error": leaseErrorMessage(err)})
				return
			}
			delay = time.Second
		}
		lock.mu.Unlock()
		service.saveMu.Unlock()
	}
}

func leaseErrorMessage(err error) string {
	var failure *os.PathError
	if errors.As(err, &failure) && errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("ENOENT: no such file or directory, stat '%s'", failure.Path)
	}
	return err.Error()
}
