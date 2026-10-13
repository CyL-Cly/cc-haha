package cliinstallation

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const lockStale = 7 * 24 * time.Hour
const retainedVersions = 2

var orphanedInstall = regexp.MustCompile(`\.tmp\.\d+\.\d+$`)
var oldWindowsExecutable = regexp.MustCompile(`^claude\.exe\.old\.\d+$`)

// The original distribution has no GrowthBook client key, so its cached PID
// rollout gate returns false. Only its explicit environment override enables it.
func (s *Installer) pidLocking() bool {
	return environmentTruthy(s.options.Environment["ENABLE_PID_BASED_VERSION_LOCKING"])
}

func (s *Installer) activeVersionLock(path string) bool {
	if s.pidLocking() {
		return activePIDLock(path)
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir() && s.options.Now().Sub(info.ModTime()) <= lockStale
}

func possibleBinary(info os.FileInfo, windows bool) bool {
	return info.Mode().IsRegular() && (windows || info.Size() == 0 || info.Mode().Perm()&0111 != 0)
}

// CleanupOldVersions uses the original native installer's retention boundary:
// protect the running version, the active launcher target and live leases;
// retain two newest unprotected binaries; re-acquire a lease before deletion.
func (s *Installer) CleanupOldVersions(ctx context.Context) {
	windows := s.options.Platform == "windows" || s.options.Platform == "win32"
	if windows {
		entries, _ := os.ReadDir(filepath.Dir(s.dirs.Executable))
		for _, entry := range entries {
			if oldWindowsExecutable.MatchString(entry.Name()) {
				_ = os.Remove(filepath.Join(filepath.Dir(s.dirs.Executable), entry.Name()))
			}
		}
	}
	oneHourAgo := s.options.Now().Add(-time.Hour)
	staging, _ := os.ReadDir(s.dirs.Staging)
	for _, entry := range staging {
		path := filepath.Join(s.dirs.Staging, entry.Name())
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(oneHourAgo) {
			_ = os.RemoveAll(path)
		}
	}
	if s.pidLocking() {
		entries, _ := os.ReadDir(s.dirs.Locks)
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".lock") {
				continue
			}
			path := filepath.Join(s.dirs.Locks, entry.Name())
			if info, err := os.Lstat(path); err == nil {
				if info.IsDir() {
					_ = os.RemoveAll(path)
				} else if !activePIDLock(path) {
					_ = os.Remove(path)
				}
			}
		}
	}
	type candidate struct {
		path  string
		mtime time.Time
	}
	entries, err := os.ReadDir(s.dirs.Versions)
	if err != nil {
		return
	}
	protected := map[string]bool{}
	if executable, err := os.Executable(); err == nil && strings.Contains(executable, s.dirs.Versions) {
		protected[filepath.Clean(executable)] = true
	}
	if target, err := os.Readlink(s.dirs.Executable); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(s.dirs.Executable), target)
		}
		if info, err := os.Stat(target); err == nil && possibleBinary(info, windows) {
			protected[filepath.Clean(target)] = true
		}
	}
	var eligible []candidate
	for _, entry := range entries {
		path := filepath.Join(s.dirs.Versions, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if orphanedInstall.MatchString(entry.Name()) {
			if info.ModTime().Before(oneHourAgo) {
				_ = os.Remove(path)
			}
			continue
		}
		if !possibleBinary(info, windows) || protected[path] || s.activeVersionLock(filepath.Join(s.dirs.Locks, entry.Name()+".lock")) {
			continue
		}
		eligible = append(eligible, candidate{path, info.ModTime()})
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].mtime.After(eligible[j].mtime) })
	for i := retainedVersions; i < len(eligible); i++ {
		if ctx.Err() != nil {
			return
		}
		entry := eligible[i]
		if os.MkdirAll(s.dirs.Locks, 0755) != nil {
			continue
		}
		path := filepath.Join(s.dirs.Locks, filepath.Base(entry.path)+".lock")
		var release func()
		if s.pidLocking() {
			release = s.acquirePID(path, filepath.Base(entry.path))
		} else {
			release, _ = s.acquireMtime(path)
		}
		if release != nil {
			_ = os.Remove(entry.path)
			release()
		}
	}
}
