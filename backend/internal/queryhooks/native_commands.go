package queryhooks

import (
	"context"
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
	frontmatter      map[string]map[string][]nativeFrontmatterGroup
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
	return s.registerNativeFrontmatterHooks(sid, hooks, "", false, true)
}
func (s *Service) ClearNativeAgentHooks(sid string) {
	store := s.NativeCommandStore()
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.events, sid)
	if _, tracked := store.frontmatter[sid]; tracked {
		delete(store.promptSuccess, sid)
	}
	delete(store.frontmatter, sid)
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
	for event, hooks := range store.events[sid] {
		if _, tracked := store.frontmatter[sid][event]; !tracked {
			count += len(hooks)
		}
	}
	for _, groups := range store.frontmatter[sid] {
		for _, group := range groups {
			count += len(group.hooks)
		}
	}
	return count
}
func (s *Service) nativeCommandMatchers(event, sid string) []commandMatcher {
	matched := s.nativeMatchedCommandMatchers(event, sid, false)
	out := make([]commandMatcher, len(matched))
	for i, row := range matched {
		out[i] = row.commandMatcher
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
