//go:build !windows

package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type editorTreeFixture struct {
	rows         map[int]fileEditorProcessIdentity
	events       []string
	snapshots    int
	now          time.Time
	late         bool
	failSnapshot bool
	failKill     bool
	replaceChild bool
	childReads   int
	stopped      map[int]bool
}

func editorTreeRow(pid, parent int) fileEditorProcessIdentity {
	return fileEditorProcessIdentity{PID: pid, Parent: parent, UID: 123, BirthSeconds: 10, BirthFraction: uint64(pid)}
}
func (f *editorTreeFixture) system() fileEditorProcessSystem {
	return fileEditorProcessSystem{uid: 123, rootAlive: func() error { return nil }, now: func() time.Time { return f.now }, pause: func(d time.Duration) { f.now = f.now.Add(d) }, read: func(pid int) (fileEditorProcessIdentity, bool, error) {
		if pid == 201 {
			f.childReads++
			if f.replaceChild && f.childReads == 3 {
				r := f.rows[pid]
				r.BirthFraction++
				f.rows[pid] = r
			}
		}
		row, ok := f.rows[pid]
		return row, ok, nil
	}, snapshot: func() ([]fileEditorProcessIdentity, error) {
		f.snapshots++
		if f.failSnapshot {
			return nil, errors.New("owned snapshot failed")
		}
		if f.late && f.snapshots == 2 {
			f.rows[202] = editorTreeRow(202, 201)
		}
		out := []fileEditorProcessIdentity{}
		for _, r := range f.rows {
			out = append(out, r)
		}
		return out, nil
	}, signal: func(pid int, sig syscall.Signal) error {
		f.events = append(f.events, fmt.Sprintf("%d:%d", pid, sig))
		switch sig {
		case syscall.SIGSTOP:
			f.stopped[pid] = true
			row := f.rows[pid]
			row.Stopped = true
			f.rows[pid] = row
		case syscall.SIGCONT:
			f.stopped[pid] = false
			row := f.rows[pid]
			row.Stopped = false
			f.rows[pid] = row
		case syscall.SIGKILL:
			if f.failKill {
				return syscall.EPERM
			}
			delete(f.rows, pid)
			delete(f.stopped, pid)
		}
		return nil
	}}
}
func newEditorTreeFixture() *editorTreeFixture {
	return &editorTreeFixture{rows: map[int]fileEditorProcessIdentity{200: editorTreeRow(200, 99), 201: editorTreeRow(201, 200), 300: editorTreeRow(300, 99)}, stopped: map[int]bool{}}
}
func TestEditorTreeCancellationStopsLateDescendantsWithoutSignalingTTYPeers(t *testing.T) {
	f := newEditorTreeFixture()
	f.late = true
	foreign := editorTreeRow(400, 200)
	foreign.UID = 999
	f.rows[400] = foreign
	if err := cancelFileEditorTree(200, f.system(), func() error { t.Fatal("unexpected direct fallback"); return nil }); err != nil {
		t.Fatal(err)
	}
	expected := []string{fmt.Sprintf("200:%d", syscall.SIGSTOP), fmt.Sprintf("201:%d", syscall.SIGSTOP), fmt.Sprintf("202:%d", syscall.SIGSTOP), fmt.Sprintf("202:%d", syscall.SIGKILL), fmt.Sprintf("201:%d", syscall.SIGKILL), fmt.Sprintf("200:%d", syscall.SIGKILL)}
	if strings.Join(f.events, ",") != strings.Join(expected, ",") {
		t.Fatalf("actual freeze/kill order %v", f.events)
	}
	if len(f.rows) != 2 || f.rows[300].PID != 300 || f.rows[400].UID != 999 {
		t.Fatal("same-TTY peer or foreign UID was touched", f.rows)
	}
}
func TestEditorTreeCancellationRejectsReusedPIDBeforeAnySignalToNewIdentity(t *testing.T) {
	f := newEditorTreeFixture()
	f.replaceChild = true
	err := cancelFileEditorTree(200, f.system(), func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatal("reused PID falsely joined", err)
	}
	for _, e := range f.events {
		if e == fmt.Sprintf("201:%d", syscall.SIGKILL) || e == fmt.Sprintf("201:%d", syscall.SIGCONT) {
			t.Fatal("signal borrowed old identity", f.events)
		}
	}
	if f.rows[201].BirthFraction != 202 || f.rows[300].PID != 300 {
		t.Fatal("foreign/current identity changed", f.rows)
	}
}
func TestEditorTreeCancellationSnapshotFailureStillTerminatesStoppedRoot(t *testing.T) {
	f := newEditorTreeFixture()
	delete(f.rows, 201)
	f.failSnapshot = true
	err := cancelFileEditorTree(200, f.system(), func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "owned snapshot failed") {
		t.Fatal(err)
	}
	if _, ok := f.rows[200]; ok {
		t.Fatal("stopped root leaked", f.events)
	}
}
func TestEditorTreeCancellationFailedKillUnstopsOwnedProcessAndReportsJoinFailure(t *testing.T) {
	f := newEditorTreeFixture()
	delete(f.rows, 201)
	f.failKill = true
	err := cancelFileEditorTree(200, f.system(), func() error { return nil })
	if !errors.Is(err, syscall.EPERM) || !strings.Contains(err.Error(), "could not join") {
		t.Fatal("kill/join failure lost", err)
	}
	if f.stopped[200] {
		t.Fatal("failed cancellation left owned root stopped", f.events)
	}
}
func TestEditorTreeCancellationPresentKilledProcessCannotCountAsJoined(t *testing.T) {
	f := newEditorTreeFixture()
	delete(f.rows, 201)
	system := f.system()
	system.signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGSTOP {
			row := f.rows[pid]
			row.Stopped = true
			f.rows[pid] = row
		}
		return nil
	}
	err := cancelFileEditorTree(200, system, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "could not join PIDs [200]") {
		t.Fatal("present or zombie process counted as gone", err)
	}
}

