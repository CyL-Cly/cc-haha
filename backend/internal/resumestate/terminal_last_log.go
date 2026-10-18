package resumestate

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/insights"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// BeginTerminalLastLog is getLastSessionLog's direct-file fallback, without
// loadInitialMessages, skill activation, plan copying or headless startup hooks.
func (s *Service) BeginTerminalLastLog(scope *cooperative.Scope, sid string) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if err := s.checkOwned(scope); err != nil {
		return nil, err
	}
	if !uuidName.MatchString(sid) {
		return nil, cooperative.ErrRestricted
	}
	out, err := ownerasync.NewOwnerFuture[protocol.JSONObject](s.owned.state.Owner())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.projectDir(), sid+".jsonl")
	read, err := s.beginSIDRead(scope, path)
	if err != nil {
		return nil, err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, t *sessionhistory.Transcript, failure error) error {
		if failure != nil {
			resolveOwned(out, next, protocol.JSONObject{}, failure)
			return nil
		}
		data := object(encode(t))
		if len(pairs(data.Get("messages"))) == 0 {
			resolveOwned(out, next, protocol.JSONObject{}, nil)
			return nil
		}
		// Source only primes an empty message UUID cache. The owned primitive shares
		// exactly that guard with normal recordTranscript; it cannot replace live IDs.
		if failure = s.owned.prime(next, sid, t); failure != nil {
			resolveOwned(out, next, protocol.JSONObject{}, failure)
			return nil
		}
		s.mu.Lock()
		if _, exists := s.state.SessionUUIDs[sid]; !exists {
			s.uuidTranscripts[sid] = t
			s.state.SessionUUIDs[sid] = []json.RawMessage{}
			for _, pair := range pairs(data.Get("messages")) {
				s.state.SessionUUIDs[sid] = append(s.state.SessionUUIDs[sid], pair[0])
			}
		}
		s.mu.Unlock()
		tip, failure := s.latest(data, false)
		if failure != nil || !tip.IsObject() {
			resolveOwned(out, next, protocol.JSONObject{}, failure)
			return nil
		}
		chain, messages, failure := s.options.Loader.BuildConversationChain(t, tip.Get("uuid"))
		if failure != nil {
			resolveOwned(out, next, protocol.JSONObject{}, failure)
			return nil
		}
		first, last := object(chain[0]), object(chain[len(chain)-1])
		log := protocol.NewJSONObject(protocol.JSONField{Name: "date", Value: last.Get("timestamp")}, protocol.JSONField{Name: "messages", Value: encode(messages)}, protocol.JSONField{Name: "fullPath", Value: quote(path)}, protocol.JSONField{Name: "value", Value: numeric(0)}, protocol.JSONField{Name: "created", Value: first.Get("timestamp")}, protocol.JSONField{Name: "modified", Value: last.Get("timestamp")})
		if failure = s.terminalLogPresentation(&log, chain); failure != nil {
			resolveOwned(out, next, protocol.JSONObject{}, failure)
			return nil
		}
		for _, name := range []string{"isSidechain", "teamName", "agentName"} {
			log.Set(name, first.Get(name))
		}
		setting, _ := t.MetadataValue("agentSettings", quote(sid))
		log.Set("agentSetting", setting)
		log.Set("leafUuid", last.Get("uuid"))
		summary, _ := t.MetadataValue("summaries", tip.Get("uuid"))
		log.Set("summary", summary)
		for _, p := range [][2]string{{"customTitle", "customTitles"}, {"tag", "tags"}} {
			value, _ := t.MetadataValue(p[1], tip.Get("sessionId"))
			log.Set(p[0], value)
		}
		log.Set("fileHistorySnapshots", encode(s.snapshots(data, chain)))
		log.Set("attributionSnapshots", encode(metadataValues(data, "attributionSnapshots")))
		sessionFields(&log, data, quote(sid))
		log.Set("gitBranch", last.Get("gitBranch"))
		log.Set("projectPath", first.Get("cwd"))
		worktree, _ := t.MetadataValue("worktreeStates", quote(sid))
		log.Set("worktreeSession", worktree)
		resolveOwned(out, next, log, nil)
		return nil
	})
	return out, err
}

func (s *Service) terminalLogPresentation(log *protocol.JSONObject, chain []json.RawMessage) error {
	rows := make([]protocol.JSONObject, len(chain))
	for i, v := range chain {
		rows[i] = object(v)
	}
	var names map[string]bool
	first, err := insights.FirstMeaningfulPrompt(rows, func(name string) (bool, error) {
		if names == nil {
			var e error
			names, e = commandnames.Set(s.options.Environment)
			if e != nil {
				return false, e
			}
		}
		return names[name], nil
	})
	if err != nil {
		return err
	}
	if first == nil {
		first = quote("No prompt")
	}
	log.Set("firstPrompt", first)
	count := 0
	for _, r := range rows {
		body, _ := r.Object("message")
		content := body.Get("content")
		if r.String("type") == "user" && !truthy(r.Get("isMeta")) {
			if len(content) > 0 && content[0] == '"' && terminalVisibleText(content) {
				count++
				continue
			}
			blocks, _ := array(content)
			for _, v := range blocks {
				b := object(v)
				if b.String("type") == "image" || b.String("type") == "text" || b.String("type") == "document" {
					count++
					break
				}
			}
		} else if r.String("type") == "assistant" {
			blocks, _ := array(content)
			for _, v := range blocks {
				b := object(v)
				if b.String("type") == "text" && terminalVisibleText(b.Get("text")) {
					count++
					break
				}
			}
		}
	}
	log.Set("messageCount", numeric(float64(count)))
	return nil
}

func terminalVisibleText(value json.RawMessage) bool {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return false
	}
	for _, unit := range units {
		if !protocol.IsJavaScriptWhitespace(rune(unit)) {
			return true
		}
	}
	return false
}
