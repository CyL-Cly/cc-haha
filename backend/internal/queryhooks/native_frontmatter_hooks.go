package queryhooks

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// MatchedHook's source root is execution context, not a persisted HookCommand
// property. Keep it beside the original declaration through selection/dedup.
type nativeMatchedHook struct {
	commandMatcher
	skillRoot string
}

func (row nativeMatchedHook) clone() nativeMatchedHook {
	row.hook = row.hook.Clone()
	return row
}
func (row nativeMatchedHook) source() string {
	if row.skillRoot != "" {
		return "skill"
	}
	return "settings"
}
func (row nativeMatchedHook) key() (string, error) {
	key, err := nativeEventHookKey(row.hook)
	if row.skillRoot != "" {
		key = row.skillRoot + "\x00" + key
	}
	return key, err
}

type nativeFrontmatterEntry struct {
	hook         protocol.JSONObject
	once, legacy bool
}
type nativeFrontmatterGroup struct {
	matcher, skillRoot string
	hooks              []nativeFrontmatterEntry
}

var nativeFrontmatterEvents = []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged"}

// Only these real executeHooks wrappers pass toolUseContext/getAppState.
// Setup/SessionStart/SubagentStart/task/idle wrappers cannot see session hooks.
func nativeFrontmatterEventHasToolContext(event string) bool {
	switch event {
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied", "PermissionRequest", "UserPromptSubmit":
		return true
	}
	return false
}

func (s *Service) RegisterNativeSkillHooks(scope *cooperative.Scope, sid string, hooks protocol.JSONObject, skillRoot string) error {
	if s == nil || s.options.Commands == nil || !scope.BelongsTo(s.options.Commands.Owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); err != nil {
		return err
	}
	return s.registerNativeFrontmatterHooks(sid, hooks, skillRoot, true, false)
}

