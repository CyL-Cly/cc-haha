//go:build linux

package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func parseFileEditorLinuxProcess(data []byte, uid uint32) (fileEditorProcessIdentity, error) {
	text := string(data)
	start := strings.IndexByte(text, '(')
	end := strings.LastIndexByte(text, ')')
	if start < 1 || end <= start {
		return fileEditorProcessIdentity{}, errors.New("invalid editor proc stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:start]))
	if err != nil {
		return fileEditorProcessIdentity{}, err
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 20 {
		return fileEditorProcessIdentity{}, errors.New("short editor proc stat")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return fileEditorProcessIdentity{}, err
	}
	birth, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return fileEditorProcessIdentity{}, err
	}
	return fileEditorProcessIdentity{PID: pid, Parent: parent, UID: uid, Stopped: fields[0] == "T" || fields[0] == "t", BirthFraction: birth}, nil
}
func readFileEditorProcess(pid int) (fileEditorProcessIdentity, bool, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid))
	data, err := os.ReadFile(filepath.Join(path, "stat"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return fileEditorProcessIdentity{}, false, nil
	}
	if err != nil {
		return fileEditorProcessIdentity{}, false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileEditorProcessIdentity{}, false, nil
	}
	if err != nil {
		return fileEditorProcessIdentity{}, false, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileEditorProcessIdentity{}, false, errors.New("editor proc owner metadata unavailable")
	}
	row, err := parseFileEditorLinuxProcess(data, st.Uid)
	if err == nil && row.PID != pid {
		err = fmt.Errorf("editor proc PID mismatch for %d", pid)
	}
	return row, err == nil, err
}
func snapshotFileEditorProcesses() ([]fileEditorProcessIdentity, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	rows := []fileEditorProcessIdentity{}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid <= 0 {
			continue
		}
		info, e := entry.Info()
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, errors.New("editor proc table owner metadata unavailable")
		}
		if owner.Uid != uint32(os.Geteuid()) {
			continue
		}
		row, present, e := readFileEditorProcess(pid)
		if e != nil {
			return nil, e
		}
		if present && row.UID == uint32(os.Geteuid()) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
