package taskstore

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type lease struct {
	mu             sync.Mutex
	path           string
	mtime, updated time.Time
	precision      string
	done, joined   chan struct{}
	failure        error
	released       bool
}

// Acquire the same canonical target + .lock mkdir lease used by the pinned
// proper-lockfile. Retrying and refreshing are physical I/O, never an Owner job.
func (s *Service) lock(target string) (func() error, error) {
	return s.lockWithOptions(target, "", 30, true)
}

// AcquireMailboxLease uses the original teammate mailbox's explicit lock path
// and ten retries, sharing the same proper-lockfile-compatible lease primitive.
func (s *Service) AcquireMailboxLease(target string) (func() error, error) {
	return s.lockWithOptions(target, target+".lock", 10, true)
}

// Team config creation locks config.json before that file exists. Source uses
// realpath:false for this consumer, while task and mailbox defaults stay intact.
func (s *Service) AcquireTeamConfigLease(target string) (func() error, error) {
	return s.lockWithOptions(target, target+".lock", 10, false)
}
func (s *Service) lockWithOptions(target, explicitPath string, retries int, realpath bool) (func() error, error) {
	canonical := target
	var err error
	if realpath {
		canonical, err = filepath.EvalSymlinks(target)
		if err != nil {
			return nil, err
		}
	}
	path := canonical + ".lock"
	if explicitPath != "" {
		path = explicitPath
	}
	for attempt := 0; ; attempt++ {
		err = os.Mkdir(path, 0777)
		if err == nil {
			break
		}
		if errors.Is(err, os.ErrExist) {
			stat, failure := os.Stat(path)
			if errors.Is(failure, os.ErrNotExist) {
				err = os.Mkdir(path, 0777)
			} else if failure != nil {
				err = failure
			} else if stat.ModTime().UnixMilli() < time.Now().UnixMilli()-10000 {
				err = os.Remove(path)
				if err == nil || errors.Is(err, os.ErrNotExist) {
					err = os.Mkdir(path, 0777)
				}
			} else {
				err = errors.New("Lock file is already being held")
			}
		}
		if err == nil {
			break
		}
		if attempt == retries {
			return nil, err
		}
		delay := 5 * time.Millisecond * time.Duration(1<<min(attempt, 5))
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		<-timer.C
	}
	s.mu.Lock()
	precision := s.precision
	if precision == "" {
		stamp := time.UnixMilli(int64(math.Ceil(float64(time.Now().UnixMilli())/1000)*1000 + 5))
		if err = os.Chtimes(path, stamp, stamp); err != nil {
			s.mu.Unlock()
			_ = os.Remove(path)
			return nil, err
		}
	}
	stat, err := os.Stat(path)
	if err != nil {
		s.mu.Unlock()
		_ = os.Remove(path)
		return nil, err
	}
	if precision == "" {
		precision = "ms"
		if stat.ModTime().UnixMilli()%1000 == 0 {
			precision = "s"
		}
		s.precision = precision
	}
	s.mu.Unlock()
	l := &lease{path: path, mtime: time.UnixMilli(stat.ModTime().UnixMilli()), updated: time.Now(), precision: precision, done: make(chan struct{}), joined: make(chan struct{})}
	go l.refresh()
	return func() error {
		l.mu.Lock()
		if l.released {
			l.mu.Unlock()
			return fmt.Errorf("Lock is already released")
		}
		l.released = true
		close(l.done)
		failure := l.failure
		l.mu.Unlock()
		<-l.joined
		if failure != nil {
			return failure
		}
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}, nil
}

func (l *lease) refresh() {
	defer close(l.joined)
	delay := 5 * time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-l.done:
			timer.Stop()
			return
		case <-timer.C:
		}
		l.mu.Lock()
		if l.released {
			l.mu.Unlock()
			return
		}
		stat, err := os.Stat(l.path)
		if err == nil && stat.ModTime().UnixMilli() != l.mtime.UnixMilli() {
			err = errors.New("Unable to update lock within the stale threshold")
			l.failure = err
			l.mu.Unlock()
			return
		}
		if err == nil {
			stamp := time.Now().UnixMilli()
			if l.precision == "s" {
				stamp = int64(math.Ceil(float64(stamp)/1000) * 1000)
			}
			mtime := time.UnixMilli(stamp)
			err = os.Chtimes(l.path, mtime, mtime)
			if err == nil {
				l.mtime, l.updated = mtime, time.Now()
				delay = 5 * time.Second
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || time.Since(l.updated) > 10*time.Second {
				l.failure = err
				l.mu.Unlock()
				return
			}
			delay = time.Second
		}
		l.mu.Unlock()
	}
}

func ensureLockTarget(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if file != nil {
		return file.Close()
	}
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
