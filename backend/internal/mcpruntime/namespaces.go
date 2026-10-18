package mcpruntime

import (
	"context"
	"errors"
)

// ShareNamespace joins an unused, actual runtime configuration to this process
// graph. Source memoizes by cwd/name/config; siblings with the same cwd share
// credentials, discovery and the original connection rather than handshaking
// again. The first actual configuration for a cwd supplies that namespace's
// auth/policy/process context. Only the root owns final graph shutdown.
func (s *Service) ShareNamespace(candidate *Service) (*Service, error) {
	if s == nil || candidate == nil {
		return nil, errors.New("MCP namespace requires actual process runtimes")
	}
	root := s.namespaceOwner()
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.closed || root.closeDone != nil {
		return nil, context.Canceled
	}
	if candidate == root {
		return root, nil
	}
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.closed || candidate.namespaceRoot != nil || len(candidate.entries) > 0 || len(candidate.sdkAttempts) > 0 {
		return nil, errors.New("MCP namespace must be unstarted and independently owned")
	}
	if candidate.options.CWD == root.options.CWD {
		return root, nil
	}
	if root.namespaces == nil {
		root.namespaces = map[string]*Service{}
	}
	if current := root.namespaces[candidate.options.CWD]; current != nil {
		return current, nil
	}
	candidate.options.AuthenticationCache = root.options.AuthenticationCache
	candidate.namespaceRoot = root
	root.namespaces[candidate.options.CWD] = candidate
	return candidate, nil
}
func (s *Service) namespaceOwner() *Service {
	if s.namespaceRoot != nil {
		return s.namespaceRoot
	}
	return s
}
func (s *Service) OwnsConnection(connection Connection) bool {
	if s == nil || connection.runtime == nil || s.namespaceOwner() != connection.runtime.namespaceOwner() {
		return false
	}
	owner := connection.runtime
	if connection.entry == nil {
		return connection.Status == "disabled" && connection.Client == nil && len(connection.Tools) == 0
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.key(connection.Name, connection.Config) == owner.key(connection.entry.value.Name, connection.entry.value.Config) && connection.entry.value.Client == connection.Client
}

// RefreshConnection retains the namespace that minted the actual peer. A child
// cannot silently reconnect its inherited parent's peer under the child cwd.
func (s *Service) RefreshConnection(ctx context.Context, connection Connection) (Connection, error) {
	if !s.OwnsConnection(connection) {
		return Connection{}, errors.New("MCP connection belongs to a different process graph")
	}
	if connection.Config.String("type") == "sdk" {
		enabled, err := connection.runtime.ExecutionEnabled(ctx, connection.Name, connection.Config)
		if err != nil {
			return Connection{}, err
		}
		if !enabled {
			connection.Status, connection.Client, connection.Tools = "disabled", nil, nil
		}
		return clone(connection), nil
	}
	return connection.runtime.Connect(ctx, connection.Name, connection.Config)
}
func (s *Service) DisconnectConnection(ctx context.Context, connection Connection) error {
	if !s.OwnsConnection(connection) {
		return errors.New("MCP connection belongs to a different process graph")
	}
	if connection.Config.String("type") == "sdk" {
		return connection.runtime.CloseProbe(ctx, connection)
	}
	return connection.runtime.Disconnect(ctx, connection.Name, connection.Config)
}
func (s *Service) CloseConnection(ctx context.Context, connection Connection) error {
	if connection.runtime == nil || s.namespaceOwner() != connection.runtime.namespaceOwner() {
		return errors.New("MCP connection belongs to a different process graph")
	}
	// Original cleanup closes its peer, never a replacement with the same key.
	return connection.runtime.CloseProbe(ctx, connection)
}
func (s *Service) closeSharedNamespaces(ctx context.Context) error {
	root := s.namespaceOwner()
	// Views are borrowed. Their worker owns only explicitly inline peers, whose
	// real cleanup calls CloseConnection; it cannot dispose named/parent peers.
	if root != s {
		return nil
	}
	root.mu.Lock()
	if root.closeDone == nil {
		root.closeDone = make(chan struct{})
		root.closed = true
		views := make([]*Service, 0, len(root.namespaces))
		for _, view := range root.namespaces {
			views = append(views, view)
		}
		go func() {
			failures := []error{drainNamespace(root)}
			for _, view := range views {
				failures = append(failures, drainNamespace(view))
			}
			if root.options.AuthenticationCache != nil {
				failures = append(failures, root.options.AuthenticationCache.Join(context.Background()))
			}
			root.mu.Lock()
			root.closeErr = errors.Join(failures...)
			close(root.closeDone)
			root.mu.Unlock()
		}()
	}
	done := root.closeDone
	root.mu.Unlock()
	select {
	case <-done:
		root.mu.Lock()
		e := root.closeErr
		root.mu.Unlock()
		return e
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func drainNamespace(s *Service) error {
	s.mu.Lock()
	s.closed = true
	entries := map[*pending]bool{}
	for _, entry := range s.entries {
		entries[entry] = true
	}
	for entry := range s.sdkAttempts {
		entries[entry] = true
	}
	discoveries := make([]*toolDiscovery, 0, len(s.discoveries))
	for discovery := range s.discoveries {
		discoveries = append(discoveries, discovery)
	}
	prompts := make([]*promptDiscovery, 0, len(s.promptReads))
	for read := range s.promptReads {
		prompts = append(prompts, read)
	}
	resources := make([]*resourceDiscovery, 0, len(s.resourceReads))
	for read := range s.resourceReads {
		resources = append(resources, read)
	}
	s.resourceCache = nil
	s.resourceOrder = nil
	s.promptCache = nil
	s.promptOrder = nil
	s.entries = map[string]*pending{}
	s.sdkAttempts = map[*pending]bool{}
	s.sdkCurrent = map[string]*pending{}
	s.toolCache = map[string]*toolDiscovery{}
	s.toolOrder = nil
	s.mu.Unlock()
	for entry := range entries {
		entry.cancel()
	}
	failures := []error{}
	for entry := range entries {
		<-entry.done
		if entry.value.Client != nil {
			failures = append(failures, entry.value.Client.Close())
		}
	}
	for _, discovery := range discoveries {
		<-discovery.done
	}
	for _, read := range prompts {
		<-read.done
	}
	for _, read := range resources {
		<-read.done
	}
	return errors.Join(failures...)
}
