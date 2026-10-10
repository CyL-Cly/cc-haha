package imruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SessionEntry struct {
	SessionID string `json:"sessionId"`
	WorkDir   string `json:"workDir"`
	UpdatedAt int64  `json:"updatedAt"`
}
type SessionStore struct {
	Path      string
	NowMillis func() int64
	mu        sync.Mutex
}

func NewSessionStore(config string) *SessionStore {
	return &SessionStore{Path: filepath.Join(config, "adapter-sessions.json"), NowMillis: func() int64 { return time.Now().UnixMilli() }}
}
func (s *SessionStore) read() protocol.JSONObject {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return protocol.NewJSONObject()
	}
	result := object(protocol.DecodeJavaScriptUTF8(data))
	if !result.IsObject() {
		return protocol.NewJSONObject()
	}
	return result
}
func (s *SessionStore) Get(chat string) *SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.read().Get(chat)
	if value == nil || string(value) == "null" {
		return nil
	}
	var entry SessionEntry
	if json.Unmarshal(value, &entry) != nil {
		return nil
	}
	return &entry
}
func (s *SessionStore) write(data protocol.JSONObject) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0777); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", s.Path, s.NowMillis())
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, append(encoded, '\n'), 0666); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
func (s *SessionStore) Set(chat, id, work string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.read()
	data.Set(chat, raw(SessionEntry{id, work, s.NowMillis()}))
	return s.write(data)
}
func (s *SessionStore) Delete(chat string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.read()
	data.Delete(chat)
	return s.write(data)
}
func (s *SessionStore) DeleteBySessionID(id string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.read()
	removed := []string{}
	for _, property := range data.OwnProperties() {
		entry := object(property.Value)
		if entry.String("sessionId") != id {
			continue
		}
		var chat string
		_ = json.Unmarshal(property.NameJSON, &chat)
		removed = append(removed, chat)
		_ = data.DeletePropertyJSON(property.NameJSON)
	}
	if len(removed) == 0 {
		return removed, nil
	}
	return removed, s.write(data)
}
func (s *SessionStore) List() protocol.JSONObject { s.mu.Lock(); defer s.mu.Unlock(); return s.read() }
