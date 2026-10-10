package sessionprojection

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type state struct {
	workDir, cwd, repository, worktreeSession                            json.RawMessage
	permissionMode, runtimeProviderID, runtimeModelID, effortLevel       json.RawMessage
	customTitle, nonemptyCustomTitle, goalTitle, aiTitle, firstUserTitle json.RawMessage
	createdAt, modifiedAt                                                json.RawMessage
	count, launchCount                                                   int
	teamWorker                                                           bool
}

func null() json.RawMessage { return json.RawMessage("null") }
func newState() state {
	return state{customTitle: null(), nonemptyCustomTitle: null(), goalTitle: null(), aiTitle: null(), firstUserTitle: null(), createdAt: null(), modifiedAt: null()}
}
func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	if raw = bytes.TrimSpace(raw); len(raw) != 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}
func stringUnits(raw json.RawMessage) ([]uint16, bool) {
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '"' {
		return nil, false
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	return units, err == nil
}
func equal(raw json.RawMessage, value string) bool {
	units, ok := stringUnits(raw)
	if !ok || len(units) != len(value) {
		return false
	}
	for index, char := range value {
		if units[index] != uint16(char) {
			return false
		}
	}
	return true
}
func quoted(units []uint16) json.RawMessage { return protocol.QuoteJavaScriptUTF16(units) }
func text(value string) json.RawMessage {
	return quoted(utf16.Encode([]rune(value)))
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, _ := stringUnits(raw)
		return len(units) != 0
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number != 0
	}
	return true
}
func isNull(raw json.RawMessage) bool      { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func stringValue(raw json.RawMessage) bool { _, ok := stringUnits(raw); return ok }
func oneOf(raw json.RawMessage, values ...string) bool {
	for _, value := range values {
		if equal(raw, value) {
			return true
		}
	}
	return false
}
func normalizeDrive(raw json.RawMessage, platform string) json.RawMessage {
	units, _ := stringUnits(raw)
	if platform == "win32" && len(units) == 2 && asciiLetter(units[0]) && units[1] == ':' {
		return quoted(append(units, '\\'))
	}
	return raw
}
func (service *Service) latest(current, candidate json.RawMessage) (json.RawMessage, error) {
	units, ok := stringUnits(candidate)
	if !ok {
		return current, nil
	}
	millis, valid, err := service.options.ParseDate(units)
	if err != nil || !valid {
		return current, err
	}
	if !truthy(current) {
		return candidate, nil
	}
	previous, _ := stringUnits(current)
	previousMillis, previousValid, err := service.options.ParseDate(previous)
	if err != nil {
		return current, err
	}
	if !previousValid || millis > previousMillis {
		return candidate, nil
	}
	return current, nil
}
func (service *Service) apply(state *state, entry protocol.JSONObject) error {
	stamp := entry.Get("timestamp")
	message := object(entry.Get("message"))
	role := message.Get("role")
	typeRaw := entry.Get("type")
	meta := truthy(entry.Get("isMeta"))
	if !truthy(state.createdAt) && truthy(stamp) {
		state.createdAt = stamp
	}
	if oneOf(typeRaw, "user", "assistant") && truthy(role) {
		state.count++
		if !meta {
			var err error
			state.modifiedAt, err = service.latest(state.modifiedAt, stamp)
			if err != nil {
				return err
			}
		}
	}
	if !meta && truthy(role) && oneOf(typeRaw, "user", "assistant", "system") {
		state.launchCount++
	}
	if cwd := entry.Get("cwd"); stringValue(cwd) {
		units, _ := stringUnits(cwd)
		if len(trim(units)) != 0 {
			state.cwd = normalizeDrive(cwd, service.options.Platform)
		}
	}
	if equal(entry.Get("entrypoint"), "claude-desktop-team-worker") {
		state.teamWorker = true
	}
	if equal(typeRaw, "session-meta") {
		if workDir := entry.Get("workDir"); stringValue(workDir) {
			state.workDir = normalizeDrive(workDir, service.options.Platform)
		}
		if mode := entry.Get("permissionMode"); oneOf(mode, "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto") {
			state.permissionMode = mode
		}
		provider, model := entry.Get("runtimeProviderId"), entry.Get("runtimeModelId")
		if isNull(provider) || stringValue(provider) {
			state.runtimeProviderID = provider
		}
		if stringValue(model) {
			state.runtimeModelID = model
		}
		state.effortLevel = ResolveSessionEffortLevel(entry, state.effortLevel)
	}
	if repository := bytes.TrimSpace(entry.Get("repository")); len(repository) != 0 && (repository[0] == '{' || repository[0] == '[') {
		state.repository = repository
	}
	if equal(typeRaw, "worktree-state") {
		worktree := entry.Get("worktreeSession")
		fields := object(worktree)
		if isNull(worktree) || stringValue(fields.Get("worktreePath")) && stringValue(fields.Get("worktreeName")) {
			state.worktreeSession = worktree
		}
	}
	if equal(typeRaw, "custom-title") {
		if title := entry.Get("customTitle"); stringValue(title) {
			state.customTitle = title
			units, _ := stringUnits(title)
			if len(trim(units)) != 0 {
				state.nonemptyCustomTitle = title
			}
		}
	}
	if isNull(state.goalTitle) {
		state.goalTitle = goalTitle(entry)
	}
	if equal(typeRaw, "ai-title") && truthy(entry.Get("aiTitle")) {
		raw, err := protocol.JavaScriptStringJSON(entry.Get("aiTitle"))
		if err != nil {
			return err
		}
		units, _ := stringUnits(raw)
		if cleaned := cleanTitle(units); len(cleaned) != 0 {
			state.aiTitle = quoted(cleaned)
		}
	}
	if !truthy(state.firstUserTitle) && equal(typeRaw, "user") && !meta && equal(role, "user") {
		var err error
		state.firstUserTitle, err = userTitle(message.Get("content"))
		if err != nil {
			return err
		}
	}
	raw, err := state.serialize()
	if err != nil {
		return err
	}
	if len(raw) > 128*1024 {
		return &sessionstore.APIError{StatusCode: 413, Code: "SESSION_METADATA_TOO_LARGE", Message: "Session metadata exceeds its resource budget"}
	}
	return nil
}

// ResolveSessionEffortLevel is the original shared transcript/launch policy.
// Complete runtime selections replace effort; historical partial records patch it.
func ResolveSessionEffortLevel(entry protocol.JSONObject, previous json.RawMessage) json.RawMessage {
	if effort := entry.Get("effortLevel"); oneOf(effort, "low", "medium", "high", "xhigh", "max") {
		return effort
	}
	provider, model := entry.Get("runtimeProviderId"), entry.Get("runtimeModelId")
	if (isNull(provider) || stringValue(provider)) && stringValue(model) && truthy(model) {
		return nil
	}
	return previous
}
func (state state) serialize() ([]byte, error) {
	fields := protocol.NewJSONObject()
	for _, field := range []protocol.JSONField{
		{Name: "workDir", Value: state.workDir}, {Name: "cwd", Value: state.cwd}, {Name: "repository", Value: state.repository}, {Name: "worktreeSession", Value: state.worktreeSession},
		{Name: "permissionMode", Value: state.permissionMode}, {Name: "runtimeProviderId", Value: state.runtimeProviderID}, {Name: "runtimeModelId", Value: state.runtimeModelID}, {Name: "effortLevel", Value: state.effortLevel},
		{Name: "customTitle", Value: state.customTitle}, {Name: "nonemptyCustomTitle", Value: state.nonemptyCustomTitle}, {Name: "goalTitle", Value: state.goalTitle}, {Name: "aiTitle", Value: state.aiTitle}, {Name: "firstUserTitle", Value: state.firstUserTitle},
		{Name: "createdAt", Value: state.createdAt}, {Name: "modifiedAt", Value: state.modifiedAt},
		{Name: "count", Value: json.RawMessage(strconv.Itoa(state.count))}, {Name: "launchCount", Value: json.RawMessage(strconv.Itoa(state.launchCount))}, {Name: "isTeamWorker", Value: json.RawMessage(strconv.FormatBool(state.teamWorker))},
	} {
		if field.Value != nil {
			fields.Set(field.Name, field.Value)
		}
	}
	raw, err := fields.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
func shared(fields *protocol.JSONObject, state state) {
	for _, field := range []protocol.JSONField{{Name: "permissionMode", Value: state.permissionMode}, {Name: "runtimeProviderId", Value: state.runtimeProviderID}, {Name: "runtimeModelId", Value: state.runtimeModelID}, {Name: "effortLevel", Value: state.effortLevel}, {Name: "repository", Value: state.repository}, {Name: "worktreeSession", Value: state.worktreeSession}} {
		if field.Value != nil && (field.Name == "runtimeProviderId" || field.Name == "worktreeSession" || truthy(field.Value)) {
			fields.Set(field.Name, field.Value)
		}
	}
}
func firstTruthy(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if truthy(value) {
			return value
		}
	}
	return nil
}
func (service *Service) result(launch, summary state, filePath, projectDir string, stat Stat) Result {
	fallback := text(desanitize(projectDir, service.options.Platform))
	summaryFields := protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: firstTruthy(summary.customTitle, summary.goalTitle, summary.aiTitle, summary.firstUserTitle, text("Untitled Session"))})
	created, modified := summary.createdAt, summary.modifiedAt
	if isNull(created) {
		created = text(stat.BirthtimeISO)
	}
	if isNull(modified) {
		modified = text(stat.MtimeISO)
	}
	summaryFields.Set("createdAt", created)
	summaryFields.Set("modifiedAt", modified)
	summaryFields.Set("messageCount", json.RawMessage(strconv.Itoa(summary.count)))
	if summary.teamWorker {
		summaryFields.Set("isTeamWorker", json.RawMessage("true"))
	}
	summaryWorkDir := firstTruthy(summary.workDir, summary.cwd)
	if summaryWorkDir == nil {
		summaryWorkDir = fallback
	}
	summaryFields.Set("workDir", summaryWorkDir)
	shared(&summaryFields, summary)
	workDir := launch.workDir
	if workDir == nil {
		workDir = launch.cwd
		if workDir == nil {
			workDir = fallback
		}
	}
	if !truthy(workDir) {
		workDir = text(service.options.Cwd())
	}
	launchFields := protocol.NewJSONObject(protocol.JSONField{Name: "filePath", Value: text(filePath)}, protocol.JSONField{Name: "projectDir", Value: text(projectDir)}, protocol.JSONField{Name: "workDir", Value: workDir}, protocol.JSONField{Name: "customTitle", Value: launch.customTitle}, protocol.JSONField{Name: "transcriptMessageCount", Value: json.RawMessage(strconv.Itoa(launch.launchCount))})
	shared(&launchFields, launch)
	summaryRaw, _ := summaryFields.MarshalJSON()
	launchRaw, _ := launchFields.MarshalJSON()
	return Result{Summary: summaryRaw, LaunchInfo: launchRaw, CustomTitle: launch.nonemptyCustomTitle}
}
