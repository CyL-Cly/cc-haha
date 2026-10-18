package climigrations

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"unicode/utf16"
)

func (s *Service) startChangelog() (*ChangelogTask, error) {
	if s.native != nil {
		return s.startNativeChangelog()
	}
	s.emit(Event{"kind": "changelogStart"})
	config, err := s.options.Global.Get()
	if err != nil {
		return nil, err
	}
	raw := config.Get("cachedChangelog")
	task := &ChangelogTask{done: make(chan struct{})}
	if !truthy(raw) {
		close(task.done)
		return task, nil
	}
	// Capture before the first await; removal rereads under the real config lock.
	raw = append(json.RawMessage(nil), raw...)
	go func() {
		defer close(task.done)
		path := filepath.Join(s.options.ConfigHome, "cache", "changelog.md")
		if err := s.options.FS.MkdirAll(filepath.Dir(path)); err == nil {
			s.emit(Event{"kind": "asyncWrite", "path": path, "value": json.RawMessage(protocol.StringifyValidatedJavaScriptJSON(raw)), "options": map[string]any{"encoding": "utf-8", "flag": "wx"}})
			if units, e := protocol.DecodeJavaScriptJSONString(raw); e == nil {
				_ = s.options.FS.WriteExclusive(path, []byte(string(utf16.Decode(units))))
			}
		}
		// Source's mkdir/write catch intentionally ignores ordinary write failures
		// (including existing files and invalid non-string data), then drops config.
		task.err = s.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Delete("cachedChangelog")
			return &next, nil
		})
	}()
	return task, nil
}