func (s *Service) registerNativeFrontmatterHooks(sid string, hooks protocol.JSONObject, skillRoot string, skill, agent bool) error {
	if sid == "" || !hooks.IsObject() {
		return errors.New("native frontmatter hooks require actual session identity and declaration")
	}
	store := s.NativeCommandStore()
	if store == nil {
		return unsupported("actual native command hook bindings")
	}
	type prepared struct {
		event, matcher string
		entry          nativeFrontmatterEntry
	}
	values := []prepared{}
	// Source iterates HOOK_EVENTS, not JSON property order. Agent Stop is mapped
	// before its later SubagentStop declarations; only Skill adds once callbacks.
	for _, event := range nativeFrontmatterEvents {
		raw := hooks.Get(event)
		if raw == nil || string(raw) == "null" {
			continue
		}
		var matchers []protocol.JSONObject
		if err := json.Unmarshal(raw, &matchers); err != nil {
			return err
		}
		target := event
		if agent && event == "Stop" {
			target = "SubagentStop"
		}
		for _, matcher := range matchers {
			var entries []protocol.JSONObject
			if raw := matcher.Get("hooks"); raw != nil && string(raw) != "null" {
				if err := json.Unmarshal(raw, &entries); err != nil {
					return err
				}
			}
			for _, hook := range entries {
				values = append(values, prepared{target, matcher.String("matcher"), nativeFrontmatterEntry{hook: hook.Clone(), once: skill && string(hook.Get("once")) == "true"}})
			}
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, value := range values {
		store.ensureFrontmatterLocked(sid, value.event)
		store.appendFrontmatterLocked(sid, value.event, value.matcher, skillRoot, value.entry)
	}
	return nil
}

func (s *Service) nativeMatchedCommandMatchers(event, sid string, includeFrontmatter bool) []nativeMatchedHook {
	if s.options.Commands == nil || s.options.Commands.Store == nil {
		return nil
	}
	store := s.options.Commands.Store
	store.mu.Lock()
	defer store.mu.Unlock()
	out := []nativeMatchedHook{}
	if groups, tracked := store.frontmatter[sid][event]; tracked {
		for _, group := range groups {
			for _, entry := range group.hooks {
				if includeFrontmatter || entry.legacy {
					out = append(out, nativeMatchedHook{commandMatcher: commandMatcher{group.matcher, entry.hook.Clone()}, skillRoot: group.skillRoot})
				}
			}
		}
		return out
	}
	for _, row := range store.events[sid][event] {
		out = append(out, nativeMatchedHook{commandMatcher: commandMatcher{row.matcher, row.hook.Clone()}})
	}
	return out
}

// Source getSessionHookCallback selects the first logically equal hook from
// the exact matchQuery group (or every group when matchQuery is absent). Its
// one-shot callback removes equal hooks from every matcher, ignoring timeout
// and root. Do not turn that into once-on-attempt or once-per-process behavior.
// Legacy registrations keep their original store and no-extra-state path until
// a real Skill/Agent registration activates a context-bearing group view. That
// view then mirrors subsequent legacy insert/remove operations in Source order.
func (store *CommandStore) ensureFrontmatterLocked(sid, event string) {
	if store.frontmatter == nil {
		store.frontmatter = map[string]map[string][]nativeFrontmatterGroup{}
	}
	if store.frontmatter[sid] == nil {
		store.frontmatter[sid] = map[string][]nativeFrontmatterGroup{}
	}
	if _, tracked := store.frontmatter[sid][event]; tracked {
		return
	}
	store.frontmatter[sid][event] = []nativeFrontmatterGroup{}
	for _, row := range store.events[sid][event] {
		store.appendFrontmatterLocked(sid, event, row.matcher, "", nativeFrontmatterEntry{hook: row.hook.Clone(), legacy: true})
	}
}
func (store *CommandStore) appendFrontmatterLocked(sid, event, matcher, root string, entry nativeFrontmatterEntry) {
	groups := store.frontmatter[sid][event]
	index := -1
	for i, group := range groups {
		if group.matcher == matcher && group.skillRoot == root {
			index = i
			break
		}
	}
	if index < 0 {
		groups = append(groups, nativeFrontmatterGroup{matcher: matcher, skillRoot: root})
		index = len(groups) - 1
	}
	groups[index].hooks = append(groups[index].hooks, entry)
	store.frontmatter[sid][event] = groups
}
func (store *CommandStore) appendLegacyFrontmatterIfTrackedLocked(sid, event string, row commandMatcher) {
	if _, tracked := store.frontmatter[sid][event]; tracked {
		store.appendFrontmatterLocked(sid, event, row.matcher, "", nativeFrontmatterEntry{hook: row.hook.Clone(), legacy: true})
	}
}
func (store *CommandStore) removeEqualFrontmatterLocked(sid, event, key string) {
	groups := store.frontmatter[sid][event]
	next := make([]nativeFrontmatterGroup, 0, len(groups))
	for _, group := range groups {
		entries := make([]nativeFrontmatterEntry, 0, len(group.hooks))
		for _, entry := range group.hooks {
			other, err := nativeEventHookKey(entry.hook)
			if err != nil || other != key {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 {
			group.hooks = entries
			next = append(next, group)
		}
	}
	// Keep an initialized empty view: later legacy additions must still mirror it.
	store.frontmatter[sid][event] = next
	rows := []commandMatcher{}
	for _, row := range store.events[sid][event] {
		other, err := nativeEventHookKey(row.hook)
		if err != nil || other != key {
			rows = append(rows, row)
		} else if row.hook.String("type") == "prompt" {
			delete(store.promptSuccess[sid], event+"\x00"+promptIdentity(row.hook))
		}
	}
	if store.events[sid] != nil {
		store.events[sid][event] = rows
	}
}
func (s *Service) nativeFrontmatterTracked(sid, event string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	var store *CommandStore
	if s.options.Commands != nil {
		store = s.options.Commands.Store
	}
	s.mu.Unlock()
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	_, tracked := store.frontmatter[sid][event]
	return tracked
}
func (s *Service) nativeSessionHookSucceeded(scope *cooperative.Scope, event, matchQuery string, hook protocol.JSONObject) {
	if hook.String("type") == "callback" {
		return
	}
	if s == nil {
		return
	}
	s.mu.Lock()
	var store *CommandStore
	if s.options.Commands != nil {
		store = s.options.Commands.Store
	}
	s.mu.Unlock()
	if store == nil {
		return
	}
	store.mu.Lock()
	empty := store.frontmatter == nil
	store.mu.Unlock()
	if empty {
		return
	}
	key, err := nativeEventHookKey(hook)
	if err != nil {
		return
	}
	sid := s.mainSessionID()
	store.mu.Lock()
	groups := store.frontmatter[sid][event]
	var selected *nativeFrontmatterEntry
	for _, group := range groups {
		if matchQuery != "" && group.matcher != matchQuery {
			continue
		}
		for _, entry := range group.hooks {
			other, err := nativeEventHookKey(entry.hook)
			if err == nil && other == key {
				copy := entry
				selected = &copy
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		store.mu.Unlock()
		return
	}
	if selected.once {
		store.removeEqualFrontmatterLocked(sid, event, key)
		store.mu.Unlock()
		return
	}
	var callback func(*cooperative.Scope) error
	if selected.legacy && selected.hook.String("type") == "prompt" {
		callback = store.promptSuccess[sid][event+"\x00"+promptIdentity(selected.hook)]
	}
	store.mu.Unlock()
	// Source catches success-callback errors. The original legacy-only path
	// still uses promptHookSucceeded unchanged, including its existing error path.
	if callback != nil {
		_ = callback(scope)
	}
}
