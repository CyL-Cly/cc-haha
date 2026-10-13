package localindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

type TranscriptChunk struct {
	Text         string `json:"text"`
	ByteStart    int64  `json:"byteStart"`
	ByteLength   *int64 `json:"byteLength,omitempty"`
	CompleteLine bool   `json:"completeLine"`
}
type ActivityDay struct {
	Date          string `json:"date"`
	MessageCount  int64  `json:"messageCount"`
	ToolCallCount int64  `json:"toolCallCount"`
}
type NamedUsage struct {
	Date  string `json:"date"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}
type ActivityModel struct {
	Fields  protocol.JSONObject
	CostUSD float64
}

func (m *ActivityModel) UnmarshalJSON(data []byte) error {
	if e := json.Unmarshal(data, &m.Fields); e != nil {
		return e
	}
	return json.Unmarshal(m.Fields.Get("costUSD"), &m.CostUSD)
}
func (m ActivityModel) MarshalJSON() ([]byte, error) {
	o := m.Fields.Clone()
	o.Set("costUSD", raw(m.CostUSD))
	return o.MarshalJSON()
}

type ActivityProjection struct {
	IsSubagent             bool            `json:"isSubagent"`
	FirstTimestamp         *string         `json:"firstTimestamp"`
	LastTimestamp          *string         `json:"lastTimestamp"`
	ActiveDurationMs       float64         `json:"activeDurationMs"`
	MessageCount           int64           `json:"messageCount"`
	StartHour              *int            `json:"startHour"`
	SpeculationTimeSavedMs float64         `json:"speculationTimeSavedMs"`
	ShotCount              *int            `json:"shotCount"`
	Daily                  []ActivityDay   `json:"daily"`
	Models                 []ActivityModel `json:"models"`
	Tools                  []NamedUsage    `json:"tools"`
	Skills                 []NamedUsage    `json:"skills"`
}
type TranscriptProjection struct {
	Summary            protocol.JSONObject `json:"summary"`
	IndexedBytes       int64               `json:"indexedBytes"`
	PendingTailBytes   int64               `json:"pendingTailBytes"`
	MalformedLineCount int64               `json:"malformedLineCount"`
	Activity           *ActivityProjection `json:"activity,omitempty"`
	state              *reducerState
}
type ReductionOptions struct {
	IsSubagent               *bool
	Platform                 string
	ParseDate                func(string) (float64, bool, error)
	Location                 *time.Location
	Compare                  func(string, string) int
	ValidateRetainedMetadata func(protocol.JSONObject) error
}
type reducerState struct {
	fallbackCreated, fallbackModified, fallbackWorkDir                                json.RawMessage
	createdAt                                                                         json.RawMessage
	hasCreated                                                                        bool
	semanticModified                                                                  *string
	messageCount                                                                      int64
	firstUserTitle, goalTitle, aiTitle, customTitle                                   json.RawMessage
	workDir, cwd, permission, provider, model, effort, fastMode, repository, worktree json.RawMessage
	teamWorker                                                                        bool
	nextOrdinal, nextLine                                                             int64
	activity                                                                          ActivityProjection
	sawActivity, firstValid, lastValid                                                bool
	lastMessageTime                                                                   *float64
	days                                                                              map[string]ActivityDay
	models                                                                            map[string]ActivityModel
	tools, skills                                                                     map[string]NamedUsage
	usageKeys                                                                         map[string]bool
}

func cloneReductionState(s *reducerState) *reducerState {
	out := *s
	out.days = map[string]ActivityDay{}
	for k, v := range s.days {
		out.days[k] = v
	}
	out.models = map[string]ActivityModel{}
	for k, v := range s.models {
		v.Fields = v.Fields.Clone()
		out.models[k] = v
	}
	out.tools = map[string]NamedUsage{}
	for k, v := range s.tools {
		out.tools[k] = v
	}
	out.skills = map[string]NamedUsage{}
	for k, v := range s.skills {
		out.skills[k] = v
	}
	out.usageKeys = map[string]bool{}
	for k, v := range s.usageKeys {
		out.usageKeys[k] = v
	}
	return &out
}
func initialReducer(seed *TranscriptProjection, o ReductionOptions) *reducerState {
	created, modified, workDir := raw(""), raw(""), raw(nil)
	if seed != nil {
		created = seed.Summary.Get("createdAt")
		modified = seed.Summary.Get("modifiedAt")
		workDir = seed.Summary.Get("workDir")
	}
	s := &reducerState{fallbackCreated: created, fallbackModified: modified, fallbackWorkDir: workDir, createdAt: created, nextLine: 1, days: map[string]ActivityDay{}, models: map[string]ActivityModel{}, tools: map[string]NamedUsage{}, skills: map[string]NamedUsage{}, usageKeys: map[string]bool{}}
	if o.IsSubagent != nil {
		s.activity.IsSubagent = *o.IsSubagent
	} else if seed != nil && seed.Activity != nil {
		s.activity.IsSubagent = seed.Activity.IsSubagent
	}
	if seed != nil && seed.Activity != nil {
		a := seed.Activity
		s.activity = *a
		s.sawActivity = a.FirstTimestamp != nil
		s.firstValid = a.FirstTimestamp != nil
		s.lastValid = a.LastTimestamp != nil
		if a.LastTimestamp != nil {
			ms, valid, _ := o.ParseDate(*a.LastTimestamp)
			if valid {
				s.lastMessageTime = &ms
			}
		}
		for _, v := range a.Daily {
			s.days[v.Date] = v
		}
		for _, v := range a.Models {
			v.Fields = v.Fields.Clone()
			s.models[stringValue(v.Fields.Get("date"))+"\x00"+stringValue(v.Fields.Get("model"))] = v
		}
		for _, v := range a.Tools {
			s.tools[v.Date+"\x00"+v.Name] = v
		}
		for _, v := range a.Skills {
			s.skills[v.Date+"\x00"+v.Name] = v
		}
	}
	return s
}
func jsonStringValue(v json.RawMessage) bool { v = bytesTrim(v); return len(v) > 0 && v[0] == '"' }
func bytesTrim(v json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(v)))
}
func oneOf(v json.RawMessage, options ...string) bool {
	if !jsonStringValue(v) {
		return false
	}
	s := stringValue(v)
	for _, candidate := range options {
		if s == candidate {
			return true
		}
	}
	return false
}
func objectValue(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(v, &o)
	return o
}
func (s *reducerState) apply(entry protocol.JSONObject, o ReductionOptions) error {
	if entry.String("entrypoint") == "claude-desktop-team-worker" {
		s.teamWorker = true
	}
	if e := s.applyActivity(entry, o); e != nil {
		return e
	}
	stamp := entry.Get("timestamp")
	if !s.hasCreated && jsTruthy(stamp) {
		s.createdAt = stamp
		s.hasCreated = true
	}
	kind := entry.String("type")
	message := objectValue(entry.Get("message"))
	if (kind == "user" || kind == "assistant") && jsTruthy(message.Get("role")) {
		s.messageCount++
		if !jsTruthy(entry.Get("isMeta")) && jsonStringValue(stamp) {
			candidate := stringValue(stamp)
			ms, valid, e := o.ParseDate(candidate)
			if e != nil {
				return e
			}
			if valid {
				update := s.semanticModified == nil
				if !update {
					previous, previousValid, e := o.ParseDate(*s.semanticModified)
					if e != nil {
						return e
					}
					update = !previousValid || ms > previous
				}
				if update {
					s.semanticModified = &candidate
				}
			}
		}
	}
	if kind == "session-meta" {
		if v := entry.Get("workDir"); jsonStringValue(v) {
			s.workDir = sessionprojection.LocalIndexNormalizeDrive(v, o.Platform)
		}
		if v := entry.Get("permissionMode"); oneOf(v, "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto") {
			s.permission = v
		}
		if v := entry.Get("openAIFastMode"); string(v) == "true" || string(v) == "false" {
			s.fastMode = v
		}
		provider, model := entry.Get("runtimeProviderId"), entry.Get("runtimeModelId")
		if string(provider) == "null" || jsonStringValue(provider) {
			s.provider = provider
		}
		if jsonStringValue(model) {
			s.model = model
		}
		if v := entry.Get("effortLevel"); oneOf(v, "low", "medium", "high", "xhigh", "max") {
			s.effort = v
		} else if (string(provider) == "null" || jsonStringValue(provider)) && jsonStringValue(model) && jsTruthy(model) {
			s.effort = nil
		}
	}
	if v := entry.Get("cwd"); jsonStringValue(v) && trimJS(stringValue(v)) != "" {
		s.cwd = sessionprojection.LocalIndexNormalizeDrive(v, o.Platform)
	}
	if v := bytesTrim(entry.Get("repository")); jsTruthy(v) && len(v) > 0 && (v[0] == '{' || v[0] == '[') {
		s.repository = v
	}
	if kind == "worktree-state" {
		v := entry.Get("worktreeSession")
		fields := objectValue(v)
		if string(v) == "null" || jsTruthy(v) && jsonStringValue(fields.Get("worktreePath")) && jsonStringValue(fields.Get("worktreeName")) {
			s.worktree = v
		}
	}
	if kind == "custom-title" && jsTruthy(entry.Get("customTitle")) {
		value, e := protocol.JavaScriptStringJSON(entry.Get("customTitle"))
		if e != nil {
			return e
		}
		s.customTitle = value
	}
	if !jsTruthy(s.goalTitle) {
		s.goalTitle = sessionprojection.LocalIndexGoalTitle(entry)
	}
	if kind == "ai-title" && jsTruthy(entry.Get("aiTitle")) {
		value, e := protocol.JavaScriptStringJSON(entry.Get("aiTitle"))
		if e != nil {
			return e
		}
		clean := sessionprojection.LocalIndexTitleText(value)
		if jsTruthy(clean) {
			s.aiTitle = clean
		}
	}
	if !jsTruthy(s.firstUserTitle) && kind == "user" && !jsTruthy(entry.Get("isMeta")) && message.String("role") == "user" {
		value, e := sessionprojection.LocalIndexUserTitle(message.Get("content"))
		if e != nil {
			return e
		}
		s.firstUserTitle = value
	}
	return nil
}
func cloneEnumerable(value json.RawMessage) json.RawMessage {
	if len(value) > 0 && value[0] == '[' {
		var array []json.RawMessage
		_ = json.Unmarshal(value, &array)
		o := protocol.NewJSONObject()
		for n, v := range array {
			o.Set(fmt.Sprint(n), v)
		}
		return raw(o)
	}
	return value
}
func (s *reducerState) summary() protocol.JSONObject {
	title := raw("Untitled Session")
	for _, v := range []json.RawMessage{s.customTitle, s.goalTitle, s.aiTitle, s.firstUserTitle} {
		if jsTruthy(v) {
			title = v
			break
		}
	}
	created := s.fallbackCreated
	if s.hasCreated {
		created = s.createdAt
	}
	modified := s.fallbackModified
	if s.semanticModified != nil {
		modified = raw(*s.semanticModified)
	}
	workDir := s.fallbackWorkDir
	for _, v := range []json.RawMessage{s.workDir, s.cwd} {
		if jsTruthy(v) {
			workDir = v
			break
		}
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: title}, protocol.JSONField{Name: "createdAt", Value: created}, protocol.JSONField{Name: "modifiedAt", Value: modified}, protocol.JSONField{Name: "messageCount", Value: raw(s.messageCount)}, protocol.JSONField{Name: "workDir", Value: workDir})
	for _, f := range []protocol.JSONField{{Name: "permissionMode", Value: s.permission}, {Name: "runtimeModelId", Value: s.model}, {Name: "effortLevel", Value: s.effort}} {
		if jsTruthy(f.Value) {
			out.Set(f.Name, f.Value)
		}
	}
	if s.fastMode != nil {
		out.Set("openAIFastMode", s.fastMode)
	}
	if len(s.provider) > 0 {
		out.Set("runtimeProviderId", s.provider)
	}
	if s.teamWorker {
		out.Set("isTeamWorker", raw(true))
	}
	if jsTruthy(s.repository) {
		out.Set("repository", cloneEnumerable(s.repository))
	}
	if len(s.worktree) > 0 {
		value := s.worktree
		if jsTruthy(value) {
			value = cloneEnumerable(value)
		}
		out.Set("worktreeSession", value)
	}
	return out
}
func (s *reducerState) claim(entry protocol.JSONObject, suffix string) bool {
	key, available := activitystatsapi.IndexUsageKey(entry)
	if !available {
		return true
	}
	key += suffix
	if s.usageKeys[key] {
		return false
	}
	s.usageKeys[key] = true
	return true
}
func nullish(v json.RawMessage) json.RawMessage {
	if len(v) == 0 || string(v) == "null" {
		return raw(0)
	}
	return v
}
func (s *reducerState) accumulateUsage(date, model string, usage protocol.JSONObject) error {
	key := date + "\x00" + model
	aggregate, exists := s.models[key]
	if !exists {
		aggregate = ActivityModel{Fields: protocol.NewJSONObject(protocol.JSONField{Name: "date", Value: raw(date)}, protocol.JSONField{Name: "model", Value: raw(model)})}
		for _, field := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "contextWindow", "maxOutputTokens"} {
			aggregate.Fields.Set(field, raw(0))
		}
	}
	creation := nullish(usage.Get("cache_creation_input_tokens"))
	if split := usage.Get("cache_creation"); jsTruthy(split) {
		fields := objectValue(split)
		creation = activitystatsapi.IndexAdd(nullish(fields.Get("ephemeral_5m_input_tokens")), nullish(fields.Get("ephemeral_1h_input_tokens")))
	}
	measure := protocol.NewJSONObject()
	for _, f := range [][2]string{{"inputTokens", "input_tokens"}, {"outputTokens", "output_tokens"}, {"cacheReadInputTokens", "cache_read_input_tokens"}} {
		measure.Set(f[0], nullish(usage.Get(f[1])))
	}
	measure.Set("cacheCreationInputTokens", creation)
	measure.Set("webSearchRequests", nullish(objectValue(usage.Get("server_tool_use")).Get("web_search_requests")))
	for _, field := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests"} {
		aggregate.Fields.Set(field, activitystatsapi.IndexAdd(aggregate.Fields.Get(field), measure.Get(field)))
	}
	cost, priced, e := activitystatsapi.IndexEstimateCost(model, measure, usage.String("speed"))
	if e != nil {
		return e
	}
	if priced {
		aggregate.CostUSD += cost
	}
	s.models[key] = aggregate
	return nil
}
func incrementNamed(values map[string]NamedUsage, date string, name json.RawMessage) {
	if !jsonStringValue(name) {
		return
	}
	normalized := trimJS(stringValue(name))
	if normalized == "" {
		return
	}
	key := date + "\x00" + normalized
	current := values[key]
	if current.Count == 0 {
		current.Date = date
		current.Name = normalized
	}
	current.Count++
	values[key] = current
}
func activityDate(milliseconds float64) string {
	t := time.UnixMilli(int64(milliseconds)).UTC()
	year := t.Year()
	date := t.Format("2006-01-02")
	if year < 0 || year > 9999 {
		date = fmt.Sprintf("%+07d-%02d-%02d", year, t.Month(), t.Day())
	}
	if len(date) > 10 {
		date = date[:10]
	}
	return date
}
func (s *reducerState) applyActivity(entry protocol.JSONObject, o ReductionOptions) error {
	kind := entry.String("type")
	if kind == "speculation-accept" {
		v := entry.Get("timeSavedMs")
		if len(v) > 0 && !jsonStringValue(v) && string(v) != "null" && string(v) != "true" && string(v) != "false" {
			var n float64
			if json.Unmarshal(v, &n) == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
				s.activity.SpeculationTimeSavedMs += n
			}
		}
		return nil
	}
	message := objectValue(entry.Get("message"))
	if s.activity.ShotCount == nil && kind == "assistant" {
		count, found := activitystatsapi.IndexShotCount(message.Get("content"))
		if found {
			s.activity.ShotCount = &count
		}
	}
	if !oneOf(entry.Get("type"), "user", "assistant", "attachment", "system") || !s.activity.IsSubagent && string(entry.Get("isSidechain")) == "true" {
		return nil
	}
	stamp := entry.Get("timestamp")
	ms, valid := 0.0, false
	var e error
	if jsonStringValue(stamp) {
		ms, valid, e = o.ParseDate(stringValue(stamp))
		if e != nil {
			return e
		}
	}
	if !s.sawActivity {
		s.sawActivity = true
		s.firstValid = valid
		if valid {
			v := stringValue(stamp)
			s.activity.FirstTimestamp = &v
			hour := time.UnixMilli(int64(ms)).In(o.Location).Hour()
			s.activity.StartHour = &hour
		}
	}
	s.lastValid = valid
	s.activity.LastTimestamp = nil
	if valid {
		v := stringValue(stamp)
		s.activity.LastTimestamp = &v
	}
	s.activity.MessageCount++
	if valid {
		if s.lastMessageTime != nil {
			gap := ms - *s.lastMessageTime
			if gap > 0 && gap <= 1800000 {
				s.activity.ActiveDurationMs += gap
			}
		}
		s.lastMessageTime = &ms
	}
	if !valid {
		return nil
	}
	date := activityDate(ms)
	day, exists := s.days[date]
	if !exists {
		day.Date = date
	}
	if !s.activity.IsSubagent {
		day.MessageCount++
	}
	if kind != "assistant" {
		s.days[date] = day
		return nil
	}
	var blocks []json.RawMessage
	_ = json.Unmarshal(message.Get("content"), &blocks)
	for _, block := range blocks {
		fields := objectValue(block)
		if fields.String("type") != "tool_use" {
			continue
		}
		day.ToolCallCount++
		incrementNamed(s.tools, date, fields.Get("name"))
		if fields.String("name") == "Skill" {
			incrementNamed(s.skills, date, objectValue(fields.Get("input")).Get("skill"))
		}
	}
	s.days[date] = day
	usageRaw := message.Get("usage")
	model := message.Get("model")
	if !jsTruthy(model) {
		model = raw("unknown")
	}
	if !jsTruthy(usageRaw) || stringValue(model) == "<synthetic>" || !activitystatsapi.IndexBillableUsage(entry) || !s.claim(entry, "") {
		return nil
	}
	if !jsonStringValue(model) {
		return errors.New("model.trim is not a function")
	}
	usage := objectValue(usageRaw)
	if e := s.accumulateUsage(date, stringValue(model), usage); e != nil {
		return e
	}
	var iterations []json.RawMessage
	_ = json.Unmarshal(usage.Get("iterations"), &iterations)
	advisorIndex := 0
	for _, iteration := range iterations {
		fields := objectValue(iteration)
		if fields.String("type") != "advisor_message" || !jsonStringValue(fields.Get("model")) || !jsTruthy(fields.Get("model")) {
			continue
		}
		index := advisorIndex
		advisorIndex++
		if !s.claim(entry, fmt.Sprintf("\x00advisor:%d", index)) {
			continue
		}
		if e := s.accumulateUsage(date, fields.String("model"), fields); e != nil {
			return e
		}
	}
	return nil
}
func sortActivity(a *ActivityProjection, compare func(string, string) int) {
	sort.SliceStable(a.Daily, func(i, j int) bool { return compare(a.Daily[i].Date, a.Daily[j].Date) < 0 })
	sort.SliceStable(a.Models, func(i, j int) bool {
		left, right := a.Models[i].Fields, a.Models[j].Fields
		if n := compare(stringValue(left.Get("date")), stringValue(right.Get("date"))); n != 0 {
			return n < 0
		}
		return compare(stringValue(left.Get("model")), stringValue(right.Get("model"))) < 0
	})
	for _, values := range [][]NamedUsage{a.Tools, a.Skills} {
		sort.SliceStable(values, func(i, j int) bool {
			if n := compare(values[i].Date, values[j].Date); n != 0 {
				return n < 0
			}
			return compare(values[i].Name, values[j].Name) < 0
		})
	}
}
func reductionError() error {
	return coded("LOCAL_INDEX_REBUILD_REQUIRED", "Transcript projection state is unavailable; rebuild this source from byte zero")
}
func chunkLength(c TranscriptChunk) int64 {
	if c.ByteLength != nil {
		return *c.ByteLength
	}
	return int64(len(c.Text))
}
func ReduceTranscript(chunks []TranscriptChunk, seed *TranscriptProjection, o ReductionOptions) (*TranscriptProjection, []EntryLocator, error) {
	if o.ParseDate == nil || o.Compare == nil {
		return nil, nil, errors.New("Transcript reduction requires real native Date and string comparison primitives")
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.Platform == "" {
		return nil, nil, errors.New("Transcript reduction requires its explicit host platform")
	}
	full := len(chunks) == 0 || chunks[0].ByteStart == 0
	if len(chunks) > 0 && chunks[0].ByteStart != 0 && (seed == nil || seed.state == nil) {
		return nil, nil, reductionError()
	}
	if len(chunks) > 0 {
		expected := int64(0)
		if !full {
			expected = seed.IndexedBytes
		}
		incomplete := false
		for _, c := range chunks {
			if incomplete || c.ByteStart != expected {
				return nil, nil, reductionError()
			}
			expected += chunkLength(c)
			incomplete = !c.CompleteLine
		}
	}
	if len(chunks) == 0 && seed != nil {
		copy := *seed
		copy.Summary = seed.Summary.Clone()
		if seed.state != nil {
			copy.state = cloneReductionState(seed.state)
		}
		if seed.Activity != nil {
			a := *seed.Activity
			a.Daily = append([]ActivityDay{}, a.Daily...)
			a.Models = append([]ActivityModel{}, a.Models...)
			for n := range a.Models {
				a.Models[n].Fields = a.Models[n].Fields.Clone()
			}
			a.Tools = append([]NamedUsage{}, a.Tools...)
			a.Skills = append([]NamedUsage{}, a.Skills...)
			copy.Activity = &a
		}
		return &copy, []EntryLocator{}, nil
	}
	state := initialReducer(seed, o)
	indexed, malformed := int64(0), int64(0)
	if !full {
		state = cloneReductionState(seed.state)
		indexed = seed.IndexedBytes
		malformed = seed.MalformedLineCount
		if o.IsSubagent != nil && state.activity.IsSubagent != *o.IsSubagent {
			return nil, nil, reductionError()
		}
	}
	pending := int64(0)
	locators := []EntryLocator{}
	for _, c := range chunks {
		end := c.ByteStart + chunkLength(c)
		if !c.CompleteLine {
			pending = end - indexed
			continue
		}
		indexed = end
		pending = 0
		line := state.nextLine
		state.nextLine++
		trimmed := trimJS(c.Text)
		if trimmed == "" {
			continue
		}
		value := json.RawMessage(trimmed)
		if !json.Valid(value) {
			malformed++
			continue
		}
		if string(value) == "null" {
			return nil, nil, errors.New("null is not an object (evaluating 'entry.entrypoint')")
		}
		entry := objectValue(value)
		object := len(value) > 0 && value[0] == '{'
		if object && o.ValidateRetainedMetadata != nil {
			if e := o.ValidateRetainedMetadata(entry); e != nil {
				return nil, nil, e
			}
		}
		if e := state.apply(entry, o); e != nil {
			return nil, nil, e
		}
		if object {
			message := objectValue(entry.Get("message"))
			kind := "unknown"
			if jsonStringValue(entry.Get("type")) {
				kind = entry.String("type")
			}
			var id, role, stamp, parent *string
			if jsonStringValue(entry.Get("uuid")) {
				s := entry.String("uuid")
				id = &s
			} else if jsonStringValue(entry.Get("messageId")) {
				s := entry.String("messageId")
				id = &s
			}
			if jsonStringValue(message.Get("role")) {
				s := message.String("role")
				role = &s
			}
			if jsonStringValue(entry.Get("timestamp")) {
				s := entry.String("timestamp")
				stamp = &s
			}
			if jsonStringValue(entry.Get("parent_tool_use_id")) {
				s := entry.String("parent_tool_use_id")
				parent = &s
			}
			locators = append(locators, EntryLocator{state.nextOrdinal, line, c.ByteStart, chunkLength(c), kind, id, role, stamp, parent})
			state.nextOrdinal++
		}
	}
	activity := state.activity
	activity.Daily = []ActivityDay{}
	activity.Models = []ActivityModel{}
	activity.Tools = []NamedUsage{}
	activity.Skills = []NamedUsage{}
	if state.firstValid && state.lastValid {
		for _, v := range state.days {
			activity.Daily = append(activity.Daily, v)
		}
		for _, v := range state.models {
			activity.Models = append(activity.Models, v)
		}
		for _, v := range state.tools {
			activity.Tools = append(activity.Tools, v)
		}
		for _, v := range state.skills {
			activity.Skills = append(activity.Skills, v)
		}
		sortActivity(&activity, o.Compare)
	} else {
		activity.FirstTimestamp = nil
		activity.LastTimestamp = nil
		activity.ActiveDurationMs = 0
		activity.MessageCount = 0
		activity.StartHour = nil
	}
	projection := &TranscriptProjection{state.summary(), indexed, pending, malformed, &activity, cloneReductionState(state)}
	return projection, locators, nil
}
