package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type HistoryFiles struct {
	ReadDir      func(string) ([]fs.DirEntry, error)
	ReadFile     func(string) ([]byte, error)
	Stat         func(string) (fs.FileInfo, error)
	Lstat        func(string) (fs.FileInfo, error)
	Realpath     func(string) (string, error)
	OpenNoFollow func(string) (*os.File, error)
}
type HistoryOptions struct {
	ConfigDir, TempDir string
	Files              HistoryFiles
	Reader             *sessionstore.Reader
	Notifications      func(context.Context, string) ([]protocol.JSONObject, error)
}
type History struct {
	options          HistoryOptions
	projectionReader *ProjectionReader
}

func NewHistory(options HistoryOptions) (*History, error) {
	if !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.TempDir) || options.Files.ReadDir == nil || options.Files.ReadFile == nil || options.Files.Stat == nil || options.Files.Lstat == nil || options.Files.Realpath == nil || options.Files.OpenNoFollow == nil || options.Reader == nil {
		return nil, errors.New("Workflow history requires native disk and bounded transcript services")
	}
	return &History{options: options, projectionReader: NewProjectionReader(nil, options.Reader.Budget())}, nil
}

type sessionDirectory struct{ id, path string }
type launch struct{ runID, taskID, toolUseID, name, ownerID, timestamp string }
type terminal struct{ taskID, toolUseID, ownerID, status, summary, result, outputFile, timestamp string }
type lifecycle struct {
	launches    map[string]launch
	launchOrder []string
	terminals   []terminal
}

func jsonObject(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(raw, &o)
	return o
}
func valueRaw(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func nonempty(o protocol.JSONObject, key string) string {
	value := o.String(key)
	if javascriptTrim(value) == "" {
		return ""
	}
	return value
}
func timestamp(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return float64(parsed.UnixNano()) / 1e6, true
		}
	}
	return 0, false
}
func normalizeStatus(value string) string {
	switch value {
	case "completed", "failed":
		return value
	case "killed", "stopped":
		return "stopped"
	}
	return ""
}
func resultText(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	if text, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		return utf16Text(text)
	}
	raw, _ = protocol.ParseAndStringifyJavaScriptJSON(raw)
	return string(raw)
}

func (h *History) sessionDirs(ctx context.Context, id *string) ([]sessionDirectory, error) {
	root := filepath.Join(h.options.ConfigDir, "projects")
	projects, err := h.options.Files.ReadDir(root)
	if err != nil {
		return []sessionDirectory{}, ctx.Err()
	}
	result := []sessionDirectory{}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		projectRoot := filepath.Join(root, project.Name())
		entries, err := h.options.Files.ReadDir(projectRoot)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		candidates := []string{}
		if id != nil {
			for _, entry := range entries {
				if entry.Name() == *id || entry.Name() == *id+".jsonl" {
					candidates = append(candidates, *id)
					break
				}
			}
		} else {
			for _, entry := range entries {
				candidate := strings.TrimSuffix(entry.Name(), ".jsonl")
				if !seen[candidate] {
					seen[candidate] = true
					candidates = append(candidates, candidate)
				}
			}
		}
		for _, candidate := range candidates {
			dir := filepath.Join(projectRoot, candidate)
			artifact, e := h.options.Files.Stat(dir)
			transcript, t := h.options.Files.Stat(filepath.Join(projectRoot, candidate+".jsonl"))
			if e == nil && artifact.IsDir() || t == nil && transcript.Mode().IsRegular() {
				result = append(result, sessionDirectory{candidate, dir})
			}
		}
	}
	return result, nil
}
func (h *History) runDirs(ctx context.Context, id *string) ([]sessionDirectory, error) {
	sessions, err := h.sessionDirs(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []sessionDirectory{}
	for _, session := range sessions {
		path := filepath.Join(session.path, "workflows")
		stat, err := h.options.Files.Stat(path)
		if err == nil && stat.IsDir() {
			out = append(out, sessionDirectory{session.id, path})
		}
	}
	return out, nil
}

var runScript = regexp.MustCompile(`^(.+)\.(wf_[a-z0-9-]{6,})\.js$`)

func (h *History) journal(ctx context.Context, id, runID string) ([]protocol.JSONObject, error) {
	dirs, err := h.runDirs(ctx, &id)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		raw, err := h.options.Files.ReadFile(filepath.Join(filepath.Dir(dir.path), "subagents", "workflows", runID, "journal.jsonl"))
		if err != nil {
			continue
		}
		result := []protocol.JSONObject{}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			entry := jsonObject(line)
			if entry.String("type") != "result" {
				continue
			}
			key := entry.Get("key")
			units, err := protocol.DecodeJavaScriptJSONString(key)
			if err != nil {
				continue
			}
			hash := sha256.Sum256([]byte(utf16Text(units)))
			item := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: quoted(hex.EncodeToString(hash[:])[:12])}, protocol.JSONField{Name: "agentId", Value: entry.Get("agentId")}, protocol.JSONField{Name: "result", Value: entry.Get("result")})
			result = append(result, item)
		}
		return result, nil
	}
	return []protocol.JSONObject{}, nil
}

