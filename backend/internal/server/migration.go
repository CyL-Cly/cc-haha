package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type migrationOperation struct {
	done chan struct{}
	err  error
}

// Protected by Server.mu, including the admission barrier shared with API work.
type migrationState struct {
	active, validation     bool
	quiescence, activation *migrationOperation
	delegate               *Server
	listener               net.Addr
}

func migrationUnavailable(w http.ResponseWriter) {
	writeJSON(w, 503, map[string]string{"error": "Data migration is in progress", "code": "MIGRATION_IN_PROGRESS"})
}

func (s *Server) migrationAdmissionFailure(w http.ResponseWriter) {
	s.mu.Lock()
	maintenance := s.migration.active
	s.mu.Unlock()
	if maintenance {
		migrationUnavailable(w)
	} else {
		w.WriteHeader(499)
	}
}

func (s *Server) beginMigrationHTTPAdmission() (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.migration.active {
		return nil, false
	}
	s.apiOperations.Add(1)
	return s.apiOperations.Done, true
}

func (s *Server) setMigrationListener(address net.Addr) {
	s.mu.Lock()
	s.migration.listener = address
	delegate := s.migration.delegate
	s.mu.Unlock()
	if s.collaboration != nil {
		s.setCollaborationEndpoint(address)
	}
	if delegate != nil {
		delegate.setMigrationListener(address)
	}
}

func (s *Server) handleMigrationIngress(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	delegate, active := s.migration.delegate, s.migration.active
	s.mu.Unlock()
	if delegate != nil {
		delegate.ServeHTTP(w, r)
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/runtime/migration/") {
		s.migrationHTTP(w, r)
		return true
	}
	if active && r.URL.Path != "/health" {
		migrationUnavailable(w)
		return true
	}
	return false
}

func (s *Server) migrationAuthorized(r *http.Request) bool {
	address := r.RemoteAddr
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsLoopback() || !s.options.Authorization.LocalAuthorized(r.Header, nil) {
		return false
	}
	for header := range r.Header {
		for _, blocked := range []string{"Origin", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "Via"} {
			if strings.EqualFold(header, blocked) {
				return false
			}
		}
	}
	return true
}

func (s *Server) migrationHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.migrationAuthorized(r) {
		writeJSON(w, 403, map[string]string{"error": "Local desktop credential required"})
		return
	}
	var value any
	var err error
	path := r.URL.Path
	switch {
	case r.Method == "GET" && strings.HasSuffix(path, "/preview"):
		value, err = s.migrationPreview(r.Context())
	case r.Method == "POST" && strings.HasSuffix(path, "/quiesce"):
		err = s.quiesceForMigration(r.Context(), false)
		value = map[string]bool{"quiesced": true}
	case r.Method == "POST" && strings.HasSuffix(path, "/recover"):
		err = s.quiesceForMigration(r.Context(), true)
		value = map[string]bool{"quiesced": true}
	case r.Method == "GET" && strings.HasSuffix(path, "/validate"):
		err = s.validateMigrationData(r.Context())
		value = map[string]bool{"valid": true}
	case r.Method == "POST" && strings.HasSuffix(path, "/activate"):
		err = s.activateMigration(r.Context())
		value = map[string]bool{"activated": true}
	default:
		writeJSON(w, 404, map[string]string{"error": "Unknown migration control"})
		return
	}
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error(), "code": "MIGRATION_RUNTIME_UNSAFE"})
		return
	}
	writeJSON(w, 200, value)
}

type migrationPreview struct {
	ActiveTasks       int `json:"activeTasks"`
	ExternalProcesses int `json:"externalProcesses"`
}

func (s *Server) migrationPreview(ctx context.Context) (migrationPreview, error) {
	s.mu.Lock()
	var tasks int
	for _, r := range s.runtimes {
		r.mu.Lock()
		if !r.retired && (r.live != nil || r.opening != nil || r.admitting || len(r.admission) > 0) {
			tasks++
		}
		r.mu.Unlock()
	}
	s.mu.Unlock()
	if s.options.Scheduler != nil {
		tasks += s.options.Scheduler.MigrationRunningCount()
	}
	owned := []int{os.Getpid()}
	if s.options.MigrationOwnedProcessIDs != nil {
		owned = append(owned, s.options.MigrationOwnedProcessIDs()...)
	}
	external, err := countMigrationProcesses(ctx, s.options.ConfigDir, owned, migrationProcessAlive)
	return migrationPreview{tasks, external}, err
}

