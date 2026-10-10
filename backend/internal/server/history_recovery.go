package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

const recoveryResidentBytes = 3 * 1024 * 1024
const recoveryStateRecordBytes = 64 * 1024

type recoveredEvidence struct {
	ordinal int
	message historyObject
}

func (e recoveredEvidence) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"ordinal": e.ordinal, "message": e.message})
}

type historyRecoveryCacheEntry struct {
	version string
	data    json.RawMessage
}
type historyRecoveryReducer struct {
	ctx                                                context.Context
	db                                                 *sql.Tx
	ordinal, omitted                                   int
	complete                                           map[string]bool
	usage                                              map[string]float64
	lastUser, lastTodo, lastTask, goalBase, goalStatus *recoveredEvidence
}

func recoveryJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(data)
}
func recoveryCopy(value historyObject) historyObject {
	result := historyObject{}
	for key, raw := range value {
		result[key] = raw
	}
	return result
}
func recoveryBrief(raw json.RawMessage) json.RawMessage {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil
	}
	if len(units) > 4096 {
		units = units[:4096]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func recoveryContentText(raw json.RawMessage) string {
	var text string
	if string(raw) != "null" && json.Unmarshal(raw, &text) == nil {
		return text
	}
	parts := []string{}
	for _, block := range contentBlocks(raw) {
		if stringField(block, "type") == "text" {
			parts = append(parts, stringField(block, "text"))
		}
	}
	return strings.Join(parts, "\n")
}
func recoveryTool(name string, values ...string) bool {
	for _, v := range values {
		if name == v {
			return true
		}
	}
	return false
}

var recoveryTeammatePattern = regexp.MustCompile(`<teammate-message\s+teammate_id="([^"]+)"[^>]*>\n?([\s\S]*?)\n?</teammate-message>`)
var recoveryBackgroundPattern = regexp.MustCompile(`(?i)\bCommand (?:running in background|was manually backgrounded by user|exceeded[^\n]*?\band was moved to the background) with ID:\s*([A-Za-z0-9_-]+)`)
var recoveryGoalPattern = regexp.MustCompile(`<local-command-(?:stdout|stderr)>([\s\S]*?)</local-command-(?:stdout|stderr)>`)

func (r *historyRecoveryReducer) exec(query string, args ...any) (sql.Result, error) {
	return r.db.ExecContext(r.ctx, query, args...)
}
func (r *historyRecoveryReducer) saveEvidence(e recoveredEvidence, category string) error {
	data := recoveryJSON(e.message)
	if len(data) > recoveryStateRecordBytes {
		r.omitted++
		r.complete[category] = false
		return nil
	}
	var previous string
	err := r.db.QueryRowContext(r.ctx, "SELECT category FROM evidence WHERE id = ?", stringField(e.message, "id")).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && previous != category {
		r.omitted++
		r.complete[previous] = false
	}
	_, err = r.exec("INSERT OR REPLACE INTO evidence VALUES (?, ?, ?, ?)", stringField(e.message, "id"), e.ordinal, string(data), category)
	return err
}

func (r *historyRecoveryReducer) consume(raw json.RawMessage, _ bool, _ int64) error {
	r.ordinal++
	entry := historyRecord(raw)
	inherited := ""
	if parent := stringField(entry, "parentUuid"); parent != "" {
		var owner sql.NullString
		err := r.db.QueryRowContext(r.ctx, "SELECT owner FROM parents WHERE id = ?", parent).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		inherited = owner.String
	}
	owner := stringField(entry, "parent_tool_use_id")
	if owner == "" && string(entry["isSidechain"]) == "true" {
		owner = inherited
	}
	rawMessage := historyRecord(entry["message"])
	parentOwner := inherited
	for _, block := range contentBlocks(rawMessage["content"]) {
		if stringField(block, "type") == "tool_use" && recoveryTool(stringField(block, "name"), "Agent", "Task") {
			var id string
			if json.Unmarshal(block["id"], &id) == nil && string(block["id"]) != "null" {
				parentOwner = id
				break
			}
		}
	}
	if id := stringField(entry, "uuid"); id != "" {
		var binding any
		if parentOwner != "" {
			binding = parentOwner
		}
		if _, e := r.exec("INSERT OR REPLACE INTO parents VALUES (?, ?)", id, binding); e != nil {
			return e
		}
	}
	if notice := taskNotification(entry); notice != nil {
		compact := recoveryCopy(notice)
		for _, key := range []string{"summary", "result"} {
			if stringField(notice, key) != "" {
				value := recoveryBrief(notice[key])
				if value == nil {
					delete(compact, key)
				} else {
					compact[key] = value
				}
			}
		}
		data := recoveryJSON(compact)
		if len(data) > recoveryStateRecordBytes {
			r.omitted++
			r.complete["activity"] = false
		} else {
			var agent any
			if name := stringField(notice, "ownerAgentId"); name != "" {
				agent = name
			}
			key := string(recoveryJSON([]any{agent, stringField(notice, "toolUseId")}))
			if _, e := r.exec("INSERT OR REPLACE INTO notices VALUES (?, ?, ?)", key, r.ordinal, string(data)); e != nil {
				return e
			}
		}
		if stringField(rawMessage, "role") == "user" {
			return nil
		}
	}
	projected, visible := messageEntryWithOwner(entry, owner)
	if !visible {
		return nil
	}
	message := historyRecord(projected)
	if usage := historyRecord(message["usage"]); usage != nil {
		count := true
		if key := stringField(message, "usageKey"); key != "" {
			changed, e := r.exec("INSERT OR IGNORE INTO usage_keys VALUES (?)", key)
			if e != nil {
				return e
			}
			n, e := changed.RowsAffected()
			if e != nil {
				return e
			}
			count = n > 0
		}
		if count {
			for source, target := range map[string]string{"input_tokens": "input_tokens", "output_tokens": "output_tokens", "cache_read_input_tokens": "cache_read_tokens", "cache_creation_input_tokens": "cache_creation_tokens"} {
				var amount float64
				if json.Unmarshal(usage[source], &amount) == nil && amount > 0 && !math.IsInf(amount, 0) && !math.IsNaN(amount) {
					r.usage[target] += amount
				}
			}
		}
	}
	if owner != "" || string(entry["isSidechain"]) == "true" {
		return nil
	}
	base := recoveryCopy(message)
	delete(base, "usage")
	delete(base, "toolUseResult")
	if stringField(message, "type") == "user" {
		last := recoveryCopy(base)
		last["content"] = jsonString("[user message]")
		r.lastUser = &recoveredEvidence{r.ordinal, last}
		parts := [][]uint16{}
		for _, match := range recoveryTeammatePattern.FindAllStringSubmatch(recoveryContentText(message["content"]), -1) {
			body := trimJS(match[2])
			lifecycle := historyRecord([]byte(body))
			if recoveryTool(stringField(lifecycle, "type"), "shutdown_approved", "shutdown_rejected", "shutdown_request", "teammate_terminated", "idle_notification") {
				continue
			}
			changed, e := r.exec("INSERT OR IGNORE INTO teammates VALUES (?)", match[1])
			if e != nil {
				return e
			}
			n, e := changed.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				prefix := utf16.Encode([]rune(`<teammate-message teammate_id="` + match[1] + `">`))
				bodyUnits := utf16.Encode([]rune(body))
				if len(bodyUnits) > 4096 {
					bodyUnits = bodyUnits[:4096]
				}
				prefix = append(prefix, bodyUnits...)
				prefix = append(prefix, utf16.Encode([]rune(`</teammate-message>`))...)
				parts = append(parts, prefix)
			}
		}
		if len(parts) > 0 {
			compact := recoveryCopy(base)
			joined := []uint16{}
			for i, part := range parts {
				if i > 0 {
					joined = append(joined, 10)
				}
				joined = append(joined, part...)
			}
			compact["content"] = protocol.QuoteJavaScriptUTF16(joined)
			if e := r.saveEvidence(recoveredEvidence{r.ordinal, compact}, "activity"); e != nil {
				return e
			}
		}
	}
	if stringField(message, "type") == "system" {
		var content string
		if json.Unmarshal(message["content"], &content) != nil {
			return nil
		}
		match := recoveryGoalPattern.FindStringSubmatch(content)
		if len(match) == 0 {
			return nil
		}
		output := trimJS(match[1])
		isBase := strings.HasPrefix(output, "Goal set:") || strings.HasPrefix(output, "Goal cleared.") || strings.HasPrefix(output, "Goal cleared:") || strings.HasPrefix(output, "No active goal.")
		if !isBase && !strings.HasPrefix(output, "Goal continuing:") && !strings.HasPrefix(output, "Goal marked complete.") {
			return nil
		}
		goal := recoveryCopy(base)
		goal["content"] = jsonString("<local-command-stdout>" + output + "</local-command-stdout>")
		evidence := &recoveredEvidence{r.ordinal, goal}
		if isBase {
			r.goalBase = nil
			r.goalStatus = nil
			r.complete["goal"] = true
			if len(recoveryJSON(evidence)) <= recoveryStateRecordBytes {
				r.goalBase = evidence
			} else {
				r.omitted++
				r.complete["goal"] = false
			}
		} else if len(recoveryJSON(evidence)) <= recoveryStateRecordBytes {
			r.goalStatus = evidence
		} else {
			r.omitted++
			r.complete["goal"] = false
		}
		return nil
	}
	var blocks []json.RawMessage
	if json.Unmarshal(message["content"], &blocks) != nil || string(message["content"]) == "null" {
		return nil
	}
	agents, calls, results := []historyObject{}, []historyObject{}, []historyObject{}
	for _, rawBlock := range blocks {
		block := historyRecord(rawBlock)
		id := stringField(block, "id")
		name := stringField(block, "name")
		if stringField(block, "type") == "tool_use" && id != "" {
			input := historyRecord(block["input"])
			if recoveryTool(name, "Write", "Edit", "MultiEdit", "NotebookEdit") {
				calls = append(calls, block)
			}
			if recoveryTool(name, "TodoWrite", "TaskCreate", "TaskUpdate", "TaskGet", "TaskList") {
				last := recoveryCopy(base)
				last["content"] = recoveryJSON([]any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}})
				r.lastTask = &recoveredEvidence{r.ordinal, last}
				var todos []json.RawMessage
				if name == "TodoWrite" && json.Unmarshal(input["todos"], &todos) == nil && string(input["todos"]) != "null" {
					todo := recoveryCopy(base)
					todo["content"] = recoveryJSON([]historyObject{block})
					evidence := &recoveredEvidence{r.ordinal, todo}
					r.complete["todos"] = len(recoveryJSON(evidence)) <= recoveryStateRecordBytes
					if r.complete["todos"] {
						r.lastTodo = evidence
					} else {
						r.lastTodo = nil
						r.omitted++
					}
				}
			}
			if recoveryTool(name, "Agent", "Task") {
				filtered := historyObject{}
				for _, key := range []string{"name", "subagent_type", "run_in_background"} {
					if value := input[key]; value != nil {
						filtered[key] = value
					}
				}
				if value := recoveryBrief(input["description"]); value != nil {
					filtered["description"] = value
				}
				agents = append(agents, historyObject{"type": jsonString("tool_use"), "id": block["id"], "name": block["name"], "input": recoveryJSON(filtered)})
			}
			if recoveryTool(name, "Bash", "PowerShell") {
				filtered := historyObject{}
				for _, key := range []string{"description", "command"} {
					if value := recoveryBrief(input[key]); value != nil {
						filtered[key] = value
					}
				}
				if value := input["run_in_background"]; value != nil {
					filtered["run_in_background"] = value
				}
				compact := recoveryCopy(base)
				compact["id"] = jsonString(stringField(base, "id") + ":background:" + id)
				compact["content"] = recoveryJSON([]historyObject{{"type": jsonString("tool_use"), "id": block["id"], "name": block["name"], "input": recoveryJSON(filtered)}})
				data := recoveryJSON(compact)
				if len(data) <= recoveryStateRecordBytes {
					legacy := 0
					if string(input["run_in_background"]) == "true" {
						legacy = 1
					}
					if _, e := r.exec("INSERT OR REPLACE INTO tools VALUES (?, ?, ?, ?, ?)", id, r.ordinal, string(data), legacy, name); e != nil {
						return e
					}
				} else {
					r.omitted++
					r.complete["activity"] = false
				}
			}
		} else if stringField(block, "type") == "tool_result" && stringField(block, "tool_use_id") != "" {
			id := stringField(block, "tool_use_id")
			var ordinal, legacy int
			var data, name string
			e := r.db.QueryRowContext(r.ctx, "SELECT ordinal, json, legacy, name FROM tools WHERE id = ?", id).Scan(&ordinal, &data, &legacy, &name)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			if recoveryTool(name, "Write", "Edit", "MultiEdit", "NotebookEdit") {
				results = append(results, block)
				continue
			}
			taskID := ""
			if structured := historyRecord(message["toolUseResult"]); structured != nil {
				value := structured["backgroundTaskId"]
				if value == nil || string(value) == "null" {
					value = structured["background_task_id"]
				}
				_ = json.Unmarshal(value, &taskID)
			} else if legacy == 1 {
				match := recoveryBackgroundPattern.FindStringSubmatch(recoveryContentText(block["content"]))
				if len(match) > 0 {
					taskID = match[1]
				}
			}
			if taskID == "" {
				continue
			}
			if e = r.saveEvidence(recoveredEvidence{ordinal, historyRecord([]byte(data))}, "activity"); e != nil {
				return e
			}
			compact := recoveryCopy(base)
			compact["content"] = recoveryJSON([]any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": ""}})
			compact["toolUseResult"] = recoveryJSON(map[string]string{"backgroundTaskId": taskID})
			if e = r.saveEvidence(recoveredEvidence{r.ordinal, compact}, "activity"); e != nil {
				return e
			}
		}
	}
	if len(calls) > 0 {
		compact := recoveryCopy(base)
		compact["content"] = recoveryJSON(calls)
		data := recoveryJSON(compact)
		if len(data) > recoveryStateRecordBytes {
			r.omitted++
			r.complete["workspace"] = false
		} else {
			if e := r.saveEvidence(recoveredEvidence{r.ordinal, compact}, "workspace"); e != nil {
				return e
			}
			for _, call := range calls {
				if _, e := r.exec("INSERT OR REPLACE INTO tools VALUES (?, ?, ?, ?, ?)", stringField(call, "id"), r.ordinal, string(data), 0, stringField(call, "name")); e != nil {
					return e
				}
			}
		}
	}
	if len(results) > 0 {
		compact := recoveryCopy(base)
		compact["content"] = recoveryJSON(results)
		if value := message["toolUseResult"]; value != nil {
			compact["toolUseResult"] = value
		}
		if e := r.saveEvidence(recoveredEvidence{r.ordinal, compact}, "workspace"); e != nil {
			return e
		}
	}
	if len(agents) > 0 {
		compact := recoveryCopy(base)
		compact["content"] = recoveryJSON(agents)
		return r.saveEvidence(recoveredEvidence{r.ordinal, compact}, "activity")
	}
	return nil
}

