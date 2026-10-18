//go:build darwin

package tui

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

// Apple SDK sys/proc.h: SSTOP == 4 (process debugging or suspension).
func fileEditorDarwinIdentity(p unix.KinfoProc) fileEditorProcessIdentity {
	return fileEditorProcessIdentity{PID: int(p.Proc.P_pid), Parent: int(p.Eproc.Ppid), UID: p.Eproc.Ucred.Uid, Stopped: p.Proc.P_stat == 4, BirthSeconds: uint64(p.Proc.P_starttime.Sec), BirthFraction: uint64(p.Proc.P_starttime.Usec)}
}
func readFileEditorProcess(pid int) (fileEditorProcessIdentity, bool, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) {
		return fileEditorProcessIdentity{}, false, nil
	}
	if err != nil {
		return fileEditorProcessIdentity{}, false, err
	}
	if len(rows) == 0 {
		return fileEditorProcessIdentity{}, false, nil
	}
	if len(rows) != 1 || int(rows[0].Proc.P_pid) != pid {
		return fileEditorProcessIdentity{}, false, errors.New("unexpected editor process identity response")
	}
	return fileEditorDarwinIdentity(rows[0]), true, nil
}
func snapshotFileEditorProcesses() ([]fileEditorProcessIdentity, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Geteuid())
	if err != nil {
		return nil, err
	}
	out := make([]fileEditorProcessIdentity, 0, len(rows))
	for _, p := range rows {
		out = append(out, fileEditorDarwinIdentity(p))
	}
	return out, nil
}