// Inspect only registrations; never prune files or treat access denial as dead.
func countMigrationProcesses(ctx context.Context, root string, owned []int, alive func(int) bool) (int, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	own := map[int]bool{}
	for _, pid := range owned {
		own[pid] = true
	}
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		stem := strings.TrimSuffix(name, ".json")
		if stem == "" || strings.IndexFunc(stem, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		pid, err := strconv.Atoi(stem)
		if err != nil || pid <= 0 || uint64(pid) > 9007199254740991 || own[pid] || !alive(pid) {
			continue
		}
		// Contents do not excuse a live process: malformed/unreadable entries
		// must count as external, just as well-formed registrations do.
		count++
	}
	return count, nil
}

func awaitMigration(ctx context.Context, operation *migrationOperation) error {
	select {
	case <-operation.done:
		return operation.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Server) quiesceForMigration(ctx context.Context, recovery bool) error {
	if recovery {
		s.mu.Lock()
		previous := s.migration.quiescence
		s.mu.Unlock()
		if previous != nil {
			if err := awaitMigration(ctx, previous); err == nil {
				return nil
			} else if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			s.mu.Lock()
			if s.migration.quiescence == previous {
				s.migration.quiescence = nil
			}
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	operation := s.migration.quiescence
	if operation == nil {
		operation = &migrationOperation{done: make(chan struct{})}
		s.migration.quiescence = operation
		s.migration.active = true
		go func() {
			operation.err = s.drainForMigration(recovery)
			close(operation.done)
		}()
	}
	s.mu.Unlock()
	return awaitMigration(ctx, operation)
}

func (s *Server) drainForMigration(recovery bool) error {
	preview, err := s.migrationPreview(context.Background())
	if err != nil {
		return err
	}
	if !recovery && preview.ExternalProcesses > 0 {
		return errors.New("Close external CLI sessions before migrating data")
	}
	// Stop actual producers first. Already admitted HTTP settings/task writes
	// retain their server context through the later API drain, even after their
	// initiating browser disconnected.
	if s.options.Scheduler != nil {
		if err := s.options.Scheduler.Stop(context.Background()); err != nil {
			return err
		}
	}
	if err := s.closeTeamRuntime(context.Background()); err != nil {
		return err
	}
	s.mu.Lock()
	runtimes := make([]*runtimeSession, 0, len(s.runtimes))
	for _, runtime := range s.runtimes {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()
	for _, runtime := range runtimes {
		if err := closeRuntime(context.Background(), runtime); err != nil {
			return err
		}
	}
	s.waitAPIOperations()
	return s.Close(context.Background())
}

func (s *Server) activateMigration(ctx context.Context) error {
	s.mu.Lock()
	if !s.migration.validation || s.options.MigrationActivate == nil || s.closed {
		s.mu.Unlock()
		return errors.New("Runtime is not awaiting migration validation")
	}
	operation := s.migration.activation
	if operation != nil {
		select {
		case <-operation.done:
			if operation.err != nil {
				operation = nil
				s.migration.activation = nil
			}
		default:
		}
	}
	if operation == nil {
		operation = &migrationOperation{done: make(chan struct{})}
		s.migration.activation = operation
		go func() {
			operation.err = s.activateMigrationOnce()
			close(operation.done)
		}()
	}
	s.mu.Unlock()
	return awaitMigration(ctx, operation)
}

func (s *Server) activateMigrationOnce() error {
	if err := s.validateMigrationData(context.Background()); err != nil {
		return err
	}
	delegate, err := s.options.MigrationActivate(context.Background())
	if err != nil {
		return err
	}
	if delegate == nil || delegate == s {
		return errors.New("Migration activation requires the actual runtime")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.Join(errors.New("Runtime closed during migration activation"), delegate.Close(context.Background()))
	}
	if s.migration.listener != nil {
		delegate.setMigrationListener(s.migration.listener)
	}
	s.migration.delegate = delegate
	s.migration.validation = false
	s.migration.active = false
	s.mu.Unlock()
	return nil
}

func (s *Server) closeMigrationDelegate(ctx context.Context) error {
	s.mu.Lock()
	operation := s.migration.activation
	s.mu.Unlock()
	if operation != nil {
		select {
		case <-operation.done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	s.mu.Lock()
	delegate := s.migration.delegate
	s.mu.Unlock()
	if delegate != nil {
		return delegate.Close(ctx)
	}
	return nil
}
