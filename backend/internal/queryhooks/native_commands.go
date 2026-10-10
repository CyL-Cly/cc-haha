package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
)

type CommandBindings struct {
	Async         *AsyncRegistry
	ShellResolver *CommandShellResolver
	Event         func(Context, string, string, string, CommandOutput, string) error
	Owner         *cooperative.Owner
	Factory       *messages.Factory
	Timestamp     func() string
	NowMillis     func() float64
	Input         func(context.Context, Context) (protocol.JSONObject, error)
	Run           func(context.Context, Context, protocol.JSONObject, protocol.JSONObject) (CommandOutput, error)
	Store         *CommandStore
}

func (s *Service) NativeCommandShellResolver() *CommandShellResolver {
	if s == nil || s.options.Commands == nil {
		return nil
	}
	return s.options.Commands.ShellResolver
}

type commandMatcher struct {
	matcher string
	hook    protocol.JSONObject
}
type CommandStore struct {
	mu               sync.Mutex
	events           map[string]map[string][]commandMatcher
	promptSuccess    map[string]map[string]func(*cooperative.Scope) error
	structuredOutput map[string]string
}

func (s *Service) HasNativeCommandBindings(owner *cooperative.Owner) bool {
	return s != nil && s.options.Commands != nil && s.options.Commands.Owner == owner
}

func (s *Service) NativeCommandStore() *CommandStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.options.Commands == nil {
		return nil
	}
	if s.options.Commands.Store == nil {
		s.options.Commands.Store = &CommandStore{events: map[string]map[string][]commandMatcher{}}
	}
	return s.options.Commands.Store
}
func (s *Service) RegisterNativeAgentHooks(sid string, hooks protocol.JSONObject) error {
	if sid == "" || !hooks.IsObject() {
		return errors.New("native Agent hooks require actual Agent identity and declaration")
	}
	store := s.NativeCommandStore()
	if store == nil {
		return unsupported("actual native command hook bindings")
	}
	prepared := map[string][]commandMatcher{}
	// registerFrontmatterHooks iterates original HOOK_EVENTS; Stop is converted
	// before SubagentStop entries are registered, regardless of JSON key order.
	for _, event := range []string{"Stop", "SubagentStop"} {
		var matchers []protocol.JSONObject
		if raw := hooks.Get(event); raw != nil {
			if e := json.Unmarshal(raw, &matchers); e != nil {
				return e
			}
		}
		for _, matcher := range matchers {
			var commands []protocol.JSONObject
			if e := json.Unmarshal(matcher.Get("hooks"), &commands); e != nil {
				return e
			}
			for _, hook := range commands {
				if hook.String("type") != "command" {
					return unsupported("Agent " + event + " hook kind " + hook.String("type"))
				}
				if (string(hook.Get("async")) == "true" || string(hook.Get("asyncRewake")) == "true") && s.options.Commands.Async == nil {
					return unsupported("Agent command hook asynchronous task lifecycle")
				}
				if hook.Get("if") != nil {
					return unsupported("Agent command hook if predicate")
				}
				prepared["SubagentStop"] = append(prepared["SubagentStop"], commandMatcher{matcher.String("matcher"), hook.Clone()})
			}
		}
	}
	for _, field := range hooks.OwnProperties() {
		var event string
		_ = json.Unmarshal(field.NameJSON, &event)
		if event == "Stop" || event == "SubagentStop" {
			continue
		}
		var entries []json.RawMessage
		if json.Unmarshal(field.Value, &entries) != nil || len(entries) > 0 {
			return unsupported("Agent frontmatter hook event " + event)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events[sid] == nil {
		store.events[sid] = map[string][]commandMatcher{}
	}
	for event, rows := range prepared {
		store.events[sid][event] = append(store.events[sid][event], rows...)
	}
	return nil
}
func (s *Service) ClearNativeAgentHooks(sid string) {
	store := s.NativeCommandStore()
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.events, sid)
	delete(store.structuredOutput, sid)
}

func (s *Service) NativeAgentHookCount(sid string) int {
	store := s.NativeCommandStore()
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, hooks := range store.events[sid] {
		count += len(hooks)
	}
	return count
}
func (s *Service) nativeCommandMatchers(event, sid string) []commandMatcher {
	if s.options.Commands == nil || s.options.Commands.Store == nil {
		return nil
	}
	store := s.options.Commands.Store
	store.mu.Lock()
	defer store.mu.Unlock()
	in := store.events[sid][event]
	out := make([]commandMatcher, len(in))
	for i, row := range in {
		out[i] = commandMatcher{row.matcher, row.hook.Clone()}
	}
	return out
}
func nativeCommandMatch(query, matcher string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	if regexp.MustCompile(`^[a-zA-Z0-9_|]+$`).MatchString(matcher) {
		for _, part := range strings.Split(matcher, "|") {
			if nativeLegacyHookName(strings.TrimSpace(part)) == query {
				return true
			}
		}
		return false
	}
	re, e := regexp2.Compile(matcher, regexp2.ECMAScript)
	if e != nil {
		return false
	}
	matched, e := re.MatchString(query)
	if e == nil && matched {
		return true
	}
	for _, name := range []string{"Task", "KillShell", "AgentOutputTool", "BashOutputTool"} {
		if nativeLegacyHookName(name) == query {
			matched, e = re.MatchString(name)
			if e == nil && matched {
				return true
			}
		}
	}
	return false
}
func nativeLegacyHookName(name string) string {
	switch name {
	case "Task":
		return "Agent"
	case "KillShell":
		return "TaskStop"
	case "AgentOutputTool", "BashOutputTool":
		return "TaskOutput"
	}
	return name
}
