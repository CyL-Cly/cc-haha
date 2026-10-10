package localindex

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ActivityIndex struct {
	db       *Database
	location *time.Location
	shots    bool
}

func NewActivityIndex(db *Database, location *time.Location, shots bool) (*ActivityIndex, error) {
	if db == nil || db.kind != Primary {
		return nil, fmt.Errorf("Activity index requires its primary native database")
	}
	if location == nil {
		location = time.Local
	}
	return &ActivityIndex{db, location, shots}, nil
}
func activitySourceRow(r protocol.JSONObject) ActivitySource {
	return ActivitySource{stringValue(r.Get("path")), stringValue(r.Get("parent_session_id")), stringValue(r.Get("project_path")), intValue(r.Get("is_subagent")) == 1, intValue(r.Get("size_bytes")), numberValue(r.Get("mtime_ms")), nullableValue(r.Get("file_identity")), stringValue(r.Get("prefix_hash")), intValue(r.Get("indexed_bytes")), intValue(r.Get("parser_version")), stringValue(r.Get("state"))}
}
func (i *ActivityIndex) GetSource(ctx context.Context, path string) (*ActivitySource, error) {
	var out *ActivitySource
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM activity_sources WHERE path=?", path)
		if r != nil {
			s := activitySourceRow(*r)
			out = &s
		}
		return e
	})
	return out, err
}
func (i *ActivityIndex) ListSources(ctx context.Context) ([]ActivitySource, error) {
	out := []ActivitySource{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("SELECT * FROM activity_sources ORDER BY path ASC")
		if e != nil {
			return e
		}
		for _, r := range rows {
			out = append(out, activitySourceRow(r))
		}
		return nil
	})
	return out, err
}
func (i *ActivityIndex) CountSources(ctx context.Context) (int64, error) {
	var out int64
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT COUNT(*) AS count FROM activity_sources")
		if r != nil {
			out = intValue(r.Get("count"))
		}
		return e
	})
	return out, err
}
func (i *ActivityIndex) GetBackfill(ctx context.Context, scope string) (*ActivityBackfill, error) {
	var out *ActivityBackfill
	err := i.db.Read(ctx, func(op *Operation) error {
		r, e := op.Get("SELECT * FROM activity_backfill_state WHERE scope=?", scope)
		if r != nil {
			s := ActivityBackfill{stringValue(r.Get("scope")), stringValue(r.Get("state")), intValue(r.Get("discovered")), intValue(r.Get("indexed")), intValue(r.Get("degraded")), nullableValue(r.Get("last_error_code")), intValue(r.Get("updated_at_ms"))}
			out = &s
		}
		return e
	})
	return out, err
}

type sqlPredicate struct {
	sql      string
	bindings []any
}

