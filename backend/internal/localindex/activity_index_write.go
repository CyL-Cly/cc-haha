package localindex

import (
	"encoding/json"
	"math"
)

type ActivitySourceDescriptor struct {
	Path            string      `json:"path"`
	ParentSessionID string      `json:"parentSessionId"`
	ProjectPath     string      `json:"projectPath"`
	IsSubagent      bool        `json:"isSubagent"`
	Fingerprint     Fingerprint `json:"fingerprint"`
	FingerprintJSON string      `json:"fingerprintJson"`
	IndexedBytes    int64       `json:"indexedBytes"`
	ParserVersion   int64       `json:"parserVersion"`
	State           string      `json:"state"`
	UpdatedAtMs     int64       `json:"updatedAtMs"`
}
type ActivitySource struct {
	Path            string  `json:"path"`
	ParentSessionID string  `json:"parentSessionId"`
	ProjectPath     string  `json:"projectPath"`
	IsSubagent      bool    `json:"isSubagent"`
	Size            int64   `json:"size"`
	MtimeMs         float64 `json:"mtimeMs"`
	FileIdentity    *string `json:"fileIdentity"`
	Fingerprint     string  `json:"fingerprint"`
	IndexedBytes    int64   `json:"indexedBytes"`
	ParserVersion   int64   `json:"parserVersion"`
	State           string  `json:"state"`
}
type ActivityBackfill struct {
	Scope         string  `json:"scope"`
	State         string  `json:"state"`
	Discovered    int64   `json:"discovered"`
	Indexed       int64   `json:"indexed"`
	Degraded      int64   `json:"degraded"`
	LastErrorCode *string `json:"lastErrorCode"`
	UpdatedAtMs   int64   `json:"updatedAtMs"`
}

func binding(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}
func booleanInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func WriteActivityProjection(op *Operation, source ActivitySourceDescriptor, a ActivityProjection, parseDate func(string) (float64, bool, error)) error {
	err := op.Exec(`INSERT INTO activity_sources(path,parent_session_id,project_path,is_subagent,size_bytes,mtime_ms,file_identity,prefix_hash,indexed_bytes,parser_version,state,last_error_code,updated_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL,?) ON CONFLICT(path) DO UPDATE SET parent_session_id=excluded.parent_session_id,project_path=excluded.project_path,is_subagent=excluded.is_subagent,size_bytes=excluded.size_bytes,mtime_ms=excluded.mtime_ms,file_identity=excluded.file_identity,prefix_hash=excluded.prefix_hash,indexed_bytes=excluded.indexed_bytes,parser_version=excluded.parser_version,state=excluded.state,last_error_code=NULL,updated_at_ms=excluded.updated_at_ms`, source.Path, source.ParentSessionID, source.ProjectPath, booleanInt(source.IsSubagent), source.Fingerprint.Size, source.Fingerprint.MtimeMs, source.Fingerprint.FileIdentity, source.FingerprintJSON, source.IndexedBytes, source.ParserVersion, source.State, source.UpdatedAtMs)
	if err != nil {
		return err
	}
	for _, table := range []string{"activity_sessions", "activity_daily", "activity_daily_models", "activity_daily_tools", "activity_daily_skills"} {
		if err = op.Exec("DELETE FROM "+table+" WHERE transcript_path=?", source.Path); err != nil {
			return err
		}
	}
	duration := 0.0
	first, last := math.NaN(), math.NaN()
	if a.FirstTimestamp != nil && *a.FirstTimestamp != "" {
		n, valid, e := parseDate(*a.FirstTimestamp)
		if e != nil {
			return e
		}
		if valid {
			first = n
		}
	}
	if a.LastTimestamp != nil && *a.LastTimestamp != "" {
		n, valid, e := parseDate(*a.LastTimestamp)
		if e != nil {
			return e
		}
		if valid {
			last = n
		}
	}
	if !math.IsNaN(first) && !math.IsNaN(last) && !math.IsInf(first, 0) && !math.IsInf(last, 0) {
		duration = last - first
	}
	if err = op.Exec(`INSERT INTO activity_sessions(transcript_path,session_id,first_timestamp,last_timestamp,duration_ms,active_duration_ms,message_count,start_hour,speculation_time_saved_ms,shot_count) VALUES(?,?,?,?,?,?,?,?,?,?)`, source.Path, source.ParentSessionID, a.FirstTimestamp, a.LastTimestamp, duration, a.ActiveDurationMs, a.MessageCount, a.StartHour, a.SpeculationTimeSavedMs, a.ShotCount); err != nil {
		return err
	}
	for _, day := range a.Daily {
		if err = op.Exec("INSERT INTO activity_daily(transcript_path,date,message_count,tool_call_count) VALUES(?,?,?,?)", source.Path, day.Date, day.MessageCount, day.ToolCallCount); err != nil {
			return err
		}
	}
	for _, m := range a.Models {
		f := m.Fields
		if err = op.Exec(`INSERT INTO activity_daily_models(transcript_path,date,model,input_tokens,output_tokens,cache_read_input_tokens,cache_creation_input_tokens,web_search_requests,cost_usd,context_window,max_output_tokens) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, source.Path, binding(f.Get("date")), binding(f.Get("model")), binding(f.Get("inputTokens")), binding(f.Get("outputTokens")), binding(f.Get("cacheReadInputTokens")), binding(f.Get("cacheCreationInputTokens")), binding(f.Get("webSearchRequests")), m.CostUSD, binding(f.Get("contextWindow")), binding(f.Get("maxOutputTokens"))); err != nil {
			return err
		}
	}
	for _, tool := range a.Tools {
		if err = op.Exec("INSERT INTO activity_daily_tools(transcript_path,date,tool_name,call_count) VALUES(?,?,?,?)", source.Path, tool.Date, tool.Name, tool.Count); err != nil {
			return err
		}
	}
	for _, skill := range a.Skills {
		if err = op.Exec("INSERT INTO activity_daily_skills(transcript_path,date,skill_name,call_count) VALUES(?,?,?,?)", source.Path, skill.Date, skill.Name, skill.Count); err != nil {
			return err
		}
	}
	return nil
}
func WriteActivityBackfill(op *Operation, state ActivityBackfill, watermark *string) error {
	return op.Exec(`INSERT INTO activity_backfill_state(scope,state,watermark,discovered,indexed,degraded,last_error_code,updated_at_ms) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET state=excluded.state,watermark=excluded.watermark,discovered=excluded.discovered,indexed=excluded.indexed,degraded=excluded.degraded,last_error_code=excluded.last_error_code,updated_at_ms=excluded.updated_at_ms`, state.Scope, state.State, watermark, state.Discovered, state.Indexed, state.Degraded, state.LastErrorCode, state.UpdatedAtMs)
}
func MarkActivityBuilding(op *Operation, scope string, nowMs int64) error {
	return op.Exec("UPDATE activity_backfill_state SET state='building',updated_at_ms=? WHERE scope=?", nowMs, scope)
}