// Lifecycle evidence uses the existing admitted, bounded transcript reader.
// A corrupt/oversized/partial record cannot silently produce a complete run.
func (h *History) projection(ctx context.Context, path string) ([]json.RawMessage, error) {
	projection, e := h.projectionReader.Read(ctx, path)
	if e != nil {
		return nil, e
	}
	if !projection.Complete {
		return nil, &APIError{503, "Workflow history exceeds the bounded reconstruction budget", "WORKFLOW_HISTORY_INCOMPLETE"}
	}
	return projection.Messages, nil
}

func workflowLaunch(raw json.RawMessage, base launch) (launch, bool) {
	if text, e := protocol.DecodeJavaScriptJSONString(raw); e == nil {
		value := javascriptTrim(utf16Text(text))
		if !strings.HasPrefix(value, "{") {
			return launch{}, false
		}
		return workflowLaunch([]byte(value), base)
	}
	o := jsonObject(raw)
	if !o.IsObject() {
		return launch{}, false
	}
	runID := nonempty(o, "runId")
	if runID == "" {
		runID = nonempty(o, "workflowRunId")
	}
	taskID := nonempty(o, "taskId")
	if taskID == "" {
		taskID = nonempty(o, "task_id")
	}
	kind := nonempty(o, "taskType")
	if kind == "" {
		kind = nonempty(o, "task_type")
	}
	if strings.HasPrefix(runID, "wf_") && taskID != "" && (kind == "local_workflow" || o.String("status") == "async_launched") {
		base.runID = runID
		base.taskID = taskID
		base.name = nonempty(o, "workflowName")
		if base.name == "" {
			base.name = "workflow"
		}
		return base, true
	}
	for _, field := range o.OwnProperties() {
		if found, ok := workflowLaunch(field.Value, base); ok {
			return found, true
		}
	}
	return launch{}, false
}
func stringsIn(raw json.RawMessage) []string {
	if units, e := protocol.DecodeJavaScriptJSONString(raw); e == nil {
		return []string{utf16Text(units)}
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && array != nil {
		out := []string{}
		for _, value := range array {
			out = append(out, stringsIn(value)...)
		}
		return out
	}
	out := []string{}
	for _, field := range jsonObject(raw).OwnProperties() {
		out = append(out, stringsIn(field.Value)...)
	}
	return out
}
func xml(text, tag string) string {
	match := regexp.MustCompile(`(?is)<` + tag + `>(.*?)</` + tag + `>`).FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return javascriptTrim(match[1])
}
func decodeXML(value string) string {
	for _, pair := range [][2]string{{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", "\""}, {"&apos;", "'"}, {"&amp;", "&"}} {
		value = strings.ReplaceAll(value, pair[0], pair[1])
	}
	return value
}
func collectLifecycle(entry protocol.JSONObject, owner string, out *lifecycle) {
	base := launch{ownerID: owner, timestamp: nonempty(entry, "timestamp")}
	message := jsonObject(entry.Get("message"))
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(message.Get("content"), &blocks)
	candidates := []json.RawMessage{entry.Get("toolUseResult")}
	for _, block := range blocks {
		if block.String("type") == "tool_result" {
			if base.toolUseID == "" {
				base.toolUseID = nonempty(block, "tool_use_id")
			}
			candidates = append(candidates, block.Get("content"))
		}
	}
	for _, candidate := range candidates {
		found, ok := workflowLaunch(candidate, base)
		if !ok {
			continue
		}
		old, exists := out.launches[found.runID]
		oldAt, oldValid := timestamp(old.timestamp)
		newAt, newValid := timestamp(found.timestamp)
		if !exists || newValid && (!oldValid || newAt > oldAt) {
			if !exists {
				out.launchOrder = append(out.launchOrder, found.runID)
			}
			out.launches[found.runID] = found
		}
	}
	if entry.String("type") == "cc-haha-task-notification" {
		notification := jsonObject(entry.Get("taskNotification"))
		status := normalizeStatus(notification.String("status"))
		task := nonempty(notification, "taskId")
		if status != "" && task != "" {
			value := terminal{taskID: task, toolUseID: nonempty(notification, "toolUseId"), ownerID: nonempty(notification, "ownerAgentId"), status: status, summary: nonempty(notification, "summary"), result: resultText(notification.Get("result")), outputFile: nonempty(notification, "outputFile"), timestamp: nonempty(notification, "timestamp")}
			if value.ownerID == "" {
				value.ownerID = owner
			}
			if value.timestamp == "" {
				value.timestamp = base.timestamp
			}
			out.terminals = append(out.terminals, value)
		}
	}
	for _, text := range stringsIn(message.Get("content")) {
		if !strings.Contains(text, "<task-notification>") {
			continue
		}
		status := normalizeStatus(xml(text, "status"))
		task := xml(text, "task-id")
		if status != "" && task != "" {
			out.terminals = append(out.terminals, terminal{taskID: task, toolUseID: xml(text, "tool-use-id"), ownerID: owner, status: status, summary: decodeXML(xml(text, "summary")), result: decodeXML(xml(text, "result")), outputFile: decodeXML(xml(text, "output-file")), timestamp: base.timestamp})
		}
	}
}
func (h *History) lifecycle(ctx context.Context, id string, dirs []sessionDirectory) (lifecycle, error) {
	result := lifecycle{launches: map[string]launch{}}
	type file struct {
		path, owner string
		mtime       float64
	}
	files := []file{}
	for _, session := range dirs {
		files = append(files, file{path: filepath.Join(filepath.Dir(session.path), id+".jsonl")})
		root := filepath.Join(session.path, "subagents")
		entries, e := h.options.Files.ReadDir(root)
		if e != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "agent-") || !strings.HasSuffix(entry.Name(), ".jsonl") || entry.Name() == "agent-.jsonl" {
				continue
			}
			path := filepath.Join(root, entry.Name())
			mtime := 0.0
			if stat, e := h.options.Files.Stat(path); e == nil {
				mtime = float64(stat.ModTime().UnixNano()) / 1e6
			}
			files = append(files, file{path, strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "agent-"), ".jsonl"), mtime})
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].mtime < files[j].mtime })
	seen := map[string]bool{}
	records, total := 0, 0
	for _, file := range files {
		if seen[file.path] {
			continue
		}
		seen[file.path] = true
		items, e := h.projection(ctx, file.path)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return result, e
		}
		for _, item := range items {
			records++
			raw, _ := protocol.ParseAndStringifyJavaScriptJSON(item)
			total += len(raw)
			if records > 10000 || total > 3*1024*1024 {
				return result, &APIError{503, "Workflow history exceeds the bounded reconstruction budget", "WORKFLOW_HISTORY_INCOMPLETE"}
			}
			collectLifecycle(jsonObject(item), file.owner, &result)
		}
	}
	return result, nil
}
func findTerminal(terms []terminal, launched launch, launches map[string]launch) *terminal {
	launchedAt, valid := timestamp(launched.timestamp)
	nextAt := 0.0
	hasNext := false
	for _, candidate := range launches {
		at, ok := timestamp(candidate.timestamp)
		if candidate.runID != launched.runID && candidate.taskID == launched.taskID && candidate.ownerID == launched.ownerID && (launched.toolUseID == "" || candidate.toolUseID == "" || candidate.toolUseID == launched.toolUseID) && ok && (!valid || at > launchedAt) && (!hasNext || at < nextAt) {
			nextAt, hasNext = at, true
		}
	}
	var selected *terminal
	for _, candidate := range terms {
		at, ok := timestamp(candidate.timestamp)
		if candidate.taskID != launched.taskID || candidate.ownerID != launched.ownerID || launched.toolUseID != "" && candidate.toolUseID != "" && candidate.toolUseID != launched.toolUseID || valid && ok && at < launchedAt || hasNext && ok && at >= nextAt {
			continue
		}
		if !ok {
			at = 0
		}
		previous := 0.0
		if selected != nil {
			previous, _ = timestamp(selected.timestamp)
		}
		if selected == nil || at > previous {
			copy := candidate
			selected = &copy
		}
	}
	return selected
}

