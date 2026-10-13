package cliinstallation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pidLock struct {
	PID        int    `json:"pid"`
	Version    string `json:"version"`
	Executable string `json:"execPath"`
	AcquiredAt int64  `json:"acquiredAt"`
}

func environmentTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func readPIDLock(path string) *pidLock {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var value pidLock
	if json.Unmarshal(data, &value) != nil || value.Version == "" || value.Executable == "" {
		return nil
	}
	return &value
}

func activePIDLock(path string) bool {
	value := readPIDLock(path)
	if value == nil || !processRunning(value.PID) {
		return false
	}
	if value.PID == os.Getpid() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var command *exec.Cmd
	if os.PathSeparator == '\\' {
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", "(Get-CimInstance Win32_Process -Filter \"ProcessId="+strconv.Itoa(value.PID)+"\").CommandLine")
	} else {
		command = exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(value.PID))
	}
	data, err := command.Output()
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return true
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, "claude") || strings.Contains(text, strings.ToLower(value.Executable))
}

// Follow the Source PID lock's atomic write and post-write ownership check.
func (s *Installer) acquirePID(path, version string) func() {
	if activePIDLock(path) {
		return nil
	}
	executable, _ := os.Executable()
	value := pidLock{os.Getpid(), version, executable, s.options.Now().UnixMilli()}
	data, _ := json.MarshalIndent(value, "", "  ")
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return nil
	}
	if current := readPIDLock(path); current == nil || current.PID != value.PID {
		return nil
	}
	return func() {
		if current := readPIDLock(path); current != nil && current.PID == value.PID {
			_ = os.Remove(path)
		}
	}
}

// HoldCurrent protects only an executable running from the native versions
// directory. Help/version fast paths never acquire this startup lease.
func (s *Installer) HoldCurrent() func() {
	executable, err := os.Executable()
	if err != nil {
		return func() {}
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	if !strings.Contains(executable, s.dirs.Versions) {
		return func() {}
	}
	if os.MkdirAll(s.dirs.Locks, 0755) != nil {
		return func() {}
	}
	version := filepath.Base(executable)
	path := filepath.Join(s.dirs.Locks, version+".lock")
	if s.pidLocking() {
		if release := s.acquirePID(path, version); release != nil {
			return release
		}
		return func() {}
	}
	if release, err := s.acquireMtime(path); err == nil {
		return release
	}
	return func() {}
}

func (s *Installer) acquireMtime(path string) (func(), error) {
	err := os.Mkdir(path, 0755)
	if errors.Is(err, os.ErrExist) {
		if info, e := os.Stat(path); e == nil && s.options.Now().Sub(info.ModTime()) > lockStale {
			if os.Remove(path) == nil {
				err = os.Mkdir(path, 0755)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(lockStale / 2)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				current, err := os.Stat(path)
				if err != nil || !os.SameFile(info, current) {
					return
				}
				now := s.options.Now()
				if os.Chtimes(path, now, now) != nil {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			if current, err := os.Stat(path); err == nil && os.SameFile(info, current) {
				_ = os.Remove(path)
			}
		})
	}, nil
}
