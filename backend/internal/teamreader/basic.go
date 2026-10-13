// Package teamreader reads the original CLI-owned Team files. It does not
// create sessions, synthesize members or approve a durable draft.
package teamreader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	ConfigDir       string
	Tasks           *taskstore.Service
	Mailbox         *teamstore.Mailbox
	Now             func() time.Time
	FindSessionFile func(context.Context, string) (string, error)
	LocalIndex      *localindex.Coordinator
	HistoryBudget   *sessionstore.Budget
	BirthtimeMs     func(string, os.FileInfo) float64
	ParseDate       func(string) (float64, bool, error)
	CleanupTeam     func(context.Context, string, *taskstore.Service) error
}
type Service struct {
	options           Options
	archiveMu         sync.Mutex
	archiveLocks      map[string]*sync.Mutex
	projectionMu      sync.Mutex
	projections       map[string]*teamProjectionState
	projectionOrder   []string
	projectionPending map[string]*projectionCall
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.Tasks == nil || o.Mailbox == nil || o.Now == nil {
		return nil, errors.New("Team reader requires actual private config/task/mailbox/clock producers")
	}
	if o.LocalIndex != nil && o.HistoryBudget == nil {
		return nil, errors.New("Team indexed history requires its shared admission budget")
	}
	if o.ParseDate == nil {
		o.ParseDate = activitystatsapi.NewIndexDateParser(time.Local).Parse
	}
	return &Service{options: o, archiveLocks: map[string]*sync.Mutex{}, projections: map[string]*teamProjectionState{}, projectionPending: map[string]*projectionCall{}}, nil
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func obj(data json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(data)
	return v
}
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: raw(v)} }
func rows(data json.RawMessage) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	_ = json.Unmarshal(data, &out)
	return out
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (s *Service) teamDirectory(name string) string {
	return filepath.Join(s.options.ConfigDir, "teams", name)
}
func (s *Service) loadConfig(name string) (protocol.JSONObject, error) {
	data, err := os.ReadFile(filepath.Join(s.teamDirectory(name), "config.json"))
	if err == nil {
		var value protocol.JSONObject
		err = value.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data))
		if err == nil {
			return value, nil
		}
	}
	return protocol.JSONObject{}, &teamsapi.APIError{Status: 404, Code: "NOT_FOUND", Message: "Team not found: " + name}
}
func (s *Service) inboxMembers(name string) []string {
	entries, err := os.ReadDir(filepath.Join(s.teamDirectory(name), "inboxes"))
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			if name != "team-lead" {
				names = append(names, name)
			}
		}
	}
	return names
}
func incarnation(config protocol.JSONObject) string {
	lead := config.Get("leadSessionId")
	if lead == nil || string(lead) == "null" {
		lead = raw("")
	}
	var created float64
	if json.Unmarshal(config.Get("createdAt"), &created) != nil || math.IsInf(created, 0) || math.IsNaN(created) {
		created = 0
	}
	return hash(protocol.StringifyValidatedJavaScriptJSON(raw([]json.RawMessage{config.Get("name"), lead, raw(created)})))
}
func summary(config protocol.JSONObject) (protocol.JSONObject, error) {
	var members []protocol.JSONObject
	if json.Unmarshal(config.Get("members"), &members) != nil || string(config.Get("members")) == "null" {
		return protocol.JSONObject{}, errors.New("Team config members are not an array")
	}
	active := 0
	for _, member := range members {
		if member.Get("isActive") == nil || string(member.Get("isActive")) == "true" {
			active++
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: config.Get("name")}, protocol.JSONField{Name: "description", Value: config.Get("description")}, protocol.JSONField{Name: "createdAt", Value: config.Get("createdAt")}, field("incarnationId", incarnation(config)), field("memberCount", len(members)), field("activeMemberCount", active)), nil
}
func (s *Service) ListTeams(ctx context.Context) (json.RawMessage, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.options.ConfigDir, "teams")
	if _, err := os.Stat(dir); err != nil {
		return raw([]protocol.JSONObject{}), nil
	}
	entries, err := nativeDirectoryEntries(dir)
	if err != nil {
		return nil, err
	}
	result := []protocol.JSONObject{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		config, err := s.loadConfig(entry.Name())
		if err != nil {
			continue
		}
		v, err := summary(config)
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, member := range rows(config.Get("members")) {
			names[member.String("name")] = true
		}
		extra := 0
		for _, name := range s.inboxMembers(entry.Name()) {
			if !names[name] {
				extra++
			}
		}
		var count, active int
		_ = json.Unmarshal(v.Get("memberCount"), &count)
		_ = json.Unmarshal(v.Get("activeMemberCount"), &active)
		v.Set("memberCount", raw(count+extra))
		v.Set("activeMemberCount", raw(active+extra))
		result = append(result, v)
	}
	return raw(result), nil
}

