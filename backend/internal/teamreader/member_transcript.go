package teamreader

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
)

func (s *Service) parsedDate(value string) float64 {
	n, valid, err := s.options.ParseDate(value)
	if err != nil || !valid {
		return math.NaN()
	}
	return n
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func (s *Service) resolveMemberName(config protocol.JSONObject, team, id string) (string, error) {
	if !strings.HasPrefix(string(config.Get("members")), "[") {
		return "", errors.New("Team config members are not an array")
	}
	for _, member := range rows(config.Get("members")) {
		if member.String("agentId") == id && member.String("name") != "" {
			return member.String("name"), nil
		}
	}
	name := strings.Split(id, "@")[0]
	for _, candidate := range s.inboxMembers(team) {
		if candidate == name {
			return name, nil
		}
	}
	if config.String("leadSessionId") != "" {
		for _, candidate := range s.subagentLastWrites(config.String("leadSessionId")) {
			if candidate.name == name {
				return name, nil
			}
		}
	}
	return "", nil
}
func (s *Service) findTranscript(id string) string {
	projects := filepath.Join(s.options.ConfigDir, "projects")
	entries, err := nativeDirectoryEntries(projects)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projects, entry.Name(), id+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
func subagentMetadataName(path string) string {
	data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
	if err != nil {
		return ""
	}
	name := obj(protocol.DecodeJavaScriptUTF8(data)).String("agentType")
	if protocol.TrimJavaScriptString(name) == "" {
		return ""
	}
	return name
}
func subagentHeadName(path string) string {
	head, err := readHead(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(head, "\n") {
		entry := obj([]byte(line))
		if name := entry.String("agentName"); protocol.TrimJavaScriptString(name) != "" {
			return name
		}
		if id := entry.String("agentId"); strings.Contains(id, "@") {
			return strings.Split(id, "@")[0]
		}
	}
	return ""
}
func (s *Service) belongsToIncarnation(path string, start, end float64) (bool, error) {
	if !finite(start) {
		return true, nil
	}
	head, err := readHead(path)
	if err != nil {
		return false, nil
	}
	first := math.NaN()
	for _, line := range strings.Split(head, "\n") {
		entry := obj([]byte(line))
		if stringRaw(entry.Get("timestamp")) {
			n := s.parsedDate(entry.String("timestamp"))
			if finite(n) {
				first = n
				break
			}
		}
	}
	if !finite(first) {
		info, err := os.Stat(path)
		if err != nil {
			return false, nil
		}
		if s.options.BirthtimeMs == nil {
			return false, &teamsapi.CapabilityError{Boundary: "native transcript birthtime producer"}
		}
		first = s.options.BirthtimeMs(path, info)
		if first == 0 {
			stat, err := localindex.NativeSnapshot(info)
			if err != nil {
				return false, nil
			}
			first = stat.CtimeMs
		}
	}
	return first >= start && (!finite(end) || first < end), nil
}

type transcriptMatch struct {
	path  string
	mtime float64
}

func sortedMatches(matches []transcriptMatch) []string {
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].mtime < matches[j].mtime || matches[i].mtime == matches[j].mtime && localeCompare(matches[i].path, matches[j].path) < 0
	})
	paths := []string{}
	for _, match := range matches {
		paths = append(paths, match.path)
	}
	return paths
}
func (s *Service) findSubagentTranscripts(lead, name string, start, end float64) ([]string, error) {
	projects := filepath.Join(s.options.ConfigDir, "projects")
	entries, err := nativeDirectoryEntries(projects)
	if err != nil {
		return []string{}, nil
	}
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		directory := filepath.Join(projects, project.Name(), lead, "subagents")
		files, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		matches := []transcriptMatch{}
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(directory, file.Name())
			if subagentMetadataName(path) != name && subagentHeadName(path) != name {
				continue
			}
			belongs, err := s.belongsToIncarnation(path, start, end)
			if err != nil {
				return nil, err
			}
			if !belongs {
				continue
			}
			info, err := os.Stat(path)
			if err == nil {
				matches = append(matches, transcriptMatch{path, float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6})
			}
		}
		if len(matches) > 0 {
			return sortedMatches(matches), nil
		}
	}
	return []string{}, nil
}
func teammateHeadIdentity(path string) (string, string) {
	head, err := readHead(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(head, "\n") {
		entry := obj([]byte(line))
		if nonblank(entry.Get("teamName")) && nonblank(entry.Get("agentName")) {
			return entry.String("teamName"), entry.String("agentName")
		}
	}
	return "", ""
}
func (s *Service) findTeammateTranscripts(team, name string, start, end float64) ([]string, error) {
	projects := filepath.Join(s.options.ConfigDir, "projects")
	entries, _ := nativeDirectoryEntries(projects)
	matches := []transcriptMatch{}
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		directory := filepath.Join(projects, project.Name())
		files, _ := nativeDirectoryEntries(directory)
		for _, file := range files {
			if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(directory, file.Name())
			t, n := teammateHeadIdentity(path)
			if t != team || n != name {
				continue
			}
			belongs, err := s.belongsToIncarnation(path, start, end)
			if err != nil {
				return nil, err
			}
			if !belongs {
				continue
			}
			info, err := os.Stat(path)
			if err == nil {
				matches = append(matches, transcriptMatch{path, float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6})
			}
		}
	}
	return sortedMatches(matches), nil
}
func physicalAgent(path string) string {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") || len(name) <= len("agent-.jsonl") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
}

