package diagnosticsapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// These are the same 36 curated gitleaks expressions used by secretScanner.ts.
//
//go:embed secret_rules.json
var secretRulesJSON []byte
var secretRules = func() []*regexp2.Regexp {
	var rows []struct{ Source, Flags string }
	if err := json.Unmarshal(secretRulesJSON, &rows); err != nil {
		panic(err)
	}
	result := []*regexp2.Regexp{}
	for _, row := range rows {
		options := regexp2.ECMAScript
		if strings.Contains(row.Flags, "i") {
			options |= regexp2.IgnoreCase
		}
		result = append(result, regexp2.MustCompile(row.Source, options))
	}
	return result
}()

func redactSecrets(value string) string {
	for _, pattern := range secretRules {
		redacted, err := pattern.ReplaceFunc(value, func(match regexp2.Match) string {
			if group := match.GroupByNumber(1); group != nil && len(group.Captures) > 0 {
				return strings.Replace(match.String(), group.String(), "[REDACTED]", 1)
			}
			return "[REDACTED]"
		}, -1, -1)
		if err != nil {
			return "[REDACTED]"
		}
		value = redacted
	}
	return value
}
func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, word := range strings.Fields(words) {
		m[word] = true
	}
	return m
}

var contentKeys = setOf("assistanttext body capturedoutput content filecontent filecontents input message messagetext output prompt response result sdkmessages text toolinput tooloutput transcript")
var safeKeys = setOf("activeatfinish activerequests arraybuffersmib attempts code concurrentatstart cpusystemms cpuuserms declaredbytes declaredresponsebytes durationms errorcategory errorcode fetchms externalmib hardwareconcurrency heapusedmib is_error isapierrormessage iserror lagms method name recovered recoveryms requestid route responsechars responsereadms rssmib sdktype serverappms status subtype suppressedsincelast timeoutms totaljsheapmib usedjsheapmib")
var errorNames = setOf("AggregateError Error EvalError RangeError ReferenceError SyntaxError TypeError URIError")
var safeMetadata = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_.:/ -]{0,127}$`)
var safeRoute = regexp.MustCompile(`(?i)^/api(?:/[a-z0-9_.:-]{1,64}){1,8}$`)
var uuidRoute = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f-]{27,}$`)
var toolRoute = regexp.MustCompile(`(?i)^(?:call|toolu)_[a-z0-9_-]{12,}$`)
var sharedURL = regexp.MustCompile(`(?i)https?://[^\s<>"')\]}]+`)
var legacySecret = regexp.MustCompile(`\b(?:sk-ant-api03-|sk-proj-|ghp_)[A-Za-z0-9_-]+\b`)
var email = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
var sharedBearer = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+`)
var sharedAWS = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
var sharedPath = regexp.MustCompile(`(?:/Users|/home|/private|/var/folders|/tmp)/[^\s<>"')\]}]+`)
var sharedWindows = regexp.MustCompile(`(?i)\b[A-Z]:\\(?:[^\s<>"')\]}]+\\)*[^\s<>"')\]}]*`)
var sharedControls = regexp.MustCompile(`[\x00-\x1f\x7f-\x{009f}]+`)
var whitespace = regexp.MustCompile(`\s+`)

func sharedString(value string, max int) string {
	value = redactSecrets(legacySecret.ReplaceAllString(value, "[REDACTED]"))
	value = sharedURL.ReplaceAllStringFunc(value, func(candidate string) string {
		u, err := url.Parse(candidate)
		if err != nil || u.Hostname() == "" {
			return "[REDACTED_URL]"
		}
		return u.Hostname()
	})
	value = sharedBearer.ReplaceAllString(value, "Bearer [REDACTED]")
	value = email.ReplaceAllString(value, "[REDACTED_EMAIL]")
	value = sharedAWS.ReplaceAllString(value, "[REDACTED_AWS_ACCESS_KEY]")
	value = sharedPath.ReplaceAllString(value, "[REDACTED_PATH]")
	value = sharedWindows.ReplaceAllString(value, "[REDACTED_PATH]")
	value = strings.TrimSpace(whitespace.ReplaceAllString(sharedControls.ReplaceAllString(value, " "), " "))
	units := utf16.Encode([]rune(value))
	if len(units) > max {
		return string(utf16.Decode(units[:max])) + "...[TRUNCATED]"
	}
	return value
}
func sharedID(value, fallback string, max int) string {
	value = sharedString(value, max)
	if value == "" {
		return fallback
	}
	return value
}

type SharedEvent struct {
	ID            string               `json:"id"`
	Timestamp     string               `json:"timestamp"`
	Type          string               `json:"type"`
	Severity      string               `json:"severity"`
	SessionID     string               `json:"sessionId,omitempty"`
	Details       *protocol.JSONObject `json:"details,omitempty"`
	OmittedFields []string             `json:"omittedFields"`
}

func ShareEvent(event Event) SharedEvent {
	omitted := []string{"summary"}
	details := projectDetails(event.Details, "details", &omitted)
	sort.Strings(omitted)
	unique := []string{}
	for _, field := range omitted {
		if len(unique) == 0 || unique[len(unique)-1] != field {
			unique = append(unique, field)
		}
	}
	session := ""
	if event.SessionID != "" {
		session = sharedID(event.SessionID, "unknown-session", 256)
	}
	return SharedEvent{sharedID(event.ID, "unknown-event", 256), sharedID(event.Timestamp, "unknown-time", 64), sharedID(event.Type, "unknown-event-type", 128), event.Severity, session, details, unique}
}
func projectDetails(raw json.RawMessage, path string, omitted *[]string) *protocol.JSONObject {
	var object protocol.JSONObject
	if json.Unmarshal(raw, &object) != nil || !object.IsObject() {
		if raw != nil {
			*omitted = append(*omitted, path)
		}
		return nil
	}
	result := protocol.NewJSONObject()
	set := func(key string, value json.RawMessage) {
		if key != "__proto__" {
			result.Set(key, value)
		}
	}
	for _, prop := range object.OwnProperties() {
		var key string
		_ = json.Unmarshal(prop.NameJSON, &key)
		entryPath := path + "." + key
		normal := strings.ToLower(key)
		if contentKeys[normal] {
			*omitted = append(*omitted, entryPath)
			continue
		}
		var nested protocol.JSONObject
		if json.Unmarshal(prop.Value, &nested) == nil && nested.IsObject() {
			if isSerializedError(nested) {
				*omitted = append(*omitted, entryPath+".message", entryPath+".stack")
				name := nested.String("name")
				if !errorNames[name] {
					name = "UnknownError"
				}
				set(key, []byte(fmt.Sprintf(`{"name":%q}`, name)))
				continue
			}
			if value := projectDetails(prop.Value, entryPath, omitted); value != nil {
				b, _ := json.Marshal(value)
				set(key, b)
			}
			continue
		}
		var scalar any
		_ = json.Unmarshal(prop.Value, &scalar)
		switch v := scalar.(type) {
		case nil, bool, float64, string:
			if safeKeys[normal] {
				if text, ok := v.(string); ok {
					if normal == "route" {
						v = projectRoute(text)
					} else {
						sanitized := sharedString(text, 512)
						if !safeMetadata.MatchString(sanitized) {
							sanitized = "[REDACTED]"
						}
						v = sanitized
					}
				}
				b, _ := json.Marshal(v)
				set(key, b)
				continue
			}
		}
		*omitted = append(*omitted, entryPath)
	}
	if len(result.OwnProperties()) == 0 {
		return nil
	}
	return &result
}
func isSerializedError(object protocol.JSONObject) bool {
	if object.Get("name") == nil || object.Get("message") == nil {
		return false
	}
	if object.Get("name")[0] != '"' || object.Get("message")[0] != '"' {
		return false
	}
	var name, message string
	if json.Unmarshal(object.Get("name"), &name) != nil || json.Unmarshal(object.Get("message"), &message) != nil {
		return false
	}
	for _, p := range object.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		if key != "name" && key != "message" && key != "stack" {
			return false
		}
	}
	return true
}
func projectRoute(value string) string {
	if !safeRoute.MatchString(value) {
		return "[REDACTED]"
	}
	segments := strings.Split(value, "/")
	for i, segment := range segments {
		if uuidRoute.MatchString(segment) || toolRoute.MatchString(segment) {
			segments[i] = ":id"
		} else if redactSecrets(segment) != segment {
			segments[i] = ":redacted"
		}
	}
	return strings.Join(segments, "/")
}
func newestShared(events []Event, max int) []SharedEvent {
	result := []SharedEvent{}
	for i := len(events) - 1; i >= 0 && len(result) < max; i-- {
		result = append(result, ShareEvent(events[i]))
	}
	return result
}
func sharedRuntime(events []SharedEvent) string {
	entries := []string{}
	for _, e := range events {
		if e.Severity != "warn" && e.Severity != "error" {
			continue
		}
		head := "[" + e.Timestamp + "] " + strings.ToUpper(e.Severity) + " " + e.Type
		if e.SessionID != "" {
			head += " session=" + e.SessionID
		}
		if e.Details != nil {
			head += "\n" + strings.TrimSuffix(prettyJSON(e.Details), "\n")
		}
		if len(e.OmittedFields) > 0 {
			head += "\nomittedFields: " + strings.Join(e.OmittedFields, ", ")
		}
		entries = append(entries, head)
	}
	if len(entries) == 0 {
		return ""
	}
	return strings.Join(entries, "\n\n") + "\n"
}
func (s *Service) errorSummary(events []SharedEvent) string {
	lines := []string{"# cc-haha recent diagnostics", "", "Generated: " + iso(s.options.Now())}
	errors := []SharedEvent{}
	for _, e := range events {
		if e.Severity == "warn" || e.Severity == "error" {
			errors = append(errors, e)
			if len(errors) == 50 {
				break
			}
		}
	}
	lines = append(lines, fmt.Sprintf("Events included: %d", len(errors)), "")
	if len(errors) == 0 {
		lines = append(lines, "No recent warnings or errors were recorded.", "")
	} else {
		for _, e := range errors {
			lines = append(lines, "## "+e.Timestamp+" "+strings.ToUpper(e.Severity)+" "+e.Type)
			if e.SessionID != "" {
				lines = append(lines, "session: "+e.SessionID)
			}
			lines = append(lines, "")
			if e.Details != nil {
				lines = append(lines, "", "```json", strings.TrimSuffix(prettyJSON(e.Details), "\n"), "```")
			}
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}
func providerScalar(v any) any {
	switch value := v.(type) {
	case nil, bool, float64:
		return value
	case string:
		return sharedID(value, "[REDACTED]", 256)
	}
	return nil
}
func (s *Service) providers() map[string]any {
	b, err := os.ReadFile(filepath.Join(s.options.ConfigDir, "cc-haha", "providers.json"))
	if os.IsNotExist(err) {
		return map[string]any{"activeId": nil, "count": 0, "providers": []any{}}
	}
	if err != nil {
		return map[string]any{"error": s.SanitizeString(err.Error(), 4096)}
	}
	var object map[string]any
	if err = json.Unmarshal(b, &object); err != nil {
		return map[string]any{"error": s.SanitizeString(err.Error(), 4096)}
	}
	var ordered protocol.JSONObject
	_ = json.Unmarshal(b, &ordered)
	var rawProviders []json.RawMessage
	_ = json.Unmarshal(ordered.Get("providers"), &rawProviders)
	if providers, ok := object["providers"].([]any); ok {
		for i, raw := range rawProviders {
			if i >= len(providers) {
				break
			}
			if provider, ok := providers[i].(map[string]any); ok {
				var original protocol.JSONObject
				_ = json.Unmarshal(raw, &original)
				var models protocol.JSONObject
				if json.Unmarshal(original.Get("models"), &models) == nil && models.IsObject() {
					provider["models"] = models
				}
			}
		}
	}
	return projectProviders(object)
}
func projectProviders(object map[string]any) map[string]any {
	providers := []any{}
	rows, _ := object["providers"].([]any)
	for _, row := range rows {
		provider, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if len(providers) == 100 {
			break
		}
		var base any
		if raw, ok := provider["baseUrl"].(string); ok {
			parsed, err := url.Parse(raw)
			if err == nil && parsed.Scheme != "" && parsed.Hostname() != "" {
				base = map[string]string{"hostname": sharedID(parsed.Hostname(), "[REDACTED]", 256)}
			}
		} else if raw, ok := provider["baseUrl"].(map[string]any); ok {
			if hostname, ok := raw["hostname"].(string); ok {
				base = map[string]string{"hostname": sharedID(hostname, "[REDACTED]", 256)}
			}
		}
		models := protocol.NewJSONObject()
		var originalModels protocol.JSONObject
		if raw, ok := provider["models"].(protocol.JSONObject); ok {
			originalModels = raw
		} else {
			data, _ := json.Marshal(provider["models"])
			_ = json.Unmarshal(data, &originalModels)
		}
		if originalModels.IsObject() {
			for _, property := range originalModels.OwnProperties() {
				var key, v string
				if json.Unmarshal(property.NameJSON, &key) != nil || json.Unmarshal(property.Value, &v) != nil || string(property.Value) == "null" {
					continue
				}
				if len(models.OwnProperties()) == 40 {
					break
				}
				redacted := false
				for _, re := range secretRules {
					if match, err := re.MatchString(v); err != nil || match {
						redacted = true
						break
					}
				}
				if redacted {
					models.Set(sharedID(key, "unknown-model-key", 128), json.RawMessage(`"[REDACTED]"`))
				} else {
					value, _ := json.Marshal(sharedID(v, "[REDACTED]", 256))
					models.Set(sharedID(key, "unknown-model-key", 128), value)
				}
			}
		}
		providers = append(providers, map[string]any{"id": providerScalar(provider["id"]), "name": providerScalar(provider["name"]), "presetId": providerScalar(provider["presetId"]), "apiFormat": providerScalar(provider["apiFormat"]), "baseUrl": base, "models": models})
	}
	return map[string]any{"activeId": providerScalar(object["activeId"]), "count": len(providers), "providers": providers}
}
func (s *Service) sessions(events []SharedEvent) map[string]any {
	type count struct {
		N          int
		Last       string
		Severities []string
	}
	order := []string{}
	m := map[string]*count{}
	for _, event := range events {
		if event.SessionID == "" {
			continue
		}
		v := m[event.SessionID]
		if v == nil {
			v = &count{}
			m[event.SessionID] = v
			order = append(order, event.SessionID)
		}
		v.N++
		if event.Timestamp > v.Last {
			v.Last = event.Timestamp
		}
		found := false
		for _, severity := range v.Severities {
			if severity == event.Severity {
				found = true
			}
		}
		if !found {
			v.Severities = append(v.Severities, event.Severity)
		}
	}
	rows := []any{}
	for _, id := range order {
		v := m[id]
		rows = append(rows, map[string]any{"sessionId": id, "eventCount": v.N, "lastEventAt": v.Last, "severities": v.Severities})
	}
	return map[string]any{"count": len(rows), "sessions": rows, "transcriptContentIncluded": false}
}
func markdown(value string) string {
	var out strings.Builder
	for i, c := range value {
		if c == '@' {
			out.WriteString("&#64;")
		} else if c == '_' {
			word := func(b byte) bool {
				return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
			}
			if i > 0 && i+1 < len(value) && word(value[i-1]) && word(value[i+1]) {
				out.WriteRune(c)
			} else {
				out.WriteString("&#95;")
			}
		} else {
			if strings.ContainsRune("\\`*[]()<>!~", c) {
				out.WriteByte('\\')
			}
			out.WriteRune(c)
		}
	}
	return out.String()
}
func metadata(value any) string {
	switch value.(type) {
	case string, bool, float64, int:
		return markdown(sharedString(fmt.Sprint(value), 512))
	}
	return "unknown"
}
func buildIssueReport(generated string, app, providers map[string]any, events []SharedEvent, corrupt int) string {
	providerLines := []string{}
	rows, _ := providers["providers"].([]any)
	for _, row := range rows {
		p, _ := row.(map[string]any)
		name := p["name"]
		if name == nil {
			name = p["id"]
		}
		hostname := any(nil)
		if base, ok := p["baseUrl"].(map[string]string); ok {
			hostname = base["hostname"]
		}
		models := []string{}
		if m, ok := p["models"].(protocol.JSONObject); ok {
			for _, property := range m.OwnProperties() {
				var key, value string
				_ = json.Unmarshal(property.NameJSON, &key)
				_ = json.Unmarshal(property.Value, &value)
				models = append(models, metadata(key)+"="+metadata(value))
			}
		}
		modelList := strings.Join(models, ", ")
		if modelList == "" {
			modelList = "None"
		}
		providerLines = append(providerLines, "- "+metadata(name)+" | "+metadata(p["apiFormat"])+" | Host: "+metadata(hostname)+" | Models: "+modelList)
	}
	if len(providerLines) == 0 {
		providerLines = []string{"- 未配置 Provider"}
	}
	ids := []string{}
	errorLines := []string{}
	for _, e := range events {
		ids = append(ids, metadata(e.ID))
		if (e.Severity == "error" || e.Severity == "warn") && len(errorLines) < 50 {
			details := []string{}
			if e.Details != nil {
				for _, key := range []string{"errorCode", "status"} {
					var value string
					if raw := e.Details.Get(key); raw != nil && json.Unmarshal(raw, &value) == nil {
						details = append(details, key+"="+metadata(value))
					}
				}
			}
			line := "- " + metadata(e.Timestamp) + " [" + strings.ToUpper(e.Severity) + "] " + metadata(e.Type) + " (" + metadata(e.ID) + ")"
			if len(details) > 0 {
				line += " — " + strings.Join(details, ", ")
			}
			errorLines = append(errorLines, line)
		}
	}
	idLine := strings.Join(ids, ", ")
	if idLine == "" {
		idLine = "None"
	}
	if len(errorLines) == 0 {
		errorLines = []string{"- 未记录最近的警告或错误。"}
	}
	warning := "> 未检测到损坏的诊断记录。"
	if corrupt > 0 {
		warning = fmt.Sprintf("> 警告：检测到 %d 行损坏的诊断记录，以下信息可能不完整。", corrupt)
	}
	lines := []string{"<!-- Generated: " + strings.ReplaceAll(sharedString(generated, 128), "-->", "-- >") + " -->", "", "## 问题描述", "<!-- 请补充 -->", "", "- 期望行为: <!-- 请补充 -->", "- 出现频率: <!-- 请补充 -->", "", "## 运行环境", "- App: " + metadata(app["appVersion"]), "- OS/Arch: " + metadata(app["platform"]) + " / " + metadata(app["arch"]), "- Bun/Node: " + metadata(app["bun"]) + " / " + metadata(app["node"]), "- 安装来源: <!-- 请补充 -->", "", "## Provider / 模型"}
	lines = append(lines, providerLines...)
	lines = append(lines, "", "## 诊断关联", "- Event IDs: "+idLine, fmt.Sprintf("- Corrupt diagnostic lines: %d", corrupt), "", warning, "", "## 复现步骤", "1. <!-- 请补充 -->", "", "## 错误摘要")
	lines = append(lines, errorLines...)
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

const readme = "cc-haha diagnostics bundle\n\nThis bundle is generated by the desktop app for debugging server and CLI startup/runtime failures.\nContent-bearing fields are omitted and secrets/personal data receive best-effort redaction.\nIncluded metadata may contain event ids, timestamps, event types, severity, session ids, error codes/status, provider/model identifiers, and provider hostnames.\nPaths under the current home directory are normalized to \"~\". Long fields are truncated.\nReview every file before sharing because automated redaction cannot guarantee removal of all sensitive data.\n\nFiles:\n- app-info.json: runtime and platform summary.\n- diagnostics.jsonl: sanitized structured diagnostic events.\n- recent-errors.md: human-readable warning/error timeline for GitHub issues.\n- runtime-errors.log: warning/error timeline with projected diagnostic metadata/details.\n- cli-diagnostics.jsonl: sanitized no-PII CLI internal diagnostics emitted by the child process.\n- electron-host.log: sanitized recent Electron host lifecycle diagnostics.\n- providers-summary.json: provider count, active id, base URL host, model ids, and API format without API keys.\n- sessions-summary.json: session ids observed in diagnostic events, without transcript content.\n"
