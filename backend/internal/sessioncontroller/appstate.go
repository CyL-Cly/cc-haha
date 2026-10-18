package sessioncontroller

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

// AppState contains only migrated headless restoration consumers. It is not a
// replacement for the React store or the full Source AppState defaults.
type AppState struct {
	Inbox                 []protocol.JSONObject `json:"-"`
	FileHistory           protocol.JSONObject
	Todos                 protocol.JSONObject
	Tasks                 map[string]*BackgroundTask `json:"-"`
	AgentNameRegistry     map[string]string          `json:"-"`
	ViewingAgentTaskID    string                     `json:"-"`
	ViewSelectionMode     string                     `json:"-"`
	ExpandedView          string                     `json:"-"`
	fastMode              *bool
	typedModel            *NativeTypedModelState
	settings              *protocol.JSONObject
	permission            *sdkpermissions.State
	defaultPermissionMode string
}

func emptyAppState() AppState {
	defaultFast := false // Literal Source getDefaultAppState recipe.
	return AppState{ViewSelectionMode: "none", ExpandedView: "none", defaultPermissionMode: "default", fastMode: &defaultFast, FileHistory: protocol.NewJSONObject(protocol.JSONField{Name: "snapshots", Value: raw([]protocol.JSONObject{})}, protocol.JSONField{Name: "trackedFiles", Value: raw([]string{})}, protocol.JSONField{Name: "snapshotSequence", Value: raw(0)}), Todos: protocol.NewJSONObject(), Tasks: map[string]*BackgroundTask{}}
}
func shortPath(value string, cwd string) string {
	relative, err := filepath.Rel(cwd, value)
	if err != nil || strings.HasPrefix(relative, "..") {
		return value
	}
	return relative
}
func (controller *Controller) restoreAppState(metadata protocol.JSONObject, history []*protocol.JSONObject) error {
	if envTruthy(controller.options.ResumeOptions.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]) && !envTruthy(controller.options.ResumeOptions.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
		if err := controller.restoreFileHistory(metadata); err != nil {
			return err
		}
	}
	// SDK uses legacy TodoWrite unless the actual original environment explicitly
	// enables v2 tasks. A malformed last matching use clears the candidate list.
	env := controller.options.ResumeOptions.Environment
	todoV2 := envTruthy(env["CLAUDE_CODE_ENABLE_TASKS"])
	if todoV2 {
		return nil
	}
	for index := len(history) - 1; index >= 0; index-- {
		message := history[index]
		if text(message.Get("type")) != "assistant" {
			continue
		}
		blocks, _ := parseObjects(object(message.Get("message")).Get("content"))
		for _, block := range blocks {
			if text(block.Get("type")) != "tool_use" || text(block.Get("name")) != "TodoWrite" {
				continue
			}
			todos, err := parseObjects(object(block.Get("input")).Get("todos"))
			if err != nil {
				return nil
			}
			validated := []protocol.JSONObject{}
			for _, todo := range todos {
				content, cerr := protocol.DecodeJavaScriptJSONString(todo.Get("content"))
				active, aerr := protocol.DecodeJavaScriptJSONString(todo.Get("activeForm"))
				status := text(todo.Get("status"))
				if cerr != nil || aerr != nil || len(content) == 0 || len(active) == 0 || status != "pending" && status != "in_progress" && status != "completed" {
					return nil
				}
				validated = append(validated, protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: todo.Get("content")}, protocol.JSONField{Name: "status", Value: todo.Get("status")}, protocol.JSONField{Name: "activeForm", Value: todo.Get("activeForm")}))
			}
			if len(validated) > 0 {
				controller.appState.Todos.Set(controller.SessionID(), raw(validated))
			}
			return nil
		}
	}
	return nil
}
func (controller *Controller) AppState() AppState {
	out := AppState{ViewingAgentTaskID: controller.appState.ViewingAgentTaskID, ViewSelectionMode: controller.appState.ViewSelectionMode, ExpandedView: controller.appState.ExpandedView, settings: controller.appState.settings, permission: controller.appState.permission, FileHistory: controller.appState.FileHistory.Clone(), Todos: controller.appState.Todos.Clone()}
	out.Tasks = map[string]*BackgroundTask{}
	for _, row := range controller.appState.Inbox {
		out.Inbox = append(out.Inbox, row.Clone())
	}
	for id, task := range controller.appState.Tasks {
		out.Tasks[id] = &BackgroundTask{Data: cloneBackgroundData(task.Data)}
	}
	if controller.appState.fastMode != nil {
		value := *controller.appState.fastMode
		out.fastMode = &value
	}
	return out
}
func (controller *Controller) ReadCache() *ReadCache { return controller.readCache.Clone() }

// restoreFileHistory is the shared original snapshot conversion. Startup keeps
// its SDK checkpoint gate; the interactive REPL calls it for every nonempty log.
func (controller *Controller) restoreFileHistory(metadata protocol.JSONObject) error {
	return controller.restoreFileHistoryAt(metadata, controller.options.ResumeOptions.OriginalCWD)
}

func (controller *Controller) restoreFileHistoryAt(metadata protocol.JSONObject, originalCWD string) error {
	snapshots, err := parseObjects(metadata.Get("fileHistorySnapshots"))
	if err != nil && metadata.Get("fileHistorySnapshots") != nil {
		return err
	}
	if len(snapshots) > 0 {
		restored := []protocol.JSONObject{}
		tracked := []string{}
		seen := map[string]bool{}
		for _, snapshot := range snapshots {
			original := snapshot.Clone()
			schema := snapshot.Get("schemaVersion")
			var number float64
			if len(schema) != 0 && !bytes.Equal(schema, []byte("null")) {
				var err error
				number, err = strconv.ParseFloat(string(schema), 64)
				if err != nil && !errors.Is(err, strconv.ErrRange) {
					return capability("non-numeric snapshot schemaVersion coercion")
				}
			}
			if number < 2 {
				original.Set("schemaVersion", raw(2))
			}
			for _, field := range []string{"trackedFileBackups", "completedFileBackups"} {
				value := object(snapshot.Get(field))
				if !value.IsObject() {
					if field == "trackedFileBackups" {
						return capability("non-object tracked file-history backups")
					}
					continue
				}
				rewritten := protocol.NewJSONObject()
				for _, property := range value.OwnProperties() {
					units, err := protocol.DecodeJavaScriptJSONString(property.NameJSON)
					if err != nil {
						return err
					}
					path := string(utf16.Decode(units))
					if !bytes.Equal(protocol.QuoteJavaScriptUTF16(units), quote(path)) || strings.ContainsRune(path, 0) {
						return capability("snapshot path with unpaired UTF-16 or NUL")
					}
					relative := shortPath(path, originalCWD)
					rewritten.Set(relative, property.Value)
					if field == "trackedFileBackups" && !seen[relative] {
						seen[relative] = true
						tracked = append(tracked, relative)
					}
				}
				original.Set(field, raw(rewritten))
			}
			restored = append(restored, original)
		}
		controller.appState.FileHistory = protocol.NewJSONObject(protocol.JSONField{Name: "snapshots", Value: raw(restored)}, protocol.JSONField{Name: "trackedFiles", Value: raw(tracked)}, protocol.JSONField{Name: "snapshotSequence", Value: raw(len(restored))})
	}

	return nil
}
