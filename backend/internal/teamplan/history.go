package teamplan

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"regexp"
)

var archiveName = regexp.MustCompile(`(?i)^plan-[a-f0-9-]{36}\.json$`)

// HistoryForSession selects only plans belonging to the current incarnation.
// Invalid or stale archive records never become runtime launch authority.
func (s *Store) HistoryForSession(session string) ([]protocol.JSONObject, error) {
	current, e := s.FindForSession(session)
	if e != nil || current == nil {
		return nil, e
	}
	result := []protocol.JSONObject{current.Clone()}
	directory := filepath.Dir(s.Path(current.String("teamName")))
	entries, e := os.ReadDir(directory)
	if e != nil {
		return result, nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !archiveName.MatchString(entry.Name()) {
			continue
		}
		data, e := os.ReadFile(filepath.Join(directory, entry.Name()))
		if e != nil {
			continue
		}
		plan, e := ParseRecord(data)
		if e == nil && plan.String("incarnationId") == current.String("incarnationId") && plan.String("sessionId") == session {
			result = append(result, plan)
		}
	}
	return result, nil
}
