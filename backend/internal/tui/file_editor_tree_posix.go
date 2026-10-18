//go:build !windows

package tui

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"
)

// Only process metadata is consumed: no argv, environment or user files.
// Parent is admission evidence; birth and UID prevent a reused PID inheriting it.
type fileEditorProcessIdentity struct {
	PID, Parent                 int
	UID                         uint32
	Stopped                     bool
	BirthSeconds, BirthFraction uint64
}

func (p fileEditorProcessIdentity) same(q fileEditorProcessIdentity) bool {
	return p.PID == q.PID && p.UID == q.UID && p.BirthSeconds == q.BirthSeconds && p.BirthFraction == q.BirthFraction
}

type fileEditorProcessSystem struct {
	uid       uint32
	rootAlive func() error
	read      func(int) (fileEditorProcessIdentity, bool, error)
	snapshot  func() ([]fileEditorProcessIdentity, error)
	signal    func(int, syscall.Signal) error
	now       func() time.Time
	pause     func(time.Duration)
}

func cancelFileEditorProcess(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	system := fileEditorProcessSystem{uid: uint32(os.Geteuid()), rootAlive: func() error { return process.Signal(syscall.Signal(0)) }, read: readFileEditorProcess, snapshot: snapshotFileEditorProcesses, signal: func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }, now: time.Now, pause: time.Sleep}
	return cancelFileEditorTree(process.Pid, system, process.Kill)
}

