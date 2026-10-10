package cliglobalconfig

import (
	"fmt"
	"strings"
)

type SourceLeaseBusyError struct{ error }

// AcquireSourceDirectoryLease reuses the pinned proper-lockfile mkdir/mtime
// lease for auth.ts's config-directory lock. It does not load or save config.
func (service *Service) AcquireSourceDirectoryLease(path string) (func() error, error) {
	canonical, err := service.options.FS.Realpath(path)
	if err != nil {
		return nil, err
	}
	leaseService := &Service{options: service.options, configHome: service.configHome}
	leaseService.pathOnce.Do(func() { leaseService.path = canonical })
	release, err := leaseService.acquireLock()
	if err != nil && strings.Contains(err.Error(), "Lock file is already being held") {
		return nil, &SourceLeaseBusyError{err}
	}
	if err != nil {
		return nil, fmt.Errorf("OAuth directory lock: %w", err)
	}
	return release, nil
}