func (h *History) listRuns(ctx context.Context, id *string, limit *int) ([]protocol.JSONObject, error) {
	runs := []protocol.JSONObject{}
	dirs, e := h.runDirs(ctx, id)
	if e != nil {
		return nil, e
	}
	sessions, e := h.sessionDirs(ctx, id)
	if e != nil {
		return nil, e
	}
	for _, dir := range dirs {
		entries, e := h.options.Files.ReadDir(dir.path)
		if e != nil {
			continue
		}
		for _, entry := range entries {
			match := runScript.FindStringSubmatch(entry.Name())
			if len(match) == 0 {
				continue
			}
			path := filepath.Join(dir.path, entry.Name())
			stat, e := h.options.Files.Stat(path)
			if e != nil {
				continue
			}
			agents, e := h.journal(ctx, dir.id, match[2])
			if e != nil {
				return nil, e
			}
			runs = append(runs, protocol.NewJSONObject(protocol.JSONField{Name: "runId", Value: quoted(match[2])}, protocol.JSONField{Name: "sessionId", Value: quoted(dir.id)}, protocol.JSONField{Name: "workflowName", Value: quoted(match[1])}, protocol.JSONField{Name: "scriptPath", Value: quoted(path)}, protocol.JSONField{Name: "startedAt", Value: valueRaw(float64(stat.ModTime().UnixNano()) / 1e6)}, protocol.JSONField{Name: "completedAgents", Value: valueRaw(len(agents))}, protocol.JSONField{Name: "status", Value: quoted("unknown")}))
		}
	}
	seen := map[string]bool{}
	for _, run := range runs {
		id := run.String("sessionId")
		if seen[id] {
			continue
		}
		seen[id] = true
		scoped := []sessionDirectory{}
		for _, session := range sessions {
			if session.id == id {
				scoped = append(scoped, session)
			}
		}
		life, e := h.lifecycle(ctx, id, scoped)
		if e != nil {
			return nil, e
		}
		for i := range runs {
			if runs[i].String("sessionId") != id {
				continue
			}
			status := "running"
			if launched, ok := life.launches[runs[i].String("runId")]; ok {
				if end := findTerminal(life.terminals, launched, life.launches); end != nil {
					status = end.status
				}
			}
			runs[i].Set("status", quoted(status))
		}
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return jsonNumber(runs[i].Get("startedAt")) > jsonNumber(runs[j].Get("startedAt"))
	})
	if limit != nil && *limit < len(runs) {
		runs = runs[:*limit]
	}
	return runs, nil
}
func jsonNumber(raw json.RawMessage) float64 {
	var value float64
	_ = json.Unmarshal(raw, &value)
	return value
}
func (s *Service) checkHistory(ctx context.Context) error {
	if e := s.run(ctx, s.enabled); e != nil {
		return e
	}
	if s.history == nil {
		return &CapabilityError{"native Workflow history producer"}
	}
	return nil
}
func (s *Service) ListRuns(ctx context.Context, id *string, limit *int) ([]protocol.JSONObject, error) {
	if e := s.checkHistory(ctx); e != nil {
		return nil, e
	}
	return s.history.listRuns(ctx, id, limit)
}
func (s *Service) GetRun(ctx context.Context, id, runID string) (protocol.JSONObject, error) {
	if e := s.checkHistory(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	runs, e := s.history.listRuns(ctx, &id, nil)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	for _, run := range runs {
		if run.String("runId") != runID {
			continue
		}
		raw, e := s.history.options.Files.ReadFile(run.String("scriptPath"))
		if e != nil {
			return protocol.JSONObject{}, e
		}
		agents, e := s.history.journal(ctx, id, runID)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		decoded := string(protocol.DecodeJavaScriptUTF8(raw))
		run.Set("script", quoted(decoded))
		e = s.run(ctx, func(scope *cooperative.Scope) error {
			parsed, e := s.definitions.Metadata.ParseScript(scope, decoded)
			if e != nil {
				return e
			}
			if parsed.ErrorUnits == nil {
				run.Set("description", parsed.Meta.Get("description"))
				run.Set("phases", parsed.Meta.Get("phases"))
			}
			return nil
		})
		if e != nil {
			return protocol.JSONObject{}, e
		}
		run.Set("agents", valueRaw(agents))
		return run, nil
	}
	return protocol.JSONObject{}, &APIError{404, "Unknown workflow run: " + runID, "NOT_FOUND"}
}

func utf16Text(units []uint16) string { return string(utf16.Decode(units)) }
