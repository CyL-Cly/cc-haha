package clibootstrap

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func remoteAgentInaccessible(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOTDIR)
}

func (m *MainPrint) nativeRemoteAgentsDirectory(scope *cooperative.Scope) (string, error) {
	if m == nil || scope == nil || m.options.Native == nil || !m.options.Native.NativeProcess || !scope.BelongsTo(m.Owner) || m.state == nil || m.Controller == nil || m.closed || m.shuttingDown {
		return "", cooperative.ErrRestricted
	}
	snapshot := m.state.Snapshot()
	project := ""
	if snapshot.SessionProjectDir != nil {
		project = *snapshot.SessionProjectDir
	}
	if project == "" {
		config := m.nativeOpenAIEnvironment()["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(m.options.Native.Home, ".claude")
		} else if !filepath.IsAbs(config) {
			config = filepath.Join(snapshot.OriginalCWD, config)
		}
		project = filepath.Join(config, "projects", projectpath.Sanitize(snapshot.OriginalCWD))
	}
	return filepath.Join(project, snapshot.SessionID, "remote-agents"), nil
}

type nativeRemoteAgentMetadataRead struct {
	main    *MainPrint
	dir     string
	entries []fs.DirEntry
	index   int
	values  []protocol.JSONObject
	future  *ownerasync.OwnerFuture[[]protocol.JSONObject]
}

func (m *MainPrint) beginNativeRemoteAgentMetadata(scope *cooperative.Scope) (*ownerasync.OwnerFuture[[]protocol.JSONObject], error) {
	dir, err := m.nativeRemoteAgentsDirectory(scope)
	if err != nil {
		return nil, err
	}
	result, err := ownerasync.NewOwnerFuture[[]protocol.JSONObject](m.Owner)
	if err != nil {
		return nil, err
	}
	job := &nativeRemoteAgentMetadataRead{main: m, dir: dir, values: []protocol.JSONObject{}, future: result}
	n := m.options.Native
	read, err := nativeSkillWork(m, scope, func() ([]fs.DirEntry, error) {
		if err := n.check("readdir", dir); err != nil {
			return nil, err
		}
		// Keep the real directory enumeration order. os.ReadDir sorts names,
		// while Source's readdir({withFileTypes:true}) preserves its OS result.
		file, err := os.Open(dir)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return file.ReadDir(-1)
	})
	if err != nil {
		return nil, err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, entries []fs.DirEntry, failure error) error {
		if remoteAgentInaccessible(failure) {
			return result.Resolve(next, []protocol.JSONObject{})
		}
		if failure != nil {
			return result.Reject(next, failure)
		}
		job.entries = entries
		return job.next(next)
	})
	return result, err
}

func (job *nativeRemoteAgentMetadataRead) next(scope *cooperative.Scope) error {
	for job.index < len(job.entries) {
		entry := job.entries[job.index]
		job.index++
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		path := filepath.Join(job.dir, entry.Name())
		read, err := nativeSkillWork(job.main, scope, func() ([]byte, error) { return job.main.options.Native.readFile(path) })
		if err != nil {
			return job.future.Reject(scope, err)
		}
		_, err = read.Then(scope, func(next *cooperative.Scope, data []byte, failure error) error {
			if failure == nil {
				var value protocol.JSONObject
				failure = value.UnmarshalJSON(data)
				if failure == nil {
					job.values = append(job.values, value)
				}
			}
			if failure != nil {
				job.main.nativeRemoteAgentDebug(next, "listRemoteAgentMetadata: skipping "+entry.Name()+": "+failure.Error())
			}
			return job.next(next)
		})
		return err
	}
	return job.future.Resolve(scope, job.values)
}

func (m *MainPrint) nativeRemoteAgentDebug(scope *cooperative.Scope, text string) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) {
		return
	}
	if err := m.initializeNativeErrorLog(); err == nil && m.errorLogSink != nil {
		_ = m.errorLogSink.Debug(text, "DEBUG")
	}
}

// Source computes the metadata target at the delete call, using the then-live
// SID/project. It does not freeze the directory from the earlier restore read.
func (m *MainPrint) removeNativeRemoteAgentMetadata(scope *cooperative.Scope, taskID string) error {
	dir, err := m.nativeRemoteAgentsDirectory(scope)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "remote-agent-"+taskID+".meta.json")
	n := m.options.Native
	removed, err := nativeSkillWork(m, scope, func() (struct{}, error) {
		if err := n.check("unlink", path); err != nil {
			return struct{}{}, err
		}
		err := os.Remove(path)
		if remoteAgentInaccessible(err) {
			err = nil
		}
		return struct{}{}, err
	})
	if err != nil {
		return err
	}
	_, err = removed.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			m.nativeRemoteAgentDebug(next, "removeRemoteAgentMetadata failed: "+failure.Error())
		}
		return nil
	})
	return err
}
