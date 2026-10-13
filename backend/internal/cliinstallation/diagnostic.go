package cliinstallation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type VersionLockInformation struct {
	Version          string     `json:"version"`
	PID              int        `json:"pid"`
	IsProcessRunning bool       `json:"isProcessRunning"`
	Executable       string     `json:"execPath"`
	AcquiredAt       *time.Time `json:"acquiredAt"`
	Path             string     `json:"lockFilePath"`
}
type LockDiagnostic struct {
	Enabled   bool                     `json:"enabled"`
	Locks     []VersionLockInformation `json:"locks"`
	Directory string                   `json:"locksDir"`
	Cleaned   int                      `json:"staleLocksCleaned"`
}

// DiagnosticLocks performs Source's explicit Doctor stale lease cleanup,
// followed by its read-only list. It does not inspect or remove versions.
func (s *Installer) DiagnosticLocks(ctx context.Context) LockDiagnostic {
	result := LockDiagnostic{Enabled: s.pidLocking(), Locks: []VersionLockInformation{}, Directory: s.dirs.Locks}
	if !result.Enabled {
		return result
	}
	entries, e := diagnosticEntries(s.dirs.Locks)
	if e != nil {
		return result
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return result
		}
		if !strings.HasSuffix(entry, ".lock") {
			continue
		}
		path := filepath.Join(s.dirs.Locks, entry)
		info, e := os.Lstat(path)
		if e != nil {
			continue
		}
		if info.IsDir() {
			if os.RemoveAll(path) == nil {
				result.Cleaned++
			}
		} else if !activePIDLock(path) {
			if os.Remove(path) == nil {
				result.Cleaned++
			}
		}
	}
	entries, _ = diagnosticEntries(s.dirs.Locks)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return result
		}
		if !strings.HasSuffix(entry, ".lock") {
			continue
		}
		path := filepath.Join(s.dirs.Locks, entry)
		if value := readPIDLock(path); value != nil {
			acquired := time.UnixMilli(value.AcquiredAt).UTC()
			result.Locks = append(result.Locks, VersionLockInformation{Version: value.Version, PID: value.PID, IsProcessRunning: processRunning(value.PID), Executable: value.Executable, AcquiredAt: &acquired, Path: path})
		}
	}
	return result
}

func diagnosticEntries(directory string) ([]string, error) {
	file, e := os.Open(directory)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	return file.Readdirnames(-1)
}