func (s *Service) GetMemberTranscriptPage(ctx context.Context, team, id string, options protocol.JSONObject) (protocol.JSONObject, error) {
	if err := context.Cause(ctx); err != nil {
		return protocol.JSONObject{}, err
	}
	var configError error
	name, memberSession, lead := "", "", options.String("leadSessionId")
	start, end := math.NaN(), math.NaN()
	config, err := s.loadConfig(team)
	if err == nil {
		if requested := options.String("incarnationId"); requested != "" && requested != incarnation(config) {
			err = &teamsapi.APIError{Status: 404, Code: "NOT_FOUND", Message: "Team incarnation not found: " + requested}
		}
		if err == nil {
			name, err = s.resolveMemberName(config, team, id)
		}
		if err == nil {
			for _, member := range rows(config.Get("members")) {
				if member.String("agentId") == id {
					memberSession = member.String("sessionId")
					break
				}
			}
			if config.Get("leadSessionId") != nil && string(config.Get("leadSessionId")) != "null" {
				lead = config.String("leadSessionId")
			}
			start = numeric(config.Get("createdAt"))
		}
	}
	configError = err
	if name == "" && lead != "" {
		archive := s.ReadArchive(lead)
		lookup := protocol.NewJSONObject(field("teamName", team))
		lookup.Set("incarnationId", options.Get("incarnationId"))
		entry := s.SelectArchiveEntry(archive, lookup)
		if entry != nil {
			snapshots := rows(entry.Get("snapshots"))
			latest := lastSnapshot(*entry)
			archivedTeam := obj(latest.Get("team"))
			for i := len(snapshots) - 1; i >= 0 && name == ""; i-- {
				for _, member := range rows(obj(snapshots[i].Get("team")).Get("members")) {
					if member.String("agentId") == id || member.String("name") == id {
						name = member.String("name")
						memberSession = member.String("sessionId")
						break
					}
				}
			}
			if archivedTeam.Get("leadSessionId") != nil && string(archivedTeam.Get("leadSessionId")) != "null" {
				lead = archivedTeam.String("leadSessionId")
			}
			start = numeric(archivedTeam.Get("createdAt"))
			end = s.parsedDate(latest.String("deletedAt"))
			if archive != nil {
				for _, candidate := range rows(archive.Get("teams")) {
					if candidate.String("teamName") == team {
						n := numeric(obj(lastSnapshot(candidate).Get("team")).Get("createdAt"))
						if finite(n) && finite(start) && n > start && (!finite(end) || n < end) {
							end = n
						}
					}
				}
			}
		}
	}
	if name == "" {
		if configError != nil && lead == "" {
			return protocol.JSONObject{}, configError
		}
		return protocol.JSONObject{}, &teamsapi.APIError{Status: 404, Code: "NOT_FOUND", Message: "Team member not found: " + id + " in team " + team}
	}
	path, owner := "", ""
	if memberSession != "" {
		if s.options.FindSessionFile != nil {
			path, err = s.options.FindSessionFile(ctx, memberSession)
			if err != nil {
				return protocol.JSONObject{}, err
			}
		}
		if path == "" {
			path = s.findTranscript(memberSession)
		}
		if path != "" {
			belongs, failure := s.belongsToIncarnation(path, start, end)
			if failure != nil {
				return protocol.JSONObject{}, failure
			}
			if !belongs {
				path = ""
			}
		}
	}
	if path == "" && lead != "" {
		paths, failure := s.findSubagentTranscripts(lead, name, start, end)
		if failure != nil {
			return protocol.JSONObject{}, failure
		}
		if len(paths) == 1 {
			path, owner = paths[0], physicalAgent(paths[0])
		} else if len(paths) > 1 {
			files := []TranscriptFile{}
			for _, p := range paths {
				files = append(files, TranscriptFile{p, physicalAgent(p)})
			}
			return s.ParseTranscriptFiles(ctx, files, options)
		}
	}
	if path == "" {
		paths, failure := s.findTeammateTranscripts(team, name, start, end)
		if failure != nil {
			return protocol.JSONObject{}, failure
		}
		if len(paths) == 1 {
			path = paths[0]
		} else if len(paths) > 1 {
			files := []TranscriptFile{}
			for _, p := range paths {
				files = append(files, TranscriptFile{Path: p})
			}
			return s.ParseTranscriptFiles(ctx, files, options)
		}
	}
	if path == "" {
		page := s.ParseTranscriptBuffer(nil, protocol.NewJSONObject(), 0, nil, TranscriptProjection{})
		page.Set("signature", raw("missing"))
		return page, nil
	}
	owners := []string{}
	if owner != "" {
		owners = append(owners, owner)
	}
	projection := TranscriptProjection{OwnerAgentID: owner, OwnerAgentIDs: owners}
	if indexed := s.readIndexedTranscript(ctx, path, options, projection); indexed != nil {
		return *indexed, nil
	}
	return s.ParseTranscriptFiles(ctx, []TranscriptFile{{path, owner}}, options)
}
