//go:build darwin

package tui

import (
	"golang.org/x/sys/unix"
	"testing"
)

func TestEditorDarwinMetadataUsesPhysicalParentUIDAndMicrosecondBirth(t *testing.T) {
	var info unix.KinfoProc
	info.Proc.P_stat = 4
	info.Proc.P_pid = 201
	info.Eproc.Ppid = 200
	info.Eproc.Ucred.Uid = 501
	info.Proc.P_starttime.Sec = 123
	info.Proc.P_starttime.Usec = 456
	row := fileEditorDarwinIdentity(info)
	if row.PID != 201 || row.Parent != 200 || row.UID != 501 || row.BirthSeconds != 123 || row.BirthFraction != 456 || !row.Stopped {
		t.Fatal(row)
	}
	other := row
	other.BirthFraction++
	if row.same(other) {
		t.Fatal("PID reused within a second inherited authority")
	}
	other = row
	other.UID++
	if row.same(other) {
		t.Fatal("foreign UID inherited authority")
	}
}
