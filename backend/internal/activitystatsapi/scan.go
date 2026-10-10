package activitystatsapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

const maxJSONLBytes int64 = 100 * 1024 * 1024

type processed struct {
	daily         []any
	tokens        []any
	models        object
	tools         object
	skills        object
	sessions      []any
	hours         object
	totalMessages any
	speculation   any
	shots         object
}

func emptyProcessed() processed {
	return processed{daily: []any{}, tokens: []any{}, models: object{}, tools: object{}, skills: object{}, sessions: []any{}, hours: object{}, totalMessages: float64(0), speculation: float64(0), shots: object{}}
}
func (s *Service) allFiles() ([]string, error) {
	projects := filepath.Join(s.options.ConfigDir, "projects")
	entries, err := os.ReadDir(projects)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projects, project.Name())
		files, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		main, subagents := []string{}, []string{}
		for _, entry := range files {
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
				main = append(main, filepath.Join(path, entry.Name()))
			}
		}
		for _, entry := range files {
			if entry.IsDir() {
				subagents = append(subagents, collectSubagents(filepath.Join(path, entry.Name(), "subagents"), 1)...)
			}
		}
		result = append(result, main...)
		result = append(result, subagents...)
	}
	return result, nil
}
func collectSubagents(directory string, depth int) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []string{}
	}
	files, nested := []string{}, []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			if strings.HasPrefix(entry.Name(), "agent-") && strings.HasSuffix(entry.Name(), ".jsonl") {
				files = append(files, filepath.Join(directory, entry.Name()))
			}
		} else if entry.IsDir() && depth < 3 {
			nested = append(nested, collectSubagents(filepath.Join(directory, entry.Name()), depth+1)...)
		}
	}
	return append(files, nested...)
}
func readJSONL(path string) ([]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var raw []byte
	if info.Size() <= maxJSONLBytes {
		raw, err = io.ReadAll(file)
	} else {
		raw = make([]byte, maxJSONLBytes)
		n, e := file.ReadAt(raw, info.Size()-maxJSONLBytes)
		if e != nil && e != io.EOF {
			return nil, e
		}
		raw = raw[:n]
		if newline := bytes.IndexByte(raw, '\n'); newline >= 0 && newline < len(raw)-1 {
			raw = raw[newline+1:]
		}
	}
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	entries := []any{}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		text := trim(string(protocol.DecodeJavaScriptUTF8(line)))
		if text == "" {
			continue
		}
		value, err := decode([]byte(text))
		if err == nil {
			entries = append(entries, value)
		}
	}
	return entries, nil
}
func (s *Service) date(value any) (time.Time, bool, error) {
	// ECMAScript Date is a language primitive implemented by Goja in Go. A
	// fixed constructor receives only decoded JSON; no caller code is run.
	if _, missing := value.(undefinedValue); missing {
		return time.Time{}, false, nil
	}
	vm := s.dateVM
	var argument goja.Value
	if value == nil {
		argument = goja.Null()
	} else {
		argument = vm.ToValue(value)
	}
	result, err := vm.New(vm.Get("Date"), argument)
	if err != nil {
		return time.Time{}, false, err
	}
	method, ok := goja.AssertFunction(result.Get("getTime"))
	if !ok {
		return time.Time{}, false, errors.New("Date language primitive unavailable")
	}
	milliseconds, err := method(result)
	if err != nil {
		return time.Time{}, false, err
	}
	n := milliseconds.ToFloat()
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return time.Time{}, false, nil
	}
	date := time.UnixMilli(int64(n))
	text, isString := primitive(value).(string)
	if isString && (localISO.MatchString(text) || shortLegacyDate.MatchString(trim(text))) && s.options.Location != time.Local {
		local := date.In(time.Local)
		date = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), s.options.Location)
	}
	return date, true, nil
}

var localISO = regexp.MustCompile("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\\.[0-9]+)?)?$")
var shortLegacyDate = regexp.MustCompile("^[0-9]{1,2}$")

func dateKey(date time.Time) string {
	date = date.UTC()
	year := date.Year()
	if year < 0 || year > 9999 {
		return fmt.Sprintf("%+07d-%02d-%02d", year, date.Month(), date.Day())
	}
	return date.Format("2006-01-02")
}
func inRange(date, from, to string) bool {
	return (from == "" || date >= from) && (to == "" || date <= to)
}

var semver = regexp.MustCompile("^[0-9]+\\.[0-9]+\\.[0-9]")