func (s *Server) recoverSessionHistory(ctx context.Context, info sessionInfo) (json.RawMessage, error) {
	version := "memory"
	if info.ephemeral {
		version = "ephemeral"
	}
	if info.memory {
		return recoveryJSON(map[string]any{"sourceVersion": version, "status": "ready", "messages": []any{}, "taskNotifications": []any{}, "tokenUsage": nil, "omittedRecords": 0}), nil
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	file, e := sessionstore.OSFileSystem{}.Open(info.Path)
	if e != nil {
		return nil, e
	}
	stat, e := file.Stat()
	closeErr := file.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	version = fmt.Sprintf("%s:%s:%d:%s", stat.Dev, stat.Ino, stat.Size, stat.MtimeNS)
	s.mu.Lock()
	cached := s.historyRecoveryCache[info.ID]
	if cached.version == version {
		s.touchHistoryRecoveryLocked(info.ID)
		data := append(json.RawMessage(nil), cached.data...)
		s.mu.Unlock()
		return data, nil
	}
	s.mu.Unlock()
	var result json.RawMessage
	e = s.budget.With(ctx, sessionstore.RecoveryLane, func() (failure error) {
		directory, e := os.MkdirTemp("", "claude-history-recovery-")
		if e != nil {
			return e
		}
		var db *sql.DB
		// Source closes the database before removing the scratch directory and
		// propagates either cleanup failure before publishing a cache entry.
		defer func() {
			if db != nil {
				if e := db.Close(); e != nil {
					failure = e
					return
				}
			}
			if e := os.RemoveAll(directory); e != nil {
				failure = e
			}
		}()
		db, e = sql.Open("sqlite", filepath.Join(directory, "state.sqlite"))
		if e != nil {
			return e
		}
		db.SetMaxOpenConns(1)
		_, e = db.ExecContext(ctx, "PRAGMA journal_mode=OFF; PRAGMA cache_size=-512; PRAGMA temp_store=FILE; CREATE TABLE usage_keys (id TEXT PRIMARY KEY); CREATE TABLE parents (id TEXT PRIMARY KEY, owner TEXT); CREATE TABLE tools (id TEXT PRIMARY KEY, ordinal INTEGER, json TEXT, legacy INTEGER, name TEXT); CREATE TABLE evidence (id TEXT PRIMARY KEY, ordinal INTEGER, json TEXT, category TEXT); CREATE TABLE notices (id TEXT PRIMARY KEY, ordinal INTEGER, json TEXT); CREATE TABLE teammates (name TEXT PRIMARY KEY)")
		if e != nil {
			return e
		}
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		r := &historyRecoveryReducer{ctx: ctx, db: tx, complete: map[string]bool{"goal": true, "todos": true, "activity": true, "usage": true, "workspace": true}, usage: map[string]float64{"input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_creation_tokens": 0}}
		limit := int64(8 * 1024 * 1024)
		scan, e := s.reader.Stream(ctx, info.Path, r.consume, sessionstore.StreamOptions{MaxRecordBytes: &limit})
		if e != nil {
			return e
		}
		result, e = r.finish(scan)
		if e != nil {
			return e
		}
		return tx.Commit()
	})
	if e != nil {
		return nil, e
	}
	actualVersion := stringField(historyRecord(result), "sourceVersion")
	s.mu.Lock()
	if s.historyRecoveryCache == nil {
		s.historyRecoveryCache = map[string]historyRecoveryCacheEntry{}
	}
	s.historyRecoveryCache[info.ID] = historyRecoveryCacheEntry{actualVersion, append(json.RawMessage(nil), result...)}
	s.touchHistoryRecoveryLocked(info.ID)
	for len(s.historyRecoveryOrder) > 2 {
		delete(s.historyRecoveryCache, s.historyRecoveryOrder[0])
		s.historyRecoveryOrder = s.historyRecoveryOrder[1:]
	}
	s.mu.Unlock()
	return result, nil
}
func (s *Server) touchHistoryRecoveryLocked(id string) {
	for i, key := range s.historyRecoveryOrder {
		if key == id {
			s.historyRecoveryOrder = append(s.historyRecoveryOrder[:i], s.historyRecoveryOrder[i+1:]...)
			break
		}
	}
	s.historyRecoveryOrder = append(s.historyRecoveryOrder, id)
}

func (r *historyRecoveryReducer) finish(scan sessionstore.StreamResult) (json.RawMessage, error) {
	priority := []recoveredEvidence{}
	selected := map[string]recoveredEvidence{}
	selectedOrder := []string{}
	selectEvidence := func(e recoveredEvidence) {
		id := stringField(e.message, "id")
		if _, exists := selected[id]; !exists {
			selectedOrder = append(selectedOrder, id)
		}
		selected[id] = e
	}
	for i, evidence := range []*recoveredEvidence{r.goalBase, r.goalStatus, r.lastTodo, r.lastTask, r.lastUser} {
		if evidence == nil || i == 3 && r.lastTodo != nil && stringField(evidence.message, "id") == stringField(r.lastTodo.message, "id") {
			continue
		}
		priority = append(priority, *evidence)
		selectEvidence(*evidence)
	}
	// Source budgets the envelope, not just its projected message. Keep the
	// original key names for this independently bounded in-memory prefix.
	priorityBytes := []any{}
	for _, e := range priority {
		priorityBytes = append(priorityBytes, map[string]any{"ordinal": e.ordinal, "message": e.message})
	}
	bytes := len(recoveryJSON(priorityBytes))
	rows, e := r.db.QueryContext(r.ctx, "SELECT ordinal, json, category FROM evidence ORDER BY ordinal DESC")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		if e = r.ctx.Err(); e != nil {
			rows.Close()
			return nil, e
		}
		var ordinal int
		var data, category string
		if e = rows.Scan(&ordinal, &data, &category); e != nil {
			rows.Close()
			return nil, e
		}
		bytes += len(data)
		if bytes > recoveryResidentBytes || len(selected) >= 2048 {
			r.omitted++
			rows.Close()
			categories, e := r.db.QueryContext(r.ctx, "SELECT DISTINCT category FROM evidence WHERE ordinal <= ?", ordinal)
			if e != nil {
				return nil, e
			}
			for categories.Next() {
				var name string
				if e = categories.Scan(&name); e != nil {
					categories.Close()
					return nil, e
				}
				r.complete[name] = false
			}
			e = categories.Err()
			categories.Close()
			if e != nil {
				return nil, e
			}
			break
		}
		message := historyRecord([]byte(data))
		selectEvidence(recoveredEvidence{ordinal, message})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	notices := []json.RawMessage{}
	rows, e = r.db.QueryContext(r.ctx, "SELECT json FROM notices ORDER BY ordinal DESC")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var data string
		if e = rows.Scan(&data); e != nil {
			rows.Close()
			return nil, e
		}
		bytes += len(data)
		if bytes > recoveryResidentBytes || len(notices) >= 2048 {
			r.omitted++
			r.complete["activity"] = false
			break
		}
		if e = r.ctx.Err(); e != nil {
			rows.Close()
			return nil, e
		}
		notices = append(notices, json.RawMessage(data))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i, j := 0, len(notices)-1; i < j; i, j = i+1, j-1 {
		notices[i], notices[j] = notices[j], notices[i]
	}
	if scan.OmittedRecords > 0 {
		r.omitted += scan.OmittedRecords
		for key := range r.complete {
			r.complete[key] = false
		}
	}
	ordered := []recoveredEvidence{}
	for _, id := range selectedOrder {
		ordered = append(ordered, selected[id])
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ordinal < ordered[j].ordinal })
	messages := []historyObject{}
	for _, e := range ordered {
		messages = append(messages, e.message)
	}
	status := "ready"
	for _, value := range r.complete {
		if !value {
			status = "incomplete"
		}
	}
	var usage any
	for _, value := range r.usage {
		if value > 0 {
			usage = r.usage
			break
		}
	}
	return recoveryJSON(map[string]any{"sourceVersion": scan.SourceVersion, "status": status, "completeness": r.complete, "messages": messages, "taskNotifications": notices, "tokenUsage": usage, "omittedRecords": r.omitted}), nil
}

func (s *Server) historyRecoveryHTTP(w http.ResponseWriter, r *http.Request, info sessionInfo) error {
	if r.Method != http.MethodGet {
		return methodNotAllowed(r.Method)
	}
	data, e := s.recoverSessionHistory(requestSignalContext(r), info)
	if e != nil {
		return e
	}
	writeJSON(w, 200, data)
	return nil
}
