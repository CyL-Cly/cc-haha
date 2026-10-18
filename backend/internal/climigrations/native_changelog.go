package climigrations

import (
	"encoding/json"
	"path/filepath"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) startNativeChangelog() (*ChangelogTask, error) {
	s.emit(Event{"kind": "changelogStart"})
	config, err := s.options.Global.Get()
	if err != nil {
		return nil, err
	}
	raw := append(json.RawMessage(nil), config.Get("cachedChangelog")...)
	task := &ChangelogTask{done: make(chan struct{})}
	if !truthy(raw) {
		close(task.done)
		return task, nil
	}
	native := s.native
	path := filepath.Join(s.options.ConfigHome, "cache", "changelog.md")
	completion, err := ownerasync.NewOwnerFuture[struct{}](native.options.Owner)
	if err != nil {
		return nil, err
	}
	finish := func(scope *cooperative.Scope) error {
		// Original's catch covers mkdir and wx writes. The config deletion is
		// after that catch and uses its current global writer/cache identity.
		err := native.options.Sources.Check(scope, native.options.Owner, s.options.Global)
		if err == nil {
			err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Delete("cachedChangelog")
				return &next, nil
			})
		}
		if err != nil {
			return completion.Reject(scope, err)
		}
		return completion.Resolve(scope, struct{}{})
	}
	_, err = completion.Then(native.scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		task.err = failure
		close(task.done)
		return nil
	})
	if err != nil {
		return nil, err
	}
	mkdir, err := native.options.MkdirAll(native.scope, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	_, err = mkdir.Then(native.scope, func(scope *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			return finish(scope)
		}
		units, failure := protocol.DecodeJavaScriptJSONString(raw)
		if failure != nil {
			return finish(scope)
		}
		pending, failure := native.options.WriteExclusive(scope, path, []byte(string(utf16.Decode(units))))
		if failure != nil {
			return completion.Reject(scope, failure)
		}
		_, failure = pending.Then(scope, func(next *cooperative.Scope, _ struct{}, _ error) error { return finish(next) })
		return failure
	})
	if err != nil {
		return nil, err
	}
	return task, nil
}
