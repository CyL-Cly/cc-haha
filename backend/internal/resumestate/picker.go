package resumestate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/commandnames"
	"github.com/NanmiCoder/cc-haha/backend/internal/insights"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type PickerStat struct {
	SID, Path, Project string
	Modified, Birth    float64
	Size               int64
}
type PickerFiles interface{ ContinueFiles }
type PickerPage struct {
	Logs      []protocol.JSONObject
	NextIndex int
}

func ReadPickerStats(files PickerFiles, stat func(string) (Stat, error), projects, cwd string, worktrees []string, windows, allProjects bool) []PickerStat {
	type directory struct{ path, project string }
	dirs := []directory{}
	if allProjects || len(worktrees) > 1 {
		entries, e := files.ReadDir(projects)
		if e == nil {
			type prefix struct{ raw, key string }
			prefixes := []prefix{}
			for _, path := range worktrees {
				key := projectpath.Sanitize(path)
				if windows {
					key = strings.ToLower(key)
				}
				prefixes = append(prefixes, prefix{path, key})
			}
			sort.SliceStable(prefixes, func(i, j int) bool { return len(prefixes[i].key) > len(prefixes[j].key) })
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				key := entry.Name()
				if windows {
					key = strings.ToLower(key)
				}
				if allProjects {
					dirs = append(dirs, directory{filepath.Join(projects, entry.Name()), ""})
					continue
				}
				for _, p := range prefixes {
					if key == p.key || strings.HasPrefix(key, p.key+"-") {
						dirs = append(dirs, directory{filepath.Join(projects, entry.Name()), p.raw})
						break
					}
				}
			}
		} else {
			dirs = append(dirs, directory{filepath.Join(projects, projectpath.Sanitize(cwd)), cwd})
		}
	} else {
		dirs = append(dirs, directory{filepath.Join(projects, projectpath.Sanitize(cwd)), cwd})
	}
	result := []PickerStat{}
	for _, directory := range dirs {
		entries, e := files.ReadDir(directory.path)
		if e != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			sid := strings.TrimSuffix(entry.Name(), ".jsonl")
			if !uuidName.MatchString(sid) {
				continue
			}
			path := filepath.Join(directory.path, entry.Name())
			value, e := stat(path)
			if e != nil {
				continue
			}
			result = append(result, PickerStat{SID: sid, Path: path, Project: directory.project, Modified: value.Modified, Birth: value.Birth, Size: value.Size})
		}
	}
	// Same repo worktree enumeration deduplicates before the shared sort.
	if len(worktrees) > 1 || allProjects {
		positions := map[string]int{}
		dedup := []PickerStat{}
		for _, row := range result {
			if i, present := positions[row.SID]; present {
				if row.Modified > dedup[i].Modified {
					dedup[i] = row
				}
			} else {
				positions[row.SID] = len(dedup)
				dedup = append(dedup, row)
			}
		}
		result = dedup
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Modified != result[j].Modified {
			return result[i].Modified > result[j].Modified
		}
		return result[i].Birth > result[j].Birth
	})
	return result
}
func EnrichPickerPage(ctx context.Context, files PickerFiles, stats []PickerStat, start, count int, environment map[string]string) (PickerPage, error) {
	out := PickerPage{Logs: []protocol.JSONObject{}, NextIndex: start}
	builtins, e := commandnames.Set(environment)
	if e != nil {
		return out, e
	}
	for out.NextIndex < len(stats) && len(out.Logs) < count {
		if e = ctx.Err(); e != nil {
			return out, context.Cause(ctx)
		}
		row := stats[out.NextIndex]
		out.NextIndex++
		head, tail := ReadHeadAndTail(files, row.Path, row.Size)
		if strings.Contains(head, `"isSidechain":true`) || strings.Contains(head, `"isSidechain": true`) || truthy(extract(head, "teamName", false)) {
			continue
		}
		log := protocol.NewJSONObject(protocol.JSONField{Name: "sessionId", Value: quote(row.SID)}, protocol.JSONField{Name: "fullPath", Value: quote(row.Path)}, protocol.JSONField{Name: "messages", Value: []byte("[]")}, protocol.JSONField{Name: "isLite", Value: []byte("false")}, protocol.JSONField{Name: "isSidechain", Value: []byte("false")}, protocol.JSONField{Name: "value", Value: numeric(float64(len(out.Logs)))}, protocol.JSONField{Name: "date", Value: quote(time.UnixMilli(int64(row.Modified)).UTC().Format("2006-01-02T15:04:05.000Z"))}, protocol.JSONField{Name: "modified", Value: numeric(row.Modified)}, protocol.JSONField{Name: "created", Value: numeric(row.Birth)}, protocol.JSONField{Name: "fileSize", Value: numeric(float64(row.Size))}, protocol.JSONField{Name: "messageCount", Value: []byte("0")})
		first := extract(tail, "lastPrompt", true)
		if !truthy(first) {
			first = insights.FirstChunkPrompt(head, builtins)
		}
		if !truthy(first) {
			first = quote(pickerPrefix(head, "content", 200))
		}
		if !truthy(first) {
			first = quote(pickerPrefix(head, "text", 200))
		}
		title := json.RawMessage(nil)
		for _, field := range []struct{ data, name string }{{tail, "customTitle"}, {head, "customTitle"}, {tail, "aiTitle"}, {head, "aiTitle"}} {
			if title == nil {
				title = extract(field.data, field.name, true)
			}
		}
		if !truthy(first) && !truthy(title) {
			first = quote("(session)")
		}
		log.Set("firstPrompt", first)
		log.Set("customTitle", title)
		project := extract(head, "cwd", false)
		if project == nil && row.Project != "" {
			project = quote(row.Project)
		}
		log.Set("projectPath", project)
		for _, name := range []string{"summary", "tag", "prUrl", "prRepository"} {
			log.Set(name, extract(tail, name, true))
		}
		log.Set("agentSetting", extract(head, "agentSetting", false))
		branch := extract(tail, "gitBranch", true)
		if branch == nil {
			branch = extract(head, "gitBranch", false)
		}
		log.Set("gitBranch", branch)
		pr := decimalPrefix(text(extract(tail, "prNumber", true)))
		if pr == 0 {
			if i := strings.LastIndex(tail, `"prNumber":`); i >= 0 {
				end := min(len(tail), i+25)
				if n := decimalPrefix(tail[i+11 : end]); n > 0 {
					pr = n
				}
			}
		}
		if pr != 0 {
			log.Set("prNumber", numeric(pr))
		}
		out.Logs = append(out.Logs, log)
	}
	return out, nil
}
func pickerPrefix(text, key string, maxLen int) string {
	units := utf16.Encode([]rune(text))
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		pos := strings.Index(text, pattern)
		if pos < 0 {
			continue
		}
		begin := len(utf16.Encode([]rune(text[:pos+len(pattern)])))
		i, collected := begin, 0
		for i < len(units) && collected < maxLen {
			if units[i] == '\\' {
				i += 2
				collected++
				continue
			}
			if units[i] == '"' {
				break
			}
			i++
			collected++
		}
		raw := string(utf16.Decode(units[begin:min(i, len(units))]))
		raw = strings.ReplaceAll(strings.ReplaceAll(raw, `\n`, " "), `\t`, " ")
		return protocol.TrimJavaScriptString(raw)
	}
	return ""
}

var prURL = regexp.MustCompile(`github\.com/[^/]+/[^/]+/pull/(\d+)`)

func ParsePRIdentifier(value string) *float64 {
	if n := decimalPrefix(value); n > 0 {
		return &n
	}
	if m := prURL.FindStringSubmatch(value); len(m) > 1 {
		n := decimalPrefix(m[1])
		return &n
	}
	return nil
}
func FilterPR(logs []protocol.JSONObject, filter json.RawMessage) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	var number *float64
	if len(filter) > 0 && filter[0] == '"' {
		var s string
		_ = json.Unmarshal(filter, &s)
		number = ParsePRIdentifier(s)
	} else if len(filter) > 0 && filter[0] != 't' && filter[0] != 'f' && filter[0] != 'n' {
		var n float64
		if json.Unmarshal(filter, &n) == nil {
			number = &n
		}
	}
	for _, log := range logs {
		if truthy(log.Get("isSidechain")) {
			continue
		}
		if string(filter) == "true" && log.Get("prNumber") == nil {
			continue
		}
		if number != nil {
			var n float64
			if json.Unmarshal(log.Get("prNumber"), &n) != nil || n != *number {
				continue
			}
		}
		out = append(out, log.Clone())
	}
	return out
}
