package teamreader

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func watchIdentity(name string, config protocol.JSONObject) (protocol.JSONObject, bool) {
	if !config.IsObject() {
		return protocol.NewJSONObject(), false
	}
	created, valid := watchNumber(config.Get("createdAt"))
	if !valid {
		return protocol.NewJSONObject(), false
	}
	if math.IsNaN(created) || math.IsInf(created, 0) {
		return protocol.NewJSONObject(), false
	}
	if v := config.String("name"); v != "" {
		name = v
	}
	lead := config.String("leadSessionId")
	id := protocol.NewJSONObject(field("teamName", name), field("createdAt", created))
	if lead != "" {
		id.Set("leadSessionId", raw(lead))
	}
	id.Set("incarnationId", raw(hash(protocol.StringifyValidatedJavaScriptJSON(raw([]any{name, lead, created})))))
	return id, true
}

func watchNumber(data json.RawMessage) (number float64, valid bool) {
	// Number can throw for an object whose conversion methods are non-callable;
	// Source readTeamIdentity catches that failure and omits its identity.
	defer func() {
		if recover() != nil {
			valid = false
		}
	}()
	var value any
	if data == nil || json.Unmarshal(data, &value) != nil {
		return 0, false
	}
	return goja.New().ToValue(value).ToFloat(), true
}

func watchMembers(config protocol.JSONObject) ([]protocol.JSONObject, error) {
	if !config.IsObject() {
		return nil, errors.New("Team config is not an object")
	}
	members := []json.RawMessage{}
	if json.Unmarshal(config.Get("members"), &members) != nil {
		return []protocol.JSONObject{}, nil
	}
	out := []protocol.JSONObject{}
	for _, data := range members {
		member := obj(data)
		if !member.IsObject() {
			if string(data) == "null" {
				return nil, errors.New("Invalid Team member")
			}
			member = protocol.NewJSONObject()
		}
		id := member.Get("agentId")
		if !jsTruthy(id) {
			id = raw("")
		}
		role := member.Get("name")
		if !jsTruthy(role) {
			role = member.Get("agentType")
		}
		if !jsTruthy(role) {
			role = raw("member")
		}
		status := "running"
		lastError, retry := memberFailure(member)
		active := string(member.Get("isActive"))
		if active == "false" {
			status = "idle"
		}
		if lastError != nil && retry == nil {
			status = "error"
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: id}, protocol.JSONField{Name: "role", Value: role}, field("status", status))
		row.Set("lastError", raw(nil))
		row.Set("autoRetry", raw(nil))
		if lastError != nil {
			row.Set("lastError", lastError)
		}
		if retry != nil {
			row.Set("autoRetry", retry)
		}
		if string(member.Get("terminated")) == "true" {
			row.Set("activity", raw("stopped"))
		} else if active == "true" {
			row.Set("activity", raw("active"))
		} else if active == "false" {
			row.Set("activity", raw("idle"))
		}
		if task := member.Get("currentTask"); jsTruthy(task) {
			row.Set("currentTask", task)
		}
		out = append(out, row)
	}
	return out, nil
}

func (w *Watcher) watchDiscoveredMembers(name string, config protocol.JSONObject) []protocol.JSONObject {
	known := map[string]bool{}
	for _, m := range rows(config.Get("members")) {
		if value := m.Get("name"); len(value) > 0 && value[0] == '"' {
			known[m.String("name")] = true
		}
	}
	extra := []protocol.JSONObject{}
	if files, err := nativeDirectoryEntries(filepath.Join(w.reader.options.ConfigDir, "teams", name, "inboxes")); err == nil {
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			n := strings.TrimSuffix(file.Name(), ".json")
			if n != "team-lead" && !known[n] {
				extra = append(extra, protocol.NewJSONObject(field("agentId", n+"@"+name), field("role", n), field("status", "running"), field("activity", "unknown")))
			}
		}
	}
	team := config.String("name")
	if config.Get("name") == nil || len(config.Get("name")) == 0 || config.Get("name")[0] != '"' {
		team = "team"
	}
	if session := config.String("leadSessionId"); session != "" {
		for _, found := range w.watchSubagentNames(session) {
			if !known[found] {
				extra = append(extra, protocol.NewJSONObject(field("agentId", found+"@"+team), field("role", found), field("status", "running"), field("activity", "unknown")))
			}
		}
	}
	return extra
}

// Both original directory reads preserve native filesystem order. The
// workbench reader's mtime aggregation is deliberately not this cheap poll.
func (w *Watcher) watchSubagentNames(session string) []string {
	root := filepath.Join(w.reader.options.ConfigDir, "projects")
	projects, err := nativeDirectoryEntries(root)
	if err != nil {
		return nil
	}
	names := []string{}
	seen := map[string]bool{}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		directory := filepath.Join(root, project.Name(), session, "subagents")
		files, e := nativeDirectoryEntries(directory)
		if e != nil {
			continue
		}
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(directory, file.Name())
			name := ""
			if bytes, e := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); e == nil {
				metadata := obj(protocol.DecodeJavaScriptUTF8(bytes))
				candidate := metadata.String("agentType")
				if protocol.TrimJavaScriptString(candidate) != "" {
					name = candidate
				}
			}
			if name == "" {
				if head, e := readHead(path); e == nil {
					for _, line := range strings.Split(head, "\n") {
						entry := obj([]byte(line))
						candidate := entry.String("agentName")
						if protocol.TrimJavaScriptString(candidate) != "" {
							name = candidate
							break
						}
						if id := entry.String("agentId"); strings.Contains(id, "@") {
							name = strings.Split(id, "@")[0]
							break
						}
					}
				}
			}
			if name != "" && name != "team-lead" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

var watchAgentIDs = regexp.MustCompile(`"agentId"\s*:\s*"([^"]+)"`)
var watchNames = regexp.MustCompile(`"(?:agentType|name)"\s*:\s*"([^"]+)"`)
var watchActive = regexp.MustCompile(`"isActive"\s*:\s*(true|false)`)

func watchPartialMembers(content string) []protocol.JSONObject {
	ids, names, active := watchAgentIDs.FindAllStringSubmatch(content, -1), watchNames.FindAllStringSubmatch(content, -1), watchActive.FindAllStringSubmatch(content, -1)
	out := []protocol.JSONObject{}
	for i, id := range ids {
		role, status := "member", "running"
		if i < len(names) {
			role = names[i][1]
		}
		if i < len(active) && active[i][1] == "false" {
			status = "idle"
		}
		out = append(out, protocol.NewJSONObject(field("agentId", id[1]), field("role", role), field("status", status)))
	}
	return out
}

func watchDirectoryHash(configDir, name, content string) string {
	h := sha256.New()
	h.Write([]byte(content))
	for _, directory := range []string{filepath.Join(configDir, "tasks", name), filepath.Join(configDir, "teams", name, "inboxes")} {
		files, e := os.ReadDir(directory)
		if e != nil {
			h.Write([]byte("\x00missing"))
			continue
		}
		sort.Slice(files, func(i, j int) bool {
			left, right := utf16.Encode([]rune(files[i].Name())), utf16.Encode([]rune(files[j].Name()))
			for n := 0; n < len(left) && n < len(right); n++ {
				if left[n] != right[n] {
					return left[n] < right[n]
				}
			}
			return len(left) < len(right)
		})
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			h.Write([]byte("\x00" + f.Name() + "\x00"))
			b, e := os.ReadFile(filepath.Join(directory, f.Name()))
			if e != nil {
				b = []byte("unreadable")
			}
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
