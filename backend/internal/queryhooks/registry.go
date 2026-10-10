package queryhooks

// Matcher is an original runtime registration descriptor. Hook kinds identify
// required capabilities; no captured Source callback output can enter it.
type Matcher struct {
	Matcher string
	Hooks   []string
}
type sessionStore struct {
	events map[string][]Matcher
	order  []string
}
type registryState struct {
	registered   map[string][]Matcher
	order        []string
	sessions     map[string]*sessionStore
	sessionOrder []string
	programmatic int
}

func validEvent(event string) bool {
	return event == "Stop" || event == "StopFailure" || event == "SubagentStop" || event == "SessionStart" || event == "PreToolUse" || event == "PostToolUse" || event == "PostToolUseFailure"
}
func validated(event string, matchers []Matcher) error {
	if !validEvent(event) {
		return unsupported("hook event " + event)
	}
	for _, matcher := range matchers {
		if matcher.Matcher != "" {
			return unsupported("runtime matcher")
		}
		for _, kind := range matcher.Hooks {
			if kind != "callback" && kind != "function" {
				return unsupported("runtime hook kind " + kind)
			}
		}
	}
	return nil
}
func copyMatchers(input []Matcher) []Matcher {
	out := make([]Matcher, len(input))
	for index, matcher := range input {
		out[index] = Matcher{matcher.Matcher, append([]string{}, matcher.Hooks...)}
	}
	return out
}
func (service *Service) Register(event string, matchers []Matcher) error {
	if err := service.validatedEvent(event, matchers); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.registry.registered == nil {
		service.registry.registered = map[string][]Matcher{}
	}
	if _, exists := service.registry.registered[event]; !exists {
		service.registry.order = append(service.registry.order, event)
		service.registry.registered[event] = []Matcher{}
	}
	service.registry.registered[event] = append(service.registry.registered[event], copyMatchers(matchers)...)
	return nil
}
func (service *Service) ClearRegistry() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.registry.registered, service.registry.order = nil, nil
}
func (service *Service) SetSession(sid, event string, matchers []Matcher) error {
	if sid == "" {
		return unsupported("empty session hook ID")
	}
	if err := service.validatedEvent(event, matchers); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	store := service.registry.sessions[sid]
	if store == nil {
		store = &sessionStore{events: map[string][]Matcher{}}
		service.registry.sessions[sid] = store
		service.registry.sessionOrder = append(service.registry.sessionOrder, sid)
	}
	if _, exists := store.events[event]; !exists {
		store.order = append(store.order, event)
	}
	store.events[event] = copyMatchers(matchers)
	return nil
}

func (service *Service) validatedEvent(event string, matchers []Matcher) error {
	if (event == "SubagentStart" || event == "TaskCreated" || event == "TaskCompleted" || event == "ConfigChange") && service.options.Profile == ResolvedSettingsProfile {
		return validated("SubagentStop", matchers)
	}
	return validated(event, matchers)
}
func (service *Service) ClearSession(sid string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	delete(service.registry.sessions, sid)
	for index, value := range service.registry.sessionOrder {
		if value == sid {
			service.registry.sessionOrder = append(service.registry.sessionOrder[:index], service.registry.sessionOrder[index+1:]...)
			break
		}
	}
}
func (service *Service) RegisterProgrammatic() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.registry.programmatic++
}
func (service *Service) ClearProgrammatic() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.registry.programmatic = 0
}
