package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) lifecyclePath(list, suffix string) string {
	return filepath.Join(s.configDirectory(), "tasks", ".lifecycle-locks", SanitizePathComponent(list)+suffix)
}
func jsonNumber(value json.RawMessage) (float64, bool) {
	if len(value) == 0 || value[0] == '"' || value[0] == 'n' {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(value), 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func safeNonnegative(value json.RawMessage) bool {
	v, valid := jsonNumber(value)
	return valid && v >= 0 && v <= 9007199254740991 && math.Trunc(v) == v
}
func identityValid(value json.RawMessage) bool {
	var identity protocol.JSONObject
	if identity.UnmarshalJSON(value) != nil || !identity.IsObject() {
		return false
	}
	units, err := protocol.DecodeJavaScriptJSONString(identity.Get("teamName"))
	if err != nil || len(units) == 0 {
		return false
	}
	if _, ok := jsonNumber(identity.Get("createdAt")); !ok {
		return false
	}
	return identity.Get("leadSessionId") == nil || isString(identity.Get("leadSessionId"))
}
func identityEqual(a, b json.RawMessage) bool {
	var left, right protocol.JSONObject
	_ = left.UnmarshalJSON(a)
	_ = right.UnmarshalJSON(b)
	for _, field := range []string{"teamName", "createdAt", "leadSessionId"} {
		if !jsonEqual(left.Get(field), right.Get(field)) {
			return false
		}
	}
	return true
}
func (s *Service) readLifecycle(list string) (lifecycle, error) {
	value, err := os.ReadFile(s.lifecyclePath(list, ".state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return lifecycle{Terminals: []protocol.JSONObject{}}, nil
	}
	if err != nil {
		return lifecycle{}, err
	}
	return parseLifecycle(value, list)
}

func parseLifecycle(value []byte, list string) (lifecycle, error) {
	var state protocol.JSONObject
	invalid := func() (lifecycle, error) {
		return lifecycle{}, fmt.Errorf("Invalid task-list lifecycle state for %s", list)
	}
	if state.UnmarshalJSON(value) != nil || !state.IsObject() {
		return invalid()
	}
	version, ok := jsonNumber(state.Get("schemaVersion"))
	if !ok || version != 1 || !safeNonnegative(state.Get("generation")) {
		return invalid()
	}
	var deleted bool
	if json.Unmarshal(state.Get("deleted"), &deleted) != nil || string(state.Get("deleted")) == "null" {
		return invalid()
	}
	if identity := state.Get("activeIdentity"); identity != nil && !identityValid(identity) {
		return invalid()
	}
	terminals := []protocol.JSONObject{}
	if state.Get("terminals") != nil {
		if json.Unmarshal(state.Get("terminals"), &terminals) != nil || terminals == nil {
			return invalid()
		}
	}
	for _, terminal := range terminals {
		units, err := protocol.DecodeJavaScriptJSONString(terminal.Get("frameId"))
		if err != nil || len(units) == 0 || !safeNonnegative(terminal.Get("generation")) || !identityValid(terminal.Get("identity")) || !isString(terminal.Get("capturedAt")) || !safeNonnegative(terminal.Get("revision")) {
			return invalid()
		}
		var tasks []json.RawMessage
		if json.Unmarshal(terminal.Get("tasks"), &tasks) != nil || tasks == nil {
			return invalid()
		}
		for _, task := range tasks {
			if parseTask(task, false) == nil {
				return invalid()
			}
		}
	}
	generation, _ := jsonNumber(state.Get("generation"))
	return lifecycle{generation, deleted, state.Get("activeIdentity"), terminals}, nil
}

// A writer captures the generation before waiting, then verifies it after
// acquiring the durable lifecycle lock; a queued old Team writer cannot join a
// recreated directory. Readers intentionally observe the current generation.
func (s *Service) withLifecycle(list string, writer bool, run func(lifecycle) error) (err error) {
	if transaction := s.teamTransaction; transaction != nil {
		transaction.operations.RLock()
		defer transaction.operations.RUnlock()
		if transaction.list != list || !transaction.active.Load() {
			return errors.New("Foreign or stale Team lifecycle transaction")
		}
		current, e := s.readLifecycle(list)
		if e != nil {
			return e
		}
		if writer && current.Deleted {
			return fmt.Errorf("Task list %s belongs to a deleted Team lifecycle", list)
		}
		return run(current)
	}
	var expected lifecycle
	if writer {
		expected, err = s.readLifecycle(list)
		if err != nil {
			return err
		}
		if expected.Deleted {
			return fmt.Errorf("Task list %s belongs to a deleted Team lifecycle", list)
		}
	}
	if writer {
		return s.withLifecycleGeneration(list, &expected, run)
	}
	return s.withLifecycleGeneration(list, nil, run)
}

func (s *Service) withLifecycleGeneration(list string, expected *lifecycle, run func(lifecycle) error) (err error) {
	target := s.lifecyclePath(list, ".lock")
	if err = ensureLockTarget(target); err != nil {
		return err
	}
	release, err := s.lock(target)
	if err != nil {
		return err
	}
	defer func() {
		if failure := release(); err == nil {
			err = failure
		}
	}()
	current, err := s.readLifecycle(list)
	if err != nil {
		return err
	}
	if expected != nil && (current.Deleted || current.Generation != expected.Generation || !identityEqual(current.Identity, expected.Identity)) {
		return fmt.Errorf("Task list %s lifecycle changed while waiting for the lock", list)
	}
	return run(current)
}

func (s *Service) withList(list string, run func() error) (err error) {
	current, err := s.readLifecycle(list)
	if err != nil {
		return err
	}
	if current.Deleted {
		return fmt.Errorf("Task list %s belongs to a deleted Team lifecycle", list)
	}
	target := filepath.Join(s.tasksDir(list), ".lock")
	if err = ensureLockTarget(target); err != nil {
		return err
	}
	release, err := s.lock(target)
	if err != nil {
		return err
	}
	defer func() {
		if failure := release(); err == nil {
			err = failure
		}
	}()
	return run()
}
