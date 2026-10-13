package cloudauth

import "sync"

// AuthStatus is the provider-agnostic SDK auth_status payload. Its lifetime is
// the originating CLI process/session, shared with its in-process Agents.
type AuthStatus struct {
	IsAuthenticating bool     `json:"isAuthenticating"`
	Output           []string `json:"output"`
	Error            *string  `json:"error,omitempty"`
}
type authStatusListener struct {
	id uint64
	fn func(AuthStatus)
}
type AuthStatusManager struct {
	mu        sync.Mutex
	status    AuthStatus
	next      uint64
	listeners []authStatusListener
}

func NewAuthStatusManager() *AuthStatusManager {
	return &AuthStatusManager{status: AuthStatus{Output: []string{}}}
}
func copyAuthStatus(status AuthStatus) AuthStatus {
	status.Output = append([]string{}, status.Output...)
	if status.Error != nil {
		value := *status.Error
		status.Error = &value
	}
	return status
}
func (manager *AuthStatusManager) Status() AuthStatus {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return copyAuthStatus(manager.status)
}
func (manager *AuthStatusManager) change(update func(*AuthStatus)) {
	manager.mu.Lock()
	update(&manager.status)
	status := copyAuthStatus(manager.status)
	manager.mu.Unlock()
	// The original signal iterates a live insertion-ordered Set: unsubscribing
	// a later listener skips it; a newly subscribed listener is visited too.
	var last uint64
	for {
		manager.mu.Lock()
		var next authStatusListener
		for _, listener := range manager.listeners {
			if listener.id > last {
				next = listener
				break
			}
		}
		manager.mu.Unlock()
		if next.fn == nil {
			return
		}
		last = next.id
		next.fn(copyAuthStatus(status))
	}
}
func (manager *AuthStatusManager) Start() {
	manager.change(func(status *AuthStatus) { *status = AuthStatus{IsAuthenticating: true, Output: []string{}} })
}
func (manager *AuthStatusManager) AddOutput(line string) {
	manager.change(func(status *AuthStatus) { status.Output = append(status.Output, line) })
}
func (manager *AuthStatusManager) SetError(value string) {
	manager.change(func(status *AuthStatus) { status.Error = &value })
}
func (manager *AuthStatusManager) End(success bool) {
	manager.change(func(status *AuthStatus) {
		if success {
			*status = AuthStatus{Output: []string{}}
		} else {
			status.IsAuthenticating = false
		}
	})
}
func (manager *AuthStatusManager) Subscribe(fn func(AuthStatus)) func() {
	manager.mu.Lock()
	manager.next++
	id := manager.next
	manager.listeners = append(manager.listeners, authStatusListener{id, fn})
	manager.mu.Unlock()
	return func() {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		for i, listener := range manager.listeners {
			if listener.id == id {
				manager.listeners = append(manager.listeners[:i], manager.listeners[i+1:]...)
				break
			}
		}
	}
}
