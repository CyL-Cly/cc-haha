package activitystatsapi

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func maxNumber(a, b float64) float64 { return math.Max(a, b) }
func compareStrings(a, b any) int {
	if left, ok := primitive(a).(string); ok {
		if right, ok := primitive(b).(string); ok {
			if utf16Less(left, right) {
				return -1
			}
			if utf16Less(right, left) {
				return 1
			}
			return 0
		}
	}
	left, right := numberValue(a), numberValue(b)
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
func emptyStreaks() object {
	return object{"currentStreak": float64(0), "longestStreak": float64(0), "currentStreakStart": nil, "longestStreakStart": nil, "longestStreakEnd": nil}
}
func emptyStats() object {
	return object{"totalSessions": float64(0), "totalMessages": float64(0), "totalDays": float64(0), "activeDays": float64(0), "streaks": emptyStreaks(), "dailyActivity": []any{}, "dailyModelTokens": []any{}, "longestSession": nil, "modelUsage": object{}, "toolUsage": object{}, "skillUsage": object{}, "firstSessionDate": nil, "lastSessionDate": nil, "peakActivityDay": nil, "peakActivityHour": nil, "unpricedModels": []any{}, "totalSpeculationTimeSavedMs": float64(0)}
}
func utcCalendarDay(date time.Time) time.Time {
	year := date.UTC().Year()
	if year >= 0 && year <= 99 {
		year += 1900
	}
	return time.Date(year, date.UTC().Month(), date.UTC().Day(), 0, 0, 0, 0, time.UTC)
}
func (s *Service) totalDays(first, last any) (float64, error) {
	if !truthy(first) || !truthy(last) {
		return 0, nil
	}
	a, valid, err := s.date(first)
	if err != nil {
		return 0, err
	}
	if !valid {
		return 0, nil
	}
	b, valid, err := s.date(last)
	if err != nil {
		return 0, err
	}
	if !valid {
		return 0, nil
	}
	a, b = utcCalendarDay(a), utcCalendarDay(b)
	if b.Before(a) {
		return 0, nil
	}
	return math.Floor(float64(b.UnixMilli()-a.UnixMilli())/86400000) + 1, nil
}
func (s *Service) streaks(daily []any, now time.Time) (object, error) {
	out := emptyStreaks()
	if len(daily) == 0 {
		return out, nil
	}
	active := object{}
	dates := []any{}
	for _, day := range daily {
		value := get(day, "date")
		key := mapKey(value)
		if !truthy(get(active, key)) {
			dates = append(dates, value)
		}
		active[key] = true
	}
	check := utcCalendarDay(now)
	current := float64(0)
	for truthy(get(active, mapKey(dateKey(check)))) {
		current++
		out["currentStreakStart"] = dateKey(check)
		check = check.AddDate(0, 0, -1)
	}
	out["currentStreak"] = current
	sort.SliceStable(dates, func(i, j int) bool {
		_, a := dates[i].(undefinedValue)
		_, b := dates[j].(undefinedValue)
		if a || b {
			return !a && b
		}
		return utf16Less(stringValue(dates[i]), stringValue(dates[j]))
	})
	length, longest := float64(1), float64(0)
	start := dates[0]
	finish := func(end any) {
		if length > longest {
			longest = length
			out["longestStreakStart"] = start
			out["longestStreakEnd"] = end
		}
	}
	for i := 1; i < len(dates); i++ {
		a, va, err := s.date(dates[i-1])
		if err != nil {
			return nil, err
		}
		b, vb, err := s.date(dates[i])
		if err != nil {
			return nil, err
		}
		if va && vb && math.Floor(float64(b.UnixMilli()-a.UnixMilli())/86400000+.5) == 1 {
			length++
		} else {
			finish(dates[i-1])
			length = 1
			start = dates[i]
		}
	}
	finish(dates[len(dates)-1])
	out["longestStreak"] = longest
	return out, nil
}
func longer(candidate, current any) bool {
	if !truthy(current) {
		return true
	}
	a, b := get(candidate, "duration"), get(current, "duration")
	if !strictEqual(a, b) {
		return compareStrings(a, b) > 0
	}
	return compareStrings(get(candidate, "sessionId"), get(current, "sessionId")) < 0
}
func unpriced(models object) ([]any, error) {
	out := []any{}
	for _, model := range sortedKeys(models) {
		row := get(models, model)
		if nullish(row) {
			return nil, errors.New("Null model usage")
		}
		tokens := add(add(add(get(row, "inputTokens"), get(row, "outputTokens")), get(row, "cacheReadInputTokens")), get(row, "cacheCreationInputTokens"))
		if numberValue(tokens) > 0 && resolveCosts(model, "") == nil {
			out = append(out, model)
		}
	}
	return out, nil
}
func peakDay(daily []any) any {
	if len(daily) == 0 {
		return nil
	}
	peak := daily[0]
	for _, day := range daily[1:] {
		if compareStrings(get(day, "messageCount"), get(peak, "messageCount")) > 0 {
			peak = day
		}
	}
	return get(peak, "date")
}
func parseInteger(value string) float64 {
	value = strings.TrimLeft(value, " \t\n\r")
	sign := float64(1)
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = value[1:]
	} else if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return math.NaN()
	}
	n, _ := strconv.ParseFloat(value[:i], 64)
	return sign * n
}
func peakHour(hours object, processed bool) (any, error) {
	keys := ownKeys(hours)
	if len(keys) == 0 {
		return nil, nil
	}
	peak := keys[0]
	for _, key := range keys[1:] {
		previous := get(hours, peak)
		if processed {
			previous = parseInteger(stringValue(get(hours, peak)))
		}
		if compareStrings(get(hours, key), previous) > 0 {
			peak = key
		}
	}
	return parseInteger(peak), nil
}
func (s *Service) putShots(out object, shots object) {
	out["shotDistribution"] = shots
	var total any = float64(0)
	for _, count := range shots {
		total = add(total, count)
	}
	rate := float64(0)
	if numberValue(total) > 0 {
		rate = math.Floor(numberValue(orZero(get(shots, "1")))/numberValue(total)*100 + .5)
	}
	out["oneShotRate"] = rate
}
func (s *Service) processedStats(data processed, now time.Time) (Stats, error) {
	daily, err := mergeDaily(nil, data.daily, false)
	if err != nil {
		return Stats{}, err
	}
	tokens, err := mergeDaily(nil, data.tokens, true)
	if err != nil {
		return Stats{}, err
	}
	streak, err := s.streaks(daily, now)
	if err != nil {
		return Stats{}, err
	}
	var longest, first, last any = nil, nil, nil
	for _, session := range data.sessions {
		if longer(session, longest) {
			longest = session
		}
		timestamp := get(session, "timestamp")
		if !truthy(first) || compareStrings(timestamp, first) < 0 {
			first = timestamp
		}
		if !truthy(last) || compareStrings(timestamp, last) > 0 {
			last = timestamp
		}
	}
	days, err := s.totalDays(first, last)
	if err != nil {
		return Stats{}, err
	}
	models, err := unpriced(data.models)
	if err != nil {
		return Stats{}, err
	}
	hour, err := peakHour(data.hours, true)
	if err != nil {
		return Stats{}, err
	}
	out := object{"totalSessions": float64(len(data.sessions)), "totalMessages": data.totalMessages, "totalDays": days, "activeDays": float64(len(data.daily)), "streaks": streak, "dailyActivity": daily, "dailyModelTokens": tokens, "longestSession": longest, "modelUsage": data.models, "unpricedModels": models, "toolUsage": data.tools, "skillUsage": data.skills, "firstSessionDate": first, "lastSessionDate": last, "peakActivityDay": peakDay(daily), "peakActivityHour": hour, "totalSpeculationTimeSavedMs": data.speculation}
	if s.options.ShotStatsEnabled {
		s.putShots(out, data.shots)
	}
	return Stats{out}, nil
}
func (s *Service) cacheStats(cache object, today processed, now time.Time) (Stats, error) {
	oldDaily, ok := get(cache, "dailyActivity").([]any)
	if !ok {
		return Stats{}, errors.New("Cache daily activity is not iterable")
	}
	oldTokens, ok := get(cache, "dailyModelTokens").([]any)
	if !ok {
		return Stats{}, errors.New("Cache daily tokens are not iterable")
	}
	daily, err := mergeDaily(oldDaily, today.daily, false)
	if err != nil {
		return Stats{}, err
	}
	tokens, err := mergeDaily(oldTokens, today.tokens, true)
	if err != nil {
		return Stats{}, err
	}
	models, err := mergeModelUsage(get(cache, "modelUsage"), today.models)
	if err != nil {
		return Stats{}, err
	}
	unpricedModels, err := unpriced(models)
	if err != nil {
		return Stats{}, err
	}
	sourceHours, err := fields(get(cache, "hourCounts"))
	if err != nil {
		return Stats{}, err
	}
	hours := object{}
	for _, hour := range ownKeys(sourceHours) {
		key := stringValue(parseInteger(hour))
		hours[key] = get(sourceHours, hour)
	}
	for _, hour := range ownKeys(today.hours) {
		key := stringValue(parseInteger(hour))
		hours[key] = add(orZero(get(hours, key)), get(today.hours, hour))
	}
	peak, err := peakHour(hours, false)
	if err != nil {
		return Stats{}, err
	}
	longest, first, last := get(cache, "longestSession"), get(cache, "firstSessionDate"), any(nil)
	for _, session := range today.sessions {
		if longer(session, longest) {
			longest = session
		}
		timestamp := get(session, "timestamp")
		if !truthy(first) || compareStrings(timestamp, first) < 0 {
			first = timestamp
		}
		if !truthy(last) || compareStrings(timestamp, last) > 0 {
			last = timestamp
		}
	}
	if !truthy(last) && len(daily) > 0 {
		last = get(daily[len(daily)-1], "date")
	}
	days, err := s.totalDays(first, last)
	if err != nil {
		return Stats{}, err
	}
	streak, err := s.streaks(daily, now)
	if err != nil {
		return Stats{}, err
	}
	out := object{"totalSessions": add(get(cache, "totalSessions"), float64(len(today.sessions))), "totalMessages": add(get(cache, "totalMessages"), orZero(today.totalMessages)), "totalDays": days, "activeDays": float64(len(daily)), "streaks": streak, "dailyActivity": daily, "dailyModelTokens": tokens, "longestSession": longest, "modelUsage": models, "unpricedModels": unpricedModels, "toolUsage": mergeCounts(get(cache, "toolUsage"), today.tools), "skillUsage": mergeCounts(get(cache, "skillUsage"), today.skills), "firstSessionDate": first, "lastSessionDate": last, "peakActivityDay": peakDay(daily), "peakActivityHour": peak, "totalSpeculationTimeSavedMs": add(get(cache, "totalSpeculationTimeSavedMs"), orZero(today.speculation))}
	if s.options.ShotStatsEnabled {
		shots := spread(get(cache, "shotDistribution"))
		for count, sessions := range today.shots {
			key := stringValue(parseInteger(count))
			shots[key] = add(orZero(get(shots, key)), sessions)
		}
		s.putShots(out, shots)
	}
	return Stats{out}, nil
}