func TestEditorTreeCancellationCannotBorrowParentPIDAcrossBirth(t *testing.T) {
	f := newEditorTreeFixture()
	system := f.system()
	system.snapshot = func() ([]fileEditorProcessIdentity, error) {
		parent := f.rows[200]
		parent.BirthFraction++
		return []fileEditorProcessIdentity{parent, f.rows[201]}, nil
	}
	err := cancelFileEditorTree(200, system, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "frozen editor identity") {
		t.Fatal("changed parent snapshot accepted", err)
	}
	for _, e := range f.events {
		if strings.HasPrefix(e, "201:") {
			t.Fatal("unknown parent identity granted child authority", f.events)
		}
	}
}
func TestEditorTreeCancellationIdentityReadFailureUsesOwnedRootHandle(t *testing.T) {
	system := fileEditorProcessSystem{read: func(int) (fileEditorProcessIdentity, bool, error) {
		return fileEditorProcessIdentity{}, false, errors.New("metadata failed")
	}}
	killed := false
	err := cancelFileEditorTree(200, system, func() error { killed = true; return nil })
	if !killed || err == nil || !strings.Contains(err.Error(), "metadata failed") {
		t.Fatal("root fallback or actual error lost", killed, err)
	}
}
func TestEditorTreeCancellationAlreadyExitedDoesNotSignal(t *testing.T) {
	system := fileEditorProcessSystem{read: func(int) (fileEditorProcessIdentity, bool, error) { return fileEditorProcessIdentity{}, false, nil }}
	err := cancelFileEditorTree(200, system, func() error { t.Fatal("already exited root signaled"); return nil })
	if err != os.ErrProcessDone {
		t.Fatal(err)
	}
}

func TestEditorTreeCancellationRootDoneFenceCannotBorrowReusedRootPID(t *testing.T) {
	for _, gone := range []error{os.ErrProcessDone, syscall.ESRCH} {
		f := newEditorTreeFixture()
		system := f.system()
		system.rootAlive = func() error { return gone }
		err := cancelFileEditorTree(200, system, func() error { t.Fatal("done spawn root must not kill current PID"); return nil })
		if err != os.ErrProcessDone || len(f.events) != 0 || len(f.rows) != 3 {
			t.Fatal("reused root acquired signal authority", gone, err, f.events)
		}
	}
}

func TestEditorTreeCancellationWaitsForActualStoppedMetadataBeforeSnapshot(t *testing.T) {
	f := newEditorTreeFixture()
	delete(f.rows, 201)
	system := f.system()
	read := system.read
	send := system.signal
	snapshot := system.snapshot
	observed := 0
	system.signal = func(pid int, sig syscall.Signal) error {
		err := send(pid, sig)
		if sig == syscall.SIGSTOP {
			row := f.rows[pid]
			row.Stopped = false
			f.rows[pid] = row
		}
		return err
	}
	system.read = func(pid int) (fileEditorProcessIdentity, bool, error) {
		row, exists, err := read(pid)
		if pid == 200 && exists && f.stopped[pid] {
			observed++
			if observed >= 3 {
				row.Stopped = true
				f.rows[pid] = row
			}
		}
		return row, exists, err
	}
	system.snapshot = func() ([]fileEditorProcessIdentity, error) {
		if !f.rows[200].Stopped {
			t.Fatal("snapshot preceded actual stop")
		}
		return snapshot()
	}
	if err := cancelFileEditorTree(200, system, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if observed < 3 {
		t.Fatal("signal delivery treated as stopped metadata", observed)
	}
}

func TestEditorTreeCancellationUnconfirmedStopFailsBoundedAndStillKillsRoot(t *testing.T) {
	f := newEditorTreeFixture()
	delete(f.rows, 201)
	system := f.system()
	send := system.signal
	system.signal = func(pid int, sig syscall.Signal) error {
		err := send(pid, sig)
		if sig == syscall.SIGSTOP {
			row := f.rows[pid]
			row.Stopped = false
			f.rows[pid] = row
		}
		return err
	}
	err := cancelFileEditorTree(200, system, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "did not enter stopped state") {
		t.Fatal("unconfirmed stop accepted", err)
	}
	if f.snapshots != 0 || f.rows[200].PID != 0 {
		t.Fatal("unfrozen root used for discovery or leaked", f.snapshots, f.rows)
	}
}