type lastWrite struct {
	name   string
	millis float64
}

func (s *Service) subagentLastWrites(session string) []lastWrite {
	projects := filepath.Join(s.options.ConfigDir, "projects")
	entries, err := nativeDirectoryEntries(projects)
	if err != nil {
		return []lastWrite{}
	}
	result := []lastWrite{}
	indices := map[string]int{}
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		dir := filepath.Join(projects, project.Name(), session, "subagents")
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, file.Name())
			name := ""
			if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
				v := obj(protocol.DecodeJavaScriptUTF8(data))
				if candidate := v.String("agentType"); protocol.TrimJavaScriptString(candidate) != "" {
					name = candidate
				}
			}
			if name == "" {
				if head, err := readHead(path); err == nil {
					for _, line := range strings.Split(head, "\n") {
						v := obj([]byte(line))
						if candidate := v.String("agentName"); protocol.TrimJavaScriptString(candidate) != "" {
							name = candidate
							break
						}
						if id := v.String("agentId"); strings.Contains(id, "@") {
							name = strings.Split(id, "@")[0]
							break
						}
					}
				}
			}
			if name == "" || name == "team-lead" {
				continue
			}
			millis := 0.0
			if stat, err := os.Stat(path); err == nil {
				millis = float64(stat.ModTime().UnixNano()) / 1e6
			}
			if index, ok := indices[name]; ok {
				result[index].millis = math.Max(result[index].millis, millis)
			} else {
				indices[name] = len(result)
				result = append(result, lastWrite{name: name, millis: math.Max(0, millis)})
			}
		}
	}
	return result
}

