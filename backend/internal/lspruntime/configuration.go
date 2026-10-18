package lspruntime

import (
	"context"
	"sync"
)

// BeginConfiguration is the original generation fence during plugin reload.
// The stable concrete manager binding points at the new empty configuration;
// old process shutdown is owned independently and cannot publish new servers.
func (m *Manager) BeginConfiguration() uint64 {
	m.mu.Lock()
	if m.closed {
		generation := m.generation
		m.mu.Unlock()
		return generation
	}
	old, ready, once := append([]*server{}, m.servers...), m.ready, m.readyOnce
	m.generation++
	generation := m.generation
	m.servers = nil
	m.opened = map[string]string{}
	m.initError = nil
	m.ready = make(chan struct{})
	m.readyOnce = &sync.Once{}
	if len(old) > 0 {
		m.jobs.Add(1)
	}
	m.mu.Unlock()
	once.Do(func() { close(ready) })
	if len(old) > 0 {
		go func() {
			defer m.jobs.Done()
			for _, s := range old {
				s.mu.Lock()
				peer := s.client
				s.mu.Unlock()
				if peer != nil {
					_ = peer.stop(m.ctx)
				}
				s.mu.Lock()
				s.state = "stopped"
				s.mu.Unlock()
			}
		}()
	}
	return generation
}

func (m *Manager) SetEnvironment(environment map[string]string) {
	if m == nil {
		return
	}
	copy := map[string]string{}
	for k, v := range environment {
		copy[k] = v
	}
	m.mu.Lock()
	m.options.Environment = copy
	m.mu.Unlock()
}

// JoinNotifications is an observation of the real owned I/O, useful at a
// process boundary; ordinary Write/Edit never await it.
func (m *Manager) JoinNotifications(ctx context.Context) error {
	done := make(chan struct{})
	go func() { m.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