func billable(entry any, message any) bool {
	fork := get(entry, "forkedFrom")
	if source, ok := get(fork, "sessionId").(string); ok && source != "" {
		if id, ok := get(fork, "messageUuid").(string); ok && id != "" {
			return false
		}
	}
	if version, ok := get(entry, "version").(string); ok && !semver.MatchString(version) {
		return false
	}
	for _, value := range []any{get(entry, "sessionId"), get(entry, "requestId"), get(message, "id")} {
		if text, ok := value.(string); ok && text == "" {
			return false
		}
	}
	return true
}
func usageKey(entry, message any) (string, bool) {
	id, ok := get(message, "id").(string)
	if !ok || id == "" {
		return "", false
	}
	request, _ := get(entry, "requestId").(string)
	return id + "\x00" + request, true
}
func (s *Service) process(ctx context.Context, files []string, from, to string) (processed, error) {
	out := emptyProcessed()
	daily := map[string]object{}
	sessionIDs := map[string]map[string]bool{}
	tokens := map[string]object{}
	sessionsWithShots := map[string]bool{}
	activity := func(date string) object {
		row := daily[date]
		if row == nil {
			row = object{"date": date, "messageCount": float64(0), "sessionCount": float64(0), "toolCallCount": float64(0)}
			daily[date] = row
		}
		return row
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if from != "" {
			if info, err := os.Stat(path); err == nil && dateKey(info.ModTime()) < from {
				continue
			}
		}
		entries, err := readJSONL(path)
		if err != nil {
			continue
		}
		messages := []any{}
		for _, entry := range entries {
			if entry == nil {
				return out, errors.New("null transcript entry has no type")
			}
			switch get(entry, "type") {
			case "user", "assistant", "attachment", "system":
				messages = append(messages, entry)
			case "speculation-accept":
				out.speculation = add(out.speculation, get(entry, "timeSavedMs"))
			}
		}
		if len(messages) == 0 {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		separator := string(filepath.Separator)
		subagent := strings.Contains(path, separator+"subagents"+separator)
		parentID := id
		parts := strings.Split(path, separator)
		for i, part := range parts {
			if part == "subagents" {
				if subagent && i > 0 {
					parentID = parts[i-1]
				}
				break
			}
		}
		if s.options.ShotStatsEnabled && !sessionsWithShots[parentID] {
			if count, found := shotCount(messages); found {
				sessionsWithShots[parentID] = true
				name := strconv.Itoa(count)
				out.shots[name] = add(orZero(get(out.shots, name)), float64(1))
			}
		}
		main := []any{}
		for _, message := range messages {
			if subagent || !truthy(get(message, "isSidechain")) {
				main = append(main, message)
			}
		}
		if len(main) == 0 {
			continue
		}
		first, valid, err := s.date(get(main[0], "timestamp"))
		if err != nil {
			return out, err
		}
		last, lastValid, err := s.date(get(main[len(main)-1], "timestamp"))
		if err != nil {
			return out, err
		}
		_ = last
		if !valid || !lastValid {
			continue
		}
		if !subagent && inRange(dateKey(first), from, to) {
			duration := float64(0)
			var previous *int64
			for _, message := range main {
				date, valid, err := s.date(get(message, "timestamp"))
				if err != nil {
					return out, err
				}
				if !valid {
					continue
				}
				current := date.UnixMilli()
				if previous != nil {
					gap := current - *previous
					if gap > 0 && gap <= 1800000 {
						duration += float64(gap)
					}
				}
				previous = &current
			}
			out.sessions = append(out.sessions, object{"sessionId": id, "duration": duration, "messageCount": float64(len(main)), "timestamp": get(main[0], "timestamp")})
			out.totalMessages = add(out.totalMessages, float64(len(main)))
			hour := strconv.Itoa(first.In(s.options.Location).Hour())
			out.hours[hour] = add(orZero(get(out.hours, hour)), float64(1))
		}
		counted := map[string]bool{}
		for _, entry := range main {
			date, valid, err := s.date(get(entry, "timestamp"))
			if err != nil {
				return out, err
			}
			key := dateKey(date)
			include := valid && inRange(key, from, to)
			if include {
				if sessionIDs[key] == nil {
					sessionIDs[key] = map[string]bool{}
				}
				sessionIDs[key][parentID] = true
				row := activity(key)
				if !subagent {
					row["messageCount"] = add(get(row, "messageCount"), float64(1))
				}
			}
			if get(entry, "type") != "assistant" {
				continue
			}
			message := get(entry, "message")
			content, ok := get(message, "content").([]any)
			if include && ok {
				for _, block := range content {
					if block == nil {
						return out, errors.New("null assistant content block has no type")
					}
					if get(block, "type") != "tool_use" {
						continue
					}
					row := activity(key)
					row["toolCallCount"] = add(get(row, "toolCallCount"), float64(1))
					if name, ok := get(block, "name").(string); ok {
						trimmed := trim(name)
						if trimmed != "" {
							out.tools[trimmed] = add(orZero(get(out.tools, trimmed)), float64(1))
						}
						if name == "Skill" {
							if skill, ok := get(get(block, "input"), "skill").(string); ok && trim(skill) != "" {
								skill = trim(skill)
								out.skills[skill] = add(orZero(get(out.skills, skill)), float64(1))
							}
						}
					}
				}
			}
			usage := get(message, "usage")
			if !truthy(usage) {
				continue
			}
			modelValue := get(message, "model")
			if !truthy(modelValue) {
				modelValue = "unknown"
			}
			model, modelIsString := modelValue.(string)
			if modelIsString && model == "<synthetic>" {
				continue
			}
			if !include || !billable(entry, message) {
				continue
			}
			if key, available := usageKey(entry, message); available {
				if counted[key] {
					continue
				}
				counted[key] = true
			}
			if !modelIsString {
				return out, errors.New("model identifier does not provide String.trim")
			}
			ledger, exists := out.models[model]
			if !exists {
				ledger = object{"inputTokens": float64(0), "outputTokens": float64(0), "cacheReadInputTokens": float64(0), "cacheCreationInputTokens": float64(0), "webSearchRequests": float64(0), "costUSD": float64(0), "contextWindow": float64(0), "maxOutputTokens": float64(0)}
				out.models[model] = ledger
			}
			row, err := fields(ledger)
			if err != nil {
				return out, err
			}
			creation := orZero(get(usage, "cache_creation_input_tokens"))
			if split := get(usage, "cache_creation"); truthy(split) {
				creation = add(orZero(get(split, "ephemeral_5m_input_tokens")), orZero(get(split, "ephemeral_1h_input_tokens")))
			}
			measure := object{"inputTokens": orZero(get(usage, "input_tokens")), "outputTokens": orZero(get(usage, "output_tokens")), "cacheReadInputTokens": orZero(get(usage, "cache_read_input_tokens")), "cacheCreationInputTokens": creation, "webSearchRequests": orZero(get(get(usage, "server_tool_use"), "web_search_requests"))}
			for _, name := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests"} {
				row[name] = add(get(row, name), get(measure, name))
			}
			speed, _ := get(usage, "speed").(string)
			if cost, priced := estimateCost(model, measure, speed); priced {
				row["costUSD"] = add(get(row, "costUSD"), cost)
			}
			out.models[model] = row
			total := add(add(add(get(measure, "inputTokens"), get(measure, "outputTokens")), get(measure, "cacheReadInputTokens")), creation)
			if numberValue(total) > 0 {
				if tokens[key] == nil {
					tokens[key] = object{}
				}
				tokens[key][model] = add(orZero(get(tokens[key], model)), total)
			}
		}
	}
	for key, ids := range sessionIDs {
		activity(key)["sessionCount"] = float64(len(ids))
	}
	keys := []string{}
	for key := range daily {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.daily = append(out.daily, daily[key])
	}
	keys = nil
	for key := range tokens {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.tokens = append(out.tokens, object{"date": key, "tokensByModel": tokens[key]})
	}
	return out, nil
}

var shotPattern = regexp.MustCompile("([0-9]+)-shotted by")

func shotCount(messages []any) (int, bool) {
	for _, message := range messages {
		if get(message, "type") != "assistant" {
			continue
		}
		content, ok := get(get(message, "message"), "content").([]any)
		if !ok {
			continue
		}
		for _, block := range content {
			if get(block, "type") != "tool_use" {
				continue
			}
			name, ok := get(block, "name").(string)
			if !ok || name != "Bash" && name != "PowerShell" {
				continue
			}
			command, ok := get(get(block, "input"), "command").(string)
			if !ok {
				continue
			}
			if match := shotPattern.FindStringSubmatch(command); match != nil {
				n, _ := strconv.Atoi(match[1])
				return n, true
			}
		}
	}
	return 0, false
}