// Bun's node:fs withFileTypes branch retains the filesystem directory stream;
// Go os.ReadDir sorts it. Membership insertion order is externally visible.
func nativeDirectoryEntries(path string) ([]os.DirEntry, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}
func readHead(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]byte, 8192)
	n, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(protocol.DecodeJavaScriptUTF8(buffer[:n])), nil
}
func deriveActivity(active json.RawMessage, last *float64, now float64) string {
	if string(active) == "true" {
		return "active"
	}
	if string(active) == "false" {
		return "idle"
	}
	if last == nil {
		return "unknown"
	}
	if now-*last < 15000 {
		return "active"
	}
	return "idle"
}
func (s *Service) GetTeam(ctx context.Context, name string) (protocol.JSONObject, error) {
	if err := context.Cause(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	config, err := s.loadConfig(name)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	out, err := summary(config)
	if err != nil {
		return out, err
	}
	writes := []lastWrite{}
	if config.String("leadSessionId") != "" {
		writes = s.subagentLastWrites(config.String("leadSessionId"))
	}
	last := map[string]float64{}
	for _, write := range writes {
		last[write.name] = write.millis
	}
	now := float64(s.options.Now().UnixNano()) / 1e6
	activity := func(active json.RawMessage, name string) string {
		var value *float64
		if n, ok := last[name]; ok {
			value = &n
		}
		return deriveActivity(active, value, now)
	}
	members := []protocol.JSONObject{}
	names := map[string]bool{}
	baseCWD := ""
	for i, member := range rows(config.Get("members")) {
		names[member.String("name")] = true
		if i == 0 {
			baseCWD = member.String("cwd")
		}
		item := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: member.Get("agentId")}, protocol.JSONField{Name: "name", Value: member.Get("name")}, protocol.JSONField{Name: "agentType", Value: member.Get("agentType")})
		if member.String("model") != "" {
			item.Set("model", member.Get("model"))
		}
		for _, key := range []string{"providerId", "providerName", "effortLevel", "color", "backendType"} {
			item.Set(key, member.Get(key))
		}
		status := "running"
		lastError, retry := memberFailure(member)
		if string(member.Get("isActive")) == "false" {
			status = "idle"
		}
		if lastError != nil && retry == nil {
			status = "failed"
		}
		item.Set("status", raw(status))
		item.Set("activity", raw(activity(member.Get("isActive"), member.String("name"))))
		if string(member.Get("terminated")) == "true" {
			item.Set("activity", raw("stopped"))
		}
		if lastError != nil {
			item.Set("lastError", lastError)
		}
		if retry != nil {
			item.Set("autoRetry", retry)
		}
		for _, key := range []string{"joinedAt", "cwd", "sessionId"} {
			item.Set(key, member.Get(key))
		}
		members = append(members, item)
	}
	discovered := map[string]bool{}
	for _, inbox := range s.inboxMembers(name) {
		if names[inbox] {
			continue
		}
		members = append(members, protocol.NewJSONObject(field("agentId", inbox+"@"+name), field("name", inbox), field("agentType", "general-purpose"), field("status", "running"), field("activity", activity(nil, inbox)), protocol.JSONField{Name: "joinedAt", Value: config.Get("createdAt")}, field("cwd", baseCWD)))
		discovered[inbox] = true
	}
	for _, write := range writes {
		if names[write.name] || discovered[write.name] {
			continue
		}
		members = append(members, protocol.NewJSONObject(field("agentId", write.name+"@"+name), field("name", write.name), field("status", "running"), field("activity", activity(nil, write.name)), protocol.JSONField{Name: "joinedAt", Value: config.Get("createdAt")}, field("cwd", baseCWD)))
	}
	active := 0
	for _, member := range members {
		if member.String("status") == "running" {
			active++
		}
	}
	out.Set("leadAgentId", config.Get("leadAgentId"))
	out.Set("leadSessionId", config.Get("leadSessionId"))
	out.Set("memberCount", raw(len(members)))
	out.Set("activeMemberCount", raw(active))
	out.Set("members", raw(members))
	return out, nil
}
func (s *Service) SendMemberMessage(ctx context.Context, team, id string, content json.RawMessage) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if _, err := protocol.DecodeJavaScriptJSONString(content); err != nil {
		return errors.New("content.trim is not a function")
	}
	text := protocol.TrimJavaScriptString(obj(raw(map[string]json.RawMessage{"content": content})).String("content"))
	if text == "" {
		return &teamsapi.APIError{Status: 400, Code: "BAD_REQUEST", Message: "content (string) is required in request body"}
	}
	config, err := s.loadConfig(team)
	if err != nil {
		return err
	}
	recipient := ""
	for _, member := range rows(config.Get("members")) {
		if member.String("agentId") == id && member.String("name") != "" {
			recipient = member.String("name")
			break
		}
	}
	parsedName := strings.Split(id, "@")[0]
	if recipient == "" {
		for _, name := range s.inboxMembers(team) {
			if name == parsedName {
				recipient = parsedName
				break
			}
		}
	}
	if recipient == "" && config.String("leadSessionId") != "" {
		for _, write := range s.subagentLastWrites(config.String("leadSessionId")) {
			if write.name == parsedName {
				recipient = parsedName
				break
			}
		}
	}
	if recipient == "" {
		return &teamsapi.APIError{Status: 404, Code: "NOT_FOUND", Message: "Team member not found: " + id + " in team " + team}
	}
	return s.options.Mailbox.Write(recipient, team, protocol.NewJSONObject(field("from", "user"), field("text", text), field("timestamp", s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"))))
}