// The editor inherits the TUI's controlling terminal/group. Never signal that
// group. Freeze only this spawned root and descendants proved by a current
// parent identity; a second snapshot catches children born before their stop.
// Previously orphaned/unobserved daemons cannot be inferred from this ancestry.
func cancelFileEditorTree(rootPID int, system fileEditorProcessSystem, directKill func() error) error {
	root, exists, err := system.read(rootPID)
	if err != nil {
		return errors.Join(fmt.Errorf("read editor process identity: %w", err), directKill())
	}
	if !exists {
		return os.ErrProcessDone
	}
	// Metadata may have been read after exec.Cmd.Wait reaped the spawned root.
	// The original Go Process's Done fence prevents lending its PID to a reuse.
	if err = system.rootAlive(); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return errors.Join(fmt.Errorf("editor spawn process fence: %w", err), directKill())
	}
	if root.PID != rootPID || rootPID <= 1 || root.UID != system.uid || root.BirthSeconds == 0 && root.BirthFraction == 0 {
		return errors.Join(errors.New("editor root process identity unavailable"), directKill())
	}
	known := map[int]fileEditorProcessIdentity{rootPID: root}
	depths := map[int]int{rootPID: 0}
	stopped := map[int]bool{}
	send := func(p fileEditorProcessIdentity, sig syscall.Signal) error {
		current, present, e := system.read(p.PID)
		if e != nil {
			return e
		}
		if !present {
			return nil
		}
		if !p.same(current) {
			return fmt.Errorf("editor process identity changed for PID %d", p.PID)
		}
		e = system.signal(p.PID, sig)
		if errors.Is(e, syscall.ESRCH) {
			return nil
		}
		return e
	}
	var freezeError error
	freezeDeadline := system.now().Add(2 * time.Second)
	stop := func(p fileEditorProcessIdentity) error {
		current, present, e := system.read(p.PID)
		if e != nil {
			return e
		}
		if !present || !p.same(current) {
			return fmt.Errorf("editor process %d changed or exited before freeze", p.PID)
		}
		if e = system.signal(p.PID, syscall.SIGSTOP); e != nil {
			return e
		}
		// Record the request for failure cleanup; only return success once the
		// kernel metadata confirms the process cannot continue creating children.
		stopped[p.PID] = true
		for {
			current, present, e = system.read(p.PID)
			if e != nil {
				return e
			}
			if !present || !p.same(current) {
				return fmt.Errorf("editor process %d changed or exited while stopping", p.PID)
			}
			if current.Stopped {
				return nil
			}
			if !system.now().Before(freezeDeadline) {
				return fmt.Errorf("editor PID %d did not enter stopped state", p.PID)
			}
			system.pause(time.Millisecond)
		}
	}
	if err = stop(root); err != nil {
		freezeError = err
	}
	for freezeError == nil {
		if !system.now().Before(freezeDeadline) {
			freezeError = errors.New("editor descendants did not stabilize before cancellation")
			break
		}
		rows, e := system.snapshot()
		if e != nil {
			freezeError = e
			break
		}
		current := make(map[int]fileEditorProcessIdentity, len(rows))
		for _, p := range rows {
			current[p.PID] = p
		}
		for pid, p := range known {
			observed, present := current[pid]
			if stopped[pid] && (!present || !p.same(observed) || !observed.Stopped) {
				freezeError = fmt.Errorf("frozen editor identity %d changed during snapshot", pid)
				break
			}
		}
		if freezeError != nil {
			break
		}
		added := false
		for changed := true; changed; {
			changed = false
			for _, p := range rows {
				if p.PID <= 1 || p.UID != root.UID {
					continue
				}
				if _, ok := known[p.PID]; ok {
					continue
				}
				parent, ok := known[p.Parent]
				actualParent, present := current[p.Parent]
				if !ok || !present || !parent.same(actualParent) {
					continue
				}
				if p.BirthSeconds == 0 && p.BirthFraction == 0 {
					freezeError = errors.New("editor descendant birth identity unavailable")
					break
				}
				known[p.PID] = p
				depths[p.PID] = depths[p.Parent] + 1
				changed = true
				added = true
			}
			if freezeError != nil {
				break
			}
		}
		if freezeError != nil {
			break
		}
		// Parents first: a discovered child cannot spawn after its own stop.
		order := fileEditorProcessOrder(known, depths, false)
		for _, p := range order {
			if stopped[p.PID] {
				continue
			}
			if e = stop(p); e != nil {
				freezeError = e
				break
			}
		}
		if freezeError != nil {
			break
		}
		if !system.now().Before(freezeDeadline) {
			freezeError = errors.New("editor descendant freeze exceeded cancellation bound")
			break
		}
		if !added {
			break
		}
	}
	// Cancellation must not resume a parent's remaining user command just to
	// obtain a reap. Kill leaves first and root last; exec.Cmd retains root Wait.
	var killError error
	for _, p := range fileEditorProcessOrder(known, depths, true) {
		if e := send(p, syscall.SIGKILL); e != nil {
			killError = errors.Join(killError, e)
			// The exec.Cmd's own Process handle remains the root fallback if a
			// metadata read failed; do not leave that stopped child blocking Wait.
			if p.PID == rootPID {
				killError = errors.Join(killError, directKill())
			}
			// A failed kill must not leave a verified process indefinitely frozen.
			if stopped[p.PID] {
				killError = errors.Join(killError, send(p, syscall.SIGCONT))
			}
		}
	}
	joinDeadline := system.now().Add(2 * time.Second)
	for {
		var remaining []int
		var readError error
		for _, p := range known {
			current, present, e := system.read(p.PID)
			if e != nil {
				if readError == nil {
					readError = e
				}
				remaining = append(remaining, p.PID)
				continue
			}
			if present {
				remaining = append(remaining, p.PID)
				if !p.same(current) && readError == nil {
					readError = fmt.Errorf("editor PID %d changed identity during join", p.PID)
				}
			}
		}
		if len(remaining) == 0 {
			return errors.Join(freezeError, killError)
		}
		if !system.now().Before(joinDeadline) {
			sort.Ints(remaining)
			return errors.Join(freezeError, killError, readError, fmt.Errorf("editor cancellation could not join PIDs %v", remaining))
		}
		// Z is still present. In particular, do not pretend Linux PID 1 reaped it.
		system.pause(10 * time.Millisecond)
	}
}

func fileEditorProcessOrder(known map[int]fileEditorProcessIdentity, depths map[int]int, leavesFirst bool) []fileEditorProcessIdentity {
	out := make([]fileEditorProcessIdentity, 0, len(known))
	for _, p := range known {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if depths[out[i].PID] == depths[out[j].PID] {
			return out[i].PID < out[j].PID
		}
		if leavesFirst {
			return depths[out[i].PID] > depths[out[j].PID]
		}
		return depths[out[i].PID] < depths[out[j].PID]
	})
	return out
}
