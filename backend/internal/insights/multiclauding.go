package insights

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ParseDate func(string) (float64, error)

// MultiClauding ports the original sliding-window s1→s2→s1 rule. Invalid Date
// values remain NaN observations, matching Source rather than disappearing.
func MultiClauding(sessions []protocol.JSONObject, parse ParseDate) (protocol.JSONObject, error) {
	type message struct {
		ts float64
		id string
	}
	all := []message{}
	for _, session := range sessions {
		var timestamps []string
		if err := json.Unmarshal(session.Get("user_message_timestamps"), &timestamps); err != nil {
			return protocol.JSONObject{}, err
		}
		for _, stamp := range timestamps {
			ts, err := parse(stamp)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			all = append(all, message{ts, session.String("session_id")})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ts-all[j].ts < 0 })
	pairs, during := map[string]bool{}, map[string]bool{}
	last := map[string]int{}
	start := 0
	key := func(m message) string {
		if math.IsNaN(m.ts) {
			return "NaN:" + m.id
		}
		quoted, _ := protocol.JavaScriptStringJSON(number(m.ts))
		var ts string
		_ = json.Unmarshal(quoted, &ts)
		return ts + ":" + m.id
	}
	for i, msg := range all {
		for start < i && msg.ts-all[start].ts > 30*60000 {
			old := all[start]
			if index, exists := last[old.id]; exists && index == start {
				delete(last, old.id)
			}
			start++
		}
		if prev, exists := last[msg.id]; exists {
			for j := prev + 1; j < i; j++ {
				between := all[j]
				if between.id != msg.id {
					ids := []string{msg.id, between.id}
					sort.Slice(ids, func(a, b int) bool { return compare(ids[a], ids[b]) < 0 })
					pairs[strings.Join(ids, ":")] = true
					during[key(all[prev])] = true
					during[key(between)] = true
					during[key(msg)] = true
					break
				}
			}
		}
		last[msg.id] = i
	}
	involved := map[string]bool{}
	for pair := range pairs {
		ids := strings.Split(pair, ":")
		for i := 0; i < 2 && i < len(ids); i++ {
			if ids[i] != "" {
				involved[ids[i]] = true
			}
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "overlap_events", Value: raw(len(pairs))}, protocol.JSONField{Name: "sessions_involved", Value: raw(len(involved))}, protocol.JSONField{Name: "user_messages_during", Value: raw(len(during))}), nil
}
