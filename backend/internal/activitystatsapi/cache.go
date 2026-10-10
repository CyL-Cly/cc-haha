package activitystatsapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func emptyCache() object {
	return object{"version": float64(7), "lastComputedDate": nil, "dailyActivity": []any{}, "dailyModelTokens": []any{}, "modelUsage": object{}, "toolUsage": object{}, "skillUsage": object{}, "totalSessions": float64(0), "totalMessages": float64(0), "longestSession": nil, "firstSessionDate": nil, "hourCounts": object{}, "totalSpeculationTimeSavedMs": float64(0), "shotDistribution": object{}}
}
func (s *Service) loadCache() object {
	raw, err := os.ReadFile(filepath.Join(s.options.ConfigDir, "stats-cache.json"))
	if err != nil {
		return emptyCache()
	}
	value, err := decode(raw)
	if err != nil {
		return emptyCache()
	}
	cache, ok := value.(map[string]any)
	if !ok {
		return emptyCache()
	}
	if version, ok := get(cache, "version").(float64); !ok || version != 7 {
		return emptyCache()
	}
	if _, ok := get(cache, "dailyActivity").([]any); !ok {
		return emptyCache()
	}
	if _, ok := get(cache, "dailyModelTokens").([]any); !ok {
		return emptyCache()
	}
	for _, name := range []string{"toolUsage", "skillUsage"} {
		v := get(cache, name)
		switch v.(type) {
		case map[string]any, []any:
		default:
			return emptyCache()
		}
	}
	for _, name := range []string{"totalSessions", "totalMessages"} {
		if _, ok := get(cache, name).(float64); !ok {
			return emptyCache()
		}
	}
	if s.options.ShotStatsEnabled && !truthy(get(cache, "shotDistribution")) {
		return emptyCache()
	}
	return cache
}
func (s *Service) saveCache(cache object) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return
	}
	path := filepath.Join(s.options.ConfigDir, "stats-cache.json")
	temporary := path + "." + hex.EncodeToString(random[:]) + ".tmp"
	defer os.Remove(temporary)
	_ = os.Mkdir(s.options.ConfigDir, 0755)
	raw, err := json.MarshalIndent(clean(cache), "", "  ")
	if err != nil {
		return
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	_ = os.Rename(temporary, path)
}
func mergeDaily(old []any, next []any, tokens bool) ([]any, error) {
	days := map[string]object{}
	order := []string{}
	for group, rows := range [][]any{old, next} {
		for _, entry := range rows {
			row, err := fields(entry)
			if err != nil {
				return nil, err
			}
			date := mapKey(get(row, "date"))
			previous := days[date]
			if previous == nil || group == 0 {
				copy := spread(row)
				if tokens {
					copy = object{"date": get(row, "date"), "tokensByModel": spread(get(row, "tokensByModel"))}
				}
				days[date] = copy
				if previous == nil {
					order = append(order, date)
				}
			} else if tokens {
				target := spread(get(previous, "tokensByModel"))
				values, err := fields(get(row, "tokensByModel"))
				if err != nil {
					return nil, err
				}
				for name, count := range values {
					target[name] = add(orZero(get(target, name)), count)
				}
				previous["tokensByModel"] = target
			} else {
				for _, name := range []string{"messageCount", "sessionCount", "toolCallCount"} {
					previous[name] = add(get(previous, name), get(row, name))
				}
			}
		}
	}
	var sortingError error
	comparer := collate.New(language.English)
	sort.SliceStable(order, func(i, j int) bool {
		left, ok := get(days[order[i]], "date").(string)
		if !ok {
			sortingError = &dataError{"Activity date does not provide String.localeCompare"}
			return false
		}
		return comparer.CompareString(left, stringValue(get(days[order[j]], "date"))) < 0
	})
	if sortingError != nil {
		return nil, sortingError
	}
	result := []any{}
	for _, date := range order {
		result = append(result, days[date])
	}
	return result, nil
}

type dataError struct{ message string }

func (e *dataError) Error() string { return e.message }
func mergeModelUsage(old any, next object) (object, error) {
	out := spread(old)
	for name, entry := range next {
		if truthy(get(out, name)) {
			original, err := fields(get(out, name))
			if err != nil {
				return nil, err
			}
			incoming, err := fields(entry)
			if err != nil {
				return nil, err
			}
			row := object{}
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
				row[key] = add(get(original, key), get(incoming, key))
			}
			for _, key := range []string{"contextWindow", "maxOutputTokens"} {
				left, right := numberValue(get(original, key)), numberValue(get(incoming, key))
				row[key] = maxNumber(left, right)
			}
			out[name] = row
		} else {
			out[name] = spread(entry)
		}
	}
	return out, nil
}
func mergeCounts(values ...any) object {
	out := object{}
	for _, value := range values {
		if nullish(value) {
			continue
		}
		for name, count := range spread(value) {
			if n, ok := count.(float64); ok && n > 0 {
				out[name] = add(orZero(get(out, name)), count)
			}
		}
	}
	return out
}
func mergeCacheCounts(existing any, next object) object {
	out := spread(existing)
	for name, count := range next {
		if n, ok := count.(float64); ok && n > 0 {
			out[name] = add(orZero(get(out, name)), count)
		}
	}
	return out
}
func (s *Service) mergeCache(old object, next processed, last string) (object, error) {
	oldDaily, ok := get(old, "dailyActivity").([]any)
	if !ok {
		return nil, &dataError{"Cache daily activity is not iterable"}
	}
	oldTokens, ok := get(old, "dailyModelTokens").([]any)
	if !ok {
		return nil, &dataError{"Cache daily model tokens are not iterable"}
	}
	daily, err := mergeDaily(oldDaily, next.daily, false)
	if err != nil {
		return nil, err
	}
	tokens, err := mergeDaily(oldTokens, next.tokens, true)
	if err != nil {
		return nil, err
	}
	models, err := mergeModelUsage(get(old, "modelUsage"), next.models)
	if err != nil {
		return nil, err
	}
	hours := spread(get(old, "hourCounts"))
	for hour, count := range next.hours {
		hours[hour] = add(orZero(get(hours, hour)), count)
	}
	longest, first := get(old, "longestSession"), get(old, "firstSessionDate")
	for _, session := range next.sessions {
		if !truthy(longest) || compareStrings(get(session, "duration"), get(longest, "duration")) > 0 {
			longest = session
		}
		if !truthy(first) || compareStrings(get(session, "timestamp"), first) < 0 {
			first = get(session, "timestamp")
		}
	}
	var newMessages any = float64(0)
	for _, session := range next.sessions {
		newMessages = add(newMessages, get(session, "messageCount"))
	}
	messages := add(get(old, "totalMessages"), newMessages)
	out := object{"version": float64(7), "lastComputedDate": last, "dailyActivity": daily, "dailyModelTokens": tokens, "modelUsage": models, "toolUsage": mergeCacheCounts(get(old, "toolUsage"), next.tools), "skillUsage": mergeCacheCounts(get(old, "skillUsage"), next.skills), "totalSessions": add(get(old, "totalSessions"), float64(len(next.sessions))), "totalMessages": messages, "longestSession": longest, "firstSessionDate": first, "hourCounts": hours, "totalSpeculationTimeSavedMs": add(get(old, "totalSpeculationTimeSavedMs"), next.speculation)}
	if s.options.ShotStatsEnabled {
		shots := spread(get(old, "shotDistribution"))
		for shot, count := range next.shots {
			shots[shot] = add(orZero(get(shots, shot)), count)
		}
		out["shotDistribution"] = shots
	}
	return out, nil
}