func allTimeBounds(now time.Time) (string, string, float64) {
	utc := now.UTC()
	today := activityDate(float64(utc.UnixMilli()))
	yesterday := activityDate(float64(utc.AddDate(0, 0, -1).UnixMilli()))
	start := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	return today, yesterday, float64(start.UnixMilli())
}
func dateRange(rangeName string, now time.Time) (string, string) {
	if rangeName == "all" {
		return "", ""
	}
	days := 30
	if rangeName == "7d" {
		days = 7
	}
	return activityDate(float64(now.UTC().AddDate(0, 0, -days+1).UnixMilli())), activityDate(float64(now.UnixMilli()))
}
func activityRange(rangeName string, now time.Time, column, mtime string) sqlPredicate {
	from, to := dateRange(rangeName, now)
	if from == "" || to == "" {
		today, yesterday, start := allTimeBounds(now)
		if mtime != "" {
			return sqlPredicate{" WHERE (" + column + " <= ? OR (" + column + " = ? AND " + mtime + " >= ?))", []any{yesterday, today, start}}
		}
		return sqlPredicate{" WHERE " + column + " <= ?", []any{today}}
	}
	sql := " WHERE " + column + " >= ? AND " + column + " <= ?"
	bindings := []any{from, to}
	if mtime != "" {
		sql += " AND " + mtime + " >= ?"
		day, _ := time.Parse("2006-01-02", from)
		bindings = append(bindings, float64(day.UnixMilli()))
	}
	return sqlPredicate{sql, bindings}
}
func sourceMtimeRange(rangeName string, now time.Time, column string) sqlPredicate {
	from, _ := dateRange(rangeName, now)
	if from == "" {
		return sqlPredicate{}
	}
	day, _ := time.Parse("2006-01-02", from)
	return sqlPredicate{" WHERE " + column + " >= ?", []any{float64(day.UnixMilli())}}
}
func rowObject(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func (i *ActivityIndex) Aggregate(ctx context.Context, rangeName string, now time.Time) (activitystatsapi.Stats, error) {
	var result activitystatsapi.Stats
	err := i.db.Read(ctx, func(op *Operation) error {
		dailyRange := activityRange(rangeName, now, "activity_daily.date", "activity_sources.mtime_ms")
		modelRange := activityRange(rangeName, now, "activity_daily_models.date", "activity_sources.mtime_ms")
		sessionRange := activityRange(rangeName, now, "date(activity_sessions.first_timestamp)", "activity_sources.mtime_ms")
		mtimeRange := sourceMtimeRange(rangeName, now, "activity_sources.mtime_ms")
		today, yesterday, todayStart := allTimeBounds(now)
		rows, e := op.All(`SELECT activity_daily.date AS date,SUM(activity_daily.message_count) AS message_count,COUNT(DISTINCT activity_sources.parent_session_id) AS session_count,SUM(activity_daily.tool_call_count) AS tool_call_count FROM activity_daily JOIN activity_sources ON activity_sources.path=activity_daily.transcript_path`+dailyRange.sql+` GROUP BY activity_daily.date ORDER BY activity_daily.date ASC`, dailyRange.bindings...)
		if e != nil {
			return e
		}
		daily := []protocol.JSONObject{}
		for _, r := range rows {
			daily = append(daily, rowObject(protocol.JSONField{Name: "date", Value: r.Get("date")}, protocol.JSONField{Name: "messageCount", Value: r.Get("message_count")}, protocol.JSONField{Name: "sessionCount", Value: r.Get("session_count")}, protocol.JSONField{Name: "toolCallCount", Value: r.Get("tool_call_count")}))
		}
		modelRows, e := op.All(`SELECT activity_daily_models.date AS date,activity_daily_models.model AS model,SUM(input_tokens) AS input_tokens,SUM(output_tokens) AS output_tokens,SUM(cache_read_input_tokens) AS cache_read_input_tokens,SUM(cache_creation_input_tokens) AS cache_creation_input_tokens,SUM(web_search_requests) AS web_search_requests,SUM(cost_usd) AS cost_usd,MAX(context_window) AS context_window,MAX(max_output_tokens) AS max_output_tokens FROM activity_daily_models JOIN activity_sources ON activity_sources.path=activity_daily_models.transcript_path`+modelRange.sql+` GROUP BY activity_daily_models.date,activity_daily_models.model ORDER BY activity_daily_models.date ASC,activity_daily_models.model ASC`, modelRange.bindings...)
		if e != nil {
			return e
		}
		dailyTokens := map[string]protocol.JSONObject{}
		dateOrder := []string{}
		modelUsage := protocol.NewJSONObject()
		for _, r := range modelRows {
			date, model := stringValue(r.Get("date")), stringValue(r.Get("model"))
			tokens := numberValue(r.Get("input_tokens")) + numberValue(r.Get("output_tokens")) + numberValue(r.Get("cache_read_input_tokens")) + numberValue(r.Get("cache_creation_input_tokens"))
			if tokens > 0 {
				row, exists := dailyTokens[date]
				if !exists {
					row = protocol.NewJSONObject()
					dateOrder = append(dateOrder, date)
				}
				row.Set(model, raw(tokens))
				dailyTokens[date] = row
			}
			aggregate := objectValue(modelUsage.Get(model))
			if !aggregate.IsObject() {
				aggregate = protocol.NewJSONObject()
				for _, name := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD", "contextWindow", "maxOutputTokens"} {
					aggregate.Set(name, raw(0))
				}
			}
			for _, f := range [][2]string{{"inputTokens", "input_tokens"}, {"outputTokens", "output_tokens"}, {"cacheReadInputTokens", "cache_read_input_tokens"}, {"cacheCreationInputTokens", "cache_creation_input_tokens"}, {"webSearchRequests", "web_search_requests"}, {"costUSD", "cost_usd"}} {
				aggregate.Set(f[0], raw(numberValue(aggregate.Get(f[0]))+numberValue(r.Get(f[1]))))
			}
			aggregate.Set("contextWindow", raw(math.Max(numberValue(aggregate.Get("contextWindow")), numberValue(r.Get("context_window")))))
			aggregate.Set("maxOutputTokens", raw(math.Max(numberValue(aggregate.Get("maxOutputTokens")), numberValue(r.Get("max_output_tokens")))))
			modelUsage.Set(model, raw(aggregate))
		}
		dailyModelTokens := []protocol.JSONObject{}
		for _, date := range dateOrder {
			dailyModelTokens = append(dailyModelTokens, rowObject(protocol.JSONField{Name: "date", Value: raw(date)}, protocol.JSONField{Name: "tokensByModel", Value: raw(dailyTokens[date])}))
		}
		named := func(table, field string) (protocol.JSONObject, error) {
			predicate := activityRange(rangeName, now, table+".date", "activity_sources.mtime_ms")
			rows, e := op.All("SELECT "+field+" AS name,SUM(call_count) AS count FROM "+table+" JOIN activity_sources ON activity_sources.path="+table+".transcript_path"+predicate.sql+" GROUP BY "+field, predicate.bindings...)
			out := protocol.NewJSONObject()
			if e == nil {
				for _, r := range rows {
					out.Set(stringValue(r.Get("name")), r.Get("count"))
				}
			}
			return out, e
		}
		tools, e := named("activity_daily_tools", "tool_name")
		if e != nil {
			return e
		}
		skills, e := named("activity_daily_skills", "skill_name")
		if e != nil {
			return e
		}
		and := " WHERE"
		if sessionRange.sql != "" {
			and = " AND"
		}
		sessionRows, e := op.All(`SELECT activity_sessions.session_id,activity_sessions.duration_ms,activity_sessions.active_duration_ms,activity_sessions.message_count,activity_sessions.first_timestamp,activity_sessions.start_hour,activity_sources.mtime_ms AS source_mtime_ms FROM activity_sessions JOIN activity_sources ON activity_sources.path=activity_sessions.transcript_path`+sessionRange.sql+and+` activity_sources.is_subagent=0 AND activity_sessions.first_timestamp IS NOT NULL ORDER BY activity_sessions.first_timestamp ASC,activity_sessions.session_id ASC`, sessionRange.bindings...)
		if e != nil {
			return e
		}
		sessions := []protocol.JSONObject{}
		hours := protocol.NewJSONObject()
		totalMessages := 0.0
		for _, r := range sessionRows {
			sessions = append(sessions, rowObject(protocol.JSONField{Name: "sessionId", Value: r.Get("session_id")}, protocol.JSONField{Name: "duration", Value: r.Get("active_duration_ms")}, protocol.JSONField{Name: "messageCount", Value: r.Get("message_count")}, protocol.JSONField{Name: "timestamp", Value: r.Get("first_timestamp")}))
			totalMessages += numberValue(r.Get("message_count"))
			if string(r.Get("start_hour")) != "null" {
				hour := fmt.Sprintf("%.0f", numberValue(r.Get("start_hour")))
				hours.Set(hour, raw(numberValue(hours.Get(hour))+1))
			}
		}
		sumSpeculation := func(predicate sqlPredicate) (float64, error) {
			r, e := op.Get("SELECT COALESCE(SUM(speculation_time_saved_ms),0) AS total FROM activity_sessions JOIN activity_sources ON activity_sources.path=activity_sessions.transcript_path"+predicate.sql, predicate.bindings...)
			if e != nil {
				return 0, e
			}
			return numberValue(r.Get("total")), nil
		}
		historical := false
		if rangeName == "all" {
			r, e := op.Get("SELECT EXISTS(SELECT 1 FROM activity_daily WHERE date <= ? LIMIT 1) AS present", yesterday)
			if e != nil {
				return e
			}
			historical = intValue(r.Get("present")) == 1
		}
		speculation := 0.0
		if rangeName == "all" {
			if historical {
				speculation, e = sumSpeculation(sqlPredicate{})
				if e != nil {
					return e
				}
			}
			extra, e := sumSpeculation(sqlPredicate{" WHERE activity_sources.mtime_ms >= ?", []any{todayStart}})
			if e != nil {
				return e
			}
			speculation += extra
		} else {
			speculation, e = sumSpeculation(mtimeRange)
			if e != nil {
				return e
			}
		}
		readShots := func(predicate sqlPredicate) (protocol.JSONObject, error) {
			and := " WHERE"
			if predicate.sql != "" {
				and = " AND"
			}
			rows, e := op.All(`WITH ranked_shots AS(SELECT activity_sources.parent_session_id,activity_sessions.shot_count,ROW_NUMBER() OVER(PARTITION BY activity_sources.parent_session_id ORDER BY activity_sources.is_subagent ASC,activity_sources.path ASC) AS source_rank FROM activity_sessions JOIN activity_sources ON activity_sources.path=activity_sessions.transcript_path`+predicate.sql+and+` activity_sessions.shot_count IS NOT NULL) SELECT shot_count,COUNT(*) AS session_count FROM ranked_shots WHERE source_rank=1 GROUP BY shot_count ORDER BY shot_count ASC`, predicate.bindings...)
			out := protocol.NewJSONObject()
			if e == nil {
				for _, r := range rows {
					out.Set(fmt.Sprintf("%.0f", numberValue(r.Get("shot_count"))), r.Get("session_count"))
				}
			}
			return out, e
		}
		processed := rowObject(protocol.JSONField{Name: "dailyActivity", Value: raw(daily)}, protocol.JSONField{Name: "dailyModelTokens", Value: raw(dailyModelTokens)}, protocol.JSONField{Name: "modelUsage", Value: raw(modelUsage)}, protocol.JSONField{Name: "toolUsage", Value: raw(tools)}, protocol.JSONField{Name: "skillUsage", Value: raw(skills)}, protocol.JSONField{Name: "sessionStats", Value: raw(sessions)}, protocol.JSONField{Name: "hourCounts", Value: raw(hours)}, protocol.JSONField{Name: "totalMessages", Value: raw(totalMessages)}, protocol.JSONField{Name: "totalSpeculationTimeSavedMs", Value: raw(speculation)})
		if i.shots {
			shots := protocol.NewJSONObject()
			if rangeName == "all" {
				if historical {
					shots, e = readShots(sqlPredicate{})
					if e != nil {
						return e
					}
				}
				fresh, e := readShots(sqlPredicate{" WHERE activity_sources.mtime_ms >= ?", []any{todayStart}})
				if e != nil {
					return e
				}
				for _, p := range fresh.OwnProperties() {
					name := stringValue(p.NameJSON)
					shots.Set(name, raw(numberValue(shots.Get(name))+numberValue(p.Value)))
				}
			} else {
				shots, e = readShots(mtimeRange)
				if e != nil {
					return e
				}
			}
			processed.Set("shotDistribution", raw(shots))
		}
		result, e = activitystatsapi.IndexProcessedStats(processed, now, i.location, i.shots)
		if e != nil {
			return e
		}
		if rangeName == "all" {
			var last any
			dateParser := activitystatsapi.NewIndexDateParser(i.location)
			for _, r := range sessionRows {
				if numberValue(r.Get("source_mtime_ms")) < todayStart {
					continue
				}
				stamp := stringValue(r.Get("first_timestamp"))
				ms, valid, e := dateParser.Parse(stamp)
				if e != nil {
					return e
				}
				if valid && activityDate(ms) == today {
					previous, ok := last.(string)
					if !ok || stamp > previous {
						last = stamp
					}
				}
			}
			if last == nil && len(daily) > 0 {
				last = stringValue(daily[len(daily)-1].Get("date"))
			}
			result.Data["lastSessionDate"] = last
			days, e := activitystatsapi.IndexCalendarDays(result.Data["firstSessionDate"], last, i.location)
			if e != nil {
				return e
			}
			result.Data["totalDays"] = days
		}
		if i.shots {
			r, e := op.Get("SELECT EXISTS(SELECT 1 FROM activity_sources LIMIT 1) AS present")
			if e != nil {
				return e
			}
			if intValue(r.Get("present")) == 0 {
				delete(result.Data, "shotDistribution")
				delete(result.Data, "oneShotRate")
			}
		}
		return nil
	})
	return result, err
}
func (i *ActivityIndex) ExplainAggregate(ctx context.Context, rangeName string, now time.Time) ([]string, error) {
	predicate := activityRange(rangeName, now, "activity_daily.date", "activity_sources.mtime_ms")
	out := []string{}
	err := i.db.Read(ctx, func(op *Operation) error {
		rows, e := op.All("EXPLAIN QUERY PLAN SELECT activity_daily.date,SUM(activity_daily.message_count),COUNT(DISTINCT activity_sources.parent_session_id) FROM activity_daily JOIN activity_sources ON activity_sources.path=activity_daily.transcript_path"+predicate.sql+" GROUP BY activity_daily.date", predicate.bindings...)
		if e == nil {
			for _, r := range rows {
				out = append(out, stringValue(r.Get("detail")))
			}
		}
		return e
	})
	return out, err
}
func indexTableNames() []string {
	return strings.Fields("activity_sources activity_sessions activity_daily activity_daily_models activity_daily_tools activity_daily_skills activity_backfill_state")
}
