package taskstore

import (
	"errors"
	"os"
)

func (s *Service) Directory(list string) string      { return s.tasksDir(list) }
func (s *Service) EnsureDirectory(list string) error { return os.MkdirAll(s.tasksDir(list), 0777) }

// Removal is admitted only through the same active lifecycle transaction that
// committed the terminal DAG. Retained or foreign scoped handles cannot erase
// a later task-list incarnation.
func (s *Service) RemoveTeamDirectory(list string) error {
	t := s.teamTransaction
	if t == nil {
		return errors.New("Team directory removal requires the actual held lifecycle transaction")
	}
	t.operations.RLock()
	defer t.operations.RUnlock()
	if !t.active.Load() || t.list != list {
		return errors.New("Foreign or stale team lifecycle transaction")
	}
	if err := os.RemoveAll(s.tasksDir(list)); err != nil {
		return err
	}
	s.notify()
	return nil
}
