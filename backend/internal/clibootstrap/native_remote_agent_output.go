package clibootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Source DiskTaskOutput's append queue belongs to one task and one Owner.
// Each real drain releases its chunks before native append; eviction retains
// the physical output file and waits for the already admitted drain.
type nativeRemoteAgentOutput struct {
	main                      *MainPrint
	path                      string
	queue                     []byte
	units                     uint64
	capped, draining, evicted bool
	flush                     *ownerasync.OwnerFuture[struct{}]
}

func (m *MainPrint) initNativeRemoteAgentOutput(scope *cooperative.Scope, path string) error {
	n := m.options.Native
	work, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		if err := n.check("mkdir", filepath.Dir(path)); err != nil {
			return struct{}{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			return struct{}{}, err
		}
		if err := n.check("open", path); err != nil {
			return struct{}{}, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if errors.Is(err, os.ErrExist) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, file.Close()
	})
	if err != nil {
		return err
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			m.nativeRemoteAgentDebug(next, "initTaskOutput failed: "+failure.Error())
		}
		return nil
	})
	return err
}

func (out *nativeRemoteAgentOutput) append(scope *cooperative.Scope, value string) error {
	if out.capped {
		return nil
	}
	out.units += uint64(len(utf16.Encode([]rune(value))))
	if out.units > 5*1024*1024*1024 {
		out.capped = true
		value = "\n[output truncated: exceeded 5GB disk cap]\n"
	}
	out.queue = append(out.queue, []byte(value)...)
	if out.draining {
		return nil
	}
	var err error
	out.flush, err = ownerasync.NewOwnerFuture[struct{}](out.main.Owner)
	if err != nil {
		return err
	}
	out.draining = true
	return out.drain(scope)
}

func (out *nativeRemoteAgentOutput) drain(scope *cooperative.Scope) error {
	data := out.queue
	out.queue = nil
	n := out.main.options.Native
	path := out.path
	work, err := nativeSkillWork(out.main, scope, func() (struct{}, error) {
		if err := n.check("mkdir", filepath.Dir(path)); err != nil {
			return struct{}{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			return struct{}{}, err
		}
		if err := n.check("appendFile", path); err != nil {
			return struct{}{}, err
		}
		file, err := openNativeRemoteAgentOutput(path)
		if err != nil {
			return struct{}{}, err
		}
		_, writeErr := file.Write(data)
		return struct{}{}, errors.Join(writeErr, file.Close())
	})
	if err != nil {
		out.draining = false
		return out.flush.Reject(scope, err)
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			out.queue = nil
			out.main.nativeRemoteAgentDebug(next, "DiskTaskOutput drain failed: "+failure.Error())
		}
		if len(out.queue) != 0 {
			return out.drain(next)
		}
		out.draining = false
		// Source catches native write errors and settles its flush obligation.
		return out.flush.Resolve(next, struct{}{})
	})
	return err
}
